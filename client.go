package convex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Version is the version of this client. It is sent to the deployment in the
// Convex-Client header as "go-<Version>".
const Version = "0.1.0"

// statusFunctionFailed is the custom status code some Convex deployments use
// for a function that ran and returned an error. The body has the same shape
// as a 200 response.
const statusFunctionFailed = 560

// valueFormat asks the deployment for Convex encoded JSON, so Int64, Bytes and
// special floats come back with their types intact. The official JavaScript
// and Python HTTP clients send the same value.
const valueFormat = "convex_encoded_json"

const (
	defaultQueryRetries   = 3
	defaultInitialBackoff = 100 * time.Millisecond
	defaultMaxBackoff     = 2 * time.Second
	maxErrorBody          = 4 << 10
)

// Client calls functions on one Convex deployment over the HTTP API.
// It is safe for concurrent use. Create it with NewClient.
type Client struct {
	url        string
	httpClient *http.Client

	mu   sync.RWMutex
	auth string // full Authorization header value, or ""

	queryRetries   int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	logHandler     func(path, line string)
	warnHandler    func(state, message string)
	warned         sync.Map

	// Hooks replaced in tests.
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func() float64
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the *http.Client used for requests. The default is
// http.DefaultClient. Use it to set timeouts, proxies or transports.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithAuth sets a user auth token, the same as calling SetAuth.
func WithAuth(token string) Option {
	return func(c *Client) { c.SetAuth(token) }
}

// WithDeployKey authenticates as a deployment admin with a deploy key, the
// same as calling SetAdminAuth.
func WithDeployKey(deployKey string) Option {
	return func(c *Client) { c.SetAdminAuth(deployKey) }
}

// WithQueryRetries sets how many times a query is sent again after a network
// error or a 5xx response. The default is 3. Zero turns retries off.
// Mutations, actions and Function calls are never retried, since sending
// them twice could apply their side effects twice.
func WithQueryRetries(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.queryRetries = n
		}
	}
}

// WithBackoff sets the delay before the first retry and the cap for later
// ones. Each retry waits a random time between zero and
// min(initial * 2^attempt, max), the "full jitter" scheme the Rust client
// uses for reconnects. The defaults are 100ms and 2s.
func WithBackoff(initial, max time.Duration) Option {
	return func(c *Client) {
		if initial > 0 {
			c.initialBackoff = initial
		}
		if max > 0 {
			c.maxBackoff = max
		}
	}
}

// WithLogHandler receives each console line a function logged. Development
// deployments send log lines with every response, production deployments do
// not. By default they are dropped. It is the counterpart of set_debug in the
// Python client.
func WithLogHandler(fn func(functionPath, line string)) Option {
	return func(c *Client) { c.logHandler = fn }
}

// WithWarningHandler receives deprecation notices the deployment sends in
// the x-convex-deprecation-state and x-convex-deprecation-message headers.
// By default each distinct notice is printed once with the log package.
func WithWarningHandler(fn func(state, message string)) Option {
	return func(c *Client) { c.warnHandler = fn }
}

// NewClient returns a client for the deployment at deploymentURL, such as
// "https://happy-otter-123.convex.cloud". The URL is on the deployment's
// Settings page in the Convex dashboard and in CONVEX_URL after running
// `npx convex dev`.
func NewClient(deploymentURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(deploymentURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("convex: invalid deployment URL %q: %w", deploymentURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("convex: invalid deployment URL %q: it must start with https:// or http://", deploymentURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("convex: invalid deployment URL %q: no host", deploymentURL)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("convex: invalid deployment URL %q: it must not have a query or fragment", deploymentURL)
	}
	if strings.HasSuffix(u.Hostname(), ".convex.site") {
		return nil, fmt.Errorf("convex: invalid deployment URL %q: .convex.site is used for HTTP actions, the deployment URL usually ends in .convex.cloud", deploymentURL)
	}
	c := &Client{
		url:            u.String(),
		httpClient:     http.DefaultClient,
		queryRetries:   defaultQueryRetries,
		initialBackoff: defaultInitialBackoff,
		maxBackoff:     defaultMaxBackoff,
		sleep:          sleepCtx,
		jitter:         rand.Float64,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// URL returns the deployment URL the client talks to.
func (c *Client) URL() string { return c.url }

// SetAuth sets the auth token sent with every later call, usually a JWT from
// your auth provider's login flow. Call it again when the token is
// refreshed. An empty token clears auth.
func (c *Client) SetAuth(token string) {
	c.setAuthHeader("Bearer", token)
}

// SetAdminAuth authenticates as a deployment admin with a deploy key from the
// dashboard's deployment settings. Admin auth can call internal functions.
// Keep deploy keys on servers, never ship them to users.
func (c *Client) SetAdminAuth(deployKey string) {
	c.setAuthHeader("Convex", deployKey)
}

// SetAdminAuthActingAs authenticates with a deploy key and runs functions as
// if the given user identity were signed in, which is useful for testing
// functions that read ctx.auth. The identity uses the field names of
// UserIdentity in the Convex docs, for example
// {"subject": "user123", "issuer": "https://example.com", "name": "Ada"}.
func (c *Client) SetAdminAuthActingAs(deployKey string, identity map[string]any) error {
	if deployKey == "" {
		return errors.New("convex: SetAdminAuthActingAs needs a deploy key")
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return fmt.Errorf("convex: encoding acting-as identity: %w", err)
	}
	c.setAuthHeader("Convex", deployKey+":"+base64.StdEncoding.EncodeToString(raw))
	return nil
}

// ClearAuth removes any auth set before. Later calls run unauthenticated.
func (c *Client) ClearAuth() {
	c.setAuthHeader("", "")
}

func (c *Client) setAuthHeader(scheme, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if value == "" {
		c.auth = ""
		return
	}
	c.auth = scheme + " " + value
}

func (c *Client) authHeader() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.auth
}

// Query runs the query function at path, such as "messages:list" or
// "folder/file:name", with the named args and returns its result.
//
// args may be nil, a map with string keys or a struct, and must encode to a
// Convex object. Queries are retried on network errors and 5xx responses,
// see WithQueryRetries.
func (c *Client) Query(ctx context.Context, path string, args any) (any, error) {
	return c.call(ctx, "/api/query", path, args, true)
}

// Mutation runs the mutation function at path with args and returns its
// result. Mutations are not retried.
func (c *Client) Mutation(ctx context.Context, path string, args any) (any, error) {
	return c.call(ctx, "/api/mutation", path, args, false)
}

// Action runs the action function at path with args and returns its result.
// Actions are not retried.
func (c *Client) Action(ctx context.Context, path string, args any) (any, error) {
	return c.call(ctx, "/api/action", path, args, false)
}

// Function runs any query, mutation or action by path through the
// /api/run/{functionIdentifier} endpoint, for when the caller does not know
// the function type. "messages:list" is sent as /api/run/messages/list.
// Function calls are not retried, because the function may be a mutation.
func (c *Client) Function(ctx context.Context, path string, args any) (any, error) {
	runPath, err := RunPath(path)
	if err != nil {
		return nil, err
	}
	argsJSON, err := encodeArgs(args)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		Args   json.RawMessage `json:"args"`
		Format string          `json:"format"`
	}{argsJSON, valueFormat})
	if err != nil {
		return nil, err
	}
	return c.do(ctx, "/api/run/"+runPath, path, body, false)
}

// RunPath converts a function path in "module:function" form to the URL
// form used by /api/run. "messages:list" becomes "messages/list", and a path
// without a function name refers to the module's default export, so
// "messages" becomes "messages/default". Each segment is escaped.
func RunPath(functionPath string) (string, error) {
	if functionPath == "" {
		return "", errors.New("convex: empty function path")
	}
	module, name := functionPath, "default"
	if i := strings.LastIndexByte(functionPath, ':'); i >= 0 {
		module, name = functionPath[:i], functionPath[i+1:]
	}
	if module == "" || name == "" {
		return "", fmt.Errorf("convex: invalid function path %q", functionPath)
	}
	segs := strings.Split(module, "/")
	for i, s := range segs {
		if s == "" {
			return "", fmt.Errorf("convex: invalid function path %q", functionPath)
		}
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/") + "/" + url.PathEscape(name), nil
}

func encodeArgs(args any) (json.RawMessage, error) {
	j, err := ToJSON(args)
	if err != nil {
		var ve *ValueError
		if errors.As(err, &ve) {
			ve.Path = joinKey("args", ve.Path)
		}
		return nil, err
	}
	switch j.(type) {
	case nil:
		return json.RawMessage("{}"), nil
	case map[string]any:
	default:
		return nil, fmt.Errorf("convex: function arguments must be an object (a map or a struct), got %T", args)
	}
	return json.Marshal(j)
}

func (c *Client) call(ctx context.Context, endpoint, path string, args any, retry bool) (any, error) {
	if path == "" {
		return nil, errors.New("convex: empty function path")
	}
	argsJSON, err := encodeArgs(args)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		Path   string          `json:"path"`
		Args   json.RawMessage `json:"args"`
		Format string          `json:"format"`
	}{path, argsJSON, valueFormat})
	if err != nil {
		return nil, err
	}
	return c.do(ctx, endpoint, path, body, retry)
}

func (c *Client) do(ctx context.Context, endpoint, path string, body []byte, retry bool) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := 1
	if retry {
		attempts += c.queryRetries
	}
	for attempt := 0; ; attempt++ {
		v, err := c.once(ctx, endpoint, path, body)
		if err == nil || attempt+1 >= attempts || !retryable(ctx, err) {
			return v, err
		}
		if serr := c.sleep(ctx, c.backoff(attempt)); serr != nil {
			return nil, serr
		}
	}
}

func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Temporary()
	}
	var te *transportError
	return errors.As(err, &te)
}

func (c *Client) backoff(attempt int) time.Duration {
	d := c.maxBackoff
	if attempt < 30 {
		if b := c.initialBackoff << uint(attempt); b > 0 && b < d {
			d = b
		}
	}
	return time.Duration(float64(d) * c.jitter())
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// response is the body of a function call, documented at
// https://docs.convex.dev/http-api/.
type response struct {
	Status       string          `json:"status"`
	Value        json.RawMessage `json:"value"`
	ErrorMessage string          `json:"errorMessage"`
	ErrorData    json.RawMessage `json:"errorData"`
	LogLines     []string        `json:"logLines"`
}

func (c *Client) once(ctx context.Context, endpoint, path string, body []byte) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("convex: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Convex-Client", "go-"+Version)
	if a := c.authHeader(); a != "" {
		req.Header.Set("Authorization", a)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &transportError{err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &transportError{err}
	}

	c.warnDeprecation(resp.Header)

	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !ok && resp.StatusCode != statusFunctionFailed {
		return nil, httpError(resp.StatusCode, data)
	}

	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, &ResponseError{StatusCode: resp.StatusCode, Msg: "body is not JSON: " + truncate(data)}
	}
	if c.logHandler != nil {
		for _, line := range r.LogLines {
			c.logHandler(path, line)
		}
	}
	switch r.Status {
	case "success":
		if r.Value == nil {
			return nil, &ResponseError{StatusCode: resp.StatusCode, Msg: "success response has no value"}
		}
		v, err := DecodeJSON(r.Value)
		if err != nil {
			return nil, &ResponseError{StatusCode: resp.StatusCode, Msg: "invalid value: " + err.Error()}
		}
		return v, nil
	case "error":
		if len(r.ErrorData) > 0 {
			data, err := DecodeJSON(r.ErrorData)
			if err != nil {
				return nil, &ResponseError{StatusCode: resp.StatusCode, Msg: "invalid errorData: " + err.Error()}
			}
			return nil, &ConvexError{Message: r.ErrorMessage, Data: data, LogLines: r.LogLines}
		}
		return nil, &ExecutionError{Message: r.ErrorMessage, LogLines: r.LogLines}
	}
	return nil, &ResponseError{StatusCode: resp.StatusCode, Msg: fmt.Sprintf("unknown status %q", r.Status)}
}

func httpError(status int, body []byte) error {
	e := &HTTPError{StatusCode: status}
	var m struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &m) == nil && (m.Code != "" || m.Message != "") {
		e.Code, e.Message = m.Code, m.Message
	} else {
		e.Message = strings.TrimSpace(truncate(body))
	}
	return e
}

func truncate(b []byte) string {
	if len(b) > maxErrorBody {
		return string(b[:maxErrorBody]) + "..."
	}
	return string(b)
}

func (c *Client) warnDeprecation(h http.Header) {
	state := h.Get("x-convex-deprecation-state")
	msg := h.Get("x-convex-deprecation-message")
	if state == "" || msg == "" {
		return
	}
	if c.warnHandler != nil {
		c.warnHandler(state, msg)
		return
	}
	if _, seen := c.warned.LoadOrStore(state+"\x00"+msg, true); !seen {
		log.Printf("convex: %s: %s", state, msg)
	}
}

// QueryAs runs a query and decodes its result into T with DecodeInto.
func QueryAs[T any](ctx context.Context, c *Client, path string, args any) (T, error) {
	return as[T](c.Query(ctx, path, args))
}

// MutationAs runs a mutation and decodes its result into T with DecodeInto.
func MutationAs[T any](ctx context.Context, c *Client, path string, args any) (T, error) {
	return as[T](c.Mutation(ctx, path, args))
}

// ActionAs runs an action and decodes its result into T with DecodeInto.
func ActionAs[T any](ctx context.Context, c *Client, path string, args any) (T, error) {
	return as[T](c.Action(ctx, path, args))
}

// FunctionAs runs a function through /api/run and decodes its result into T.
func FunctionAs[T any](ctx context.Context, c *Client, path string, args any) (T, error) {
	return as[T](c.Function(ctx, path, args))
}

func as[T any](v any, err error) (T, error) {
	var out T
	if err != nil {
		return out, err
	}
	if err := DecodeInto(v, &out); err != nil {
		return out, err
	}
	return out, nil
}
