package convex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func TestNewClientURLs(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr string
	}{
		{in: "https://happy-otter-123.convex.cloud", want: "https://happy-otter-123.convex.cloud"},
		{in: "https://happy-otter-123.convex.cloud/", want: "https://happy-otter-123.convex.cloud"},
		{in: "http://127.0.0.1:3210", want: "http://127.0.0.1:3210"},
		{in: "https://convex.example.com/base", want: "https://convex.example.com/base"},
		{in: "happy-otter-123.convex.cloud", wantErr: "must start with https://"},
		{in: "ftp://x.convex.cloud", wantErr: "must start with https://"},
		{in: "https://", wantErr: "no host"},
		{in: "https://x.convex.cloud?x=1", wantErr: "query or fragment"},
		{in: "https://happy-otter-123.convex.site", wantErr: ".convex.site is used for HTTP actions"},
		{in: "https://bad host", wantErr: "invalid deployment URL"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			c, err := NewClient(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("NewClient(%q) error = %v, want %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.URL() != tc.want {
				t.Fatalf("URL() = %q, want %q", c.URL(), tc.want)
			}
		})
	}
}

func TestEndpointsAndBody(t *testing.T) {
	cases := []struct {
		name     string
		call     func(c *Client) (any, error)
		endpoint string
	}{
		{"query", func(c *Client) (any, error) { return c.Query(ctx, "messages:list", map[string]any{"limit": 10}) }, "/api/query"},
		{"mutation", func(c *Client) (any, error) { return c.Mutation(ctx, "messages:list", map[string]any{"limit": 10}) }, "/api/mutation"},
		{"action", func(c *Client) (any, error) { return c.Action(ctx, "messages:list", map[string]any{"limit": 10}) }, "/api/action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, ok(fixtureSuccessNull))
			c, _ := f.client()
			if _, err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			r := f.last()
			if r.Method != http.MethodPost || r.Path != tc.endpoint {
				t.Fatalf("got %s %s, want POST %s", r.Method, r.Path, tc.endpoint)
			}
			want := `{"path":"messages:list","args":{"limit":10},"format":"convex_encoded_json"}`
			if r.Raw != want {
				t.Fatalf("body %s, want %s", r.Raw, want)
			}
		})
	}
}

func TestHeaders(t *testing.T) {
	f := newFake(t, ok(fixtureSuccessNull))
	c, _ := f.client()
	if _, err := c.Query(ctx, "a:b", nil); err != nil {
		t.Fatal(err)
	}
	h := f.last().Header
	for k, want := range map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json",
		"Convex-Client": "go-" + Version,
		"Authorization": "",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
}

func TestAuthHeaders(t *testing.T) {
	identity := map[string]any{"subject": "user123", "issuer": "https://auth.example.com"}
	rawIdentity, _ := json.Marshal(identity)
	cases := []struct {
		name  string
		opts  []Option
		setup func(c *Client)
		want  string
	}{
		{"none", nil, nil, ""},
		{"WithAuth", []Option{WithAuth("jwt.token.sig")}, nil, "Bearer jwt.token.sig"},
		{"SetAuth", nil, func(c *Client) { c.SetAuth("abc") }, "Bearer abc"},
		{"WithDeployKey", []Option{WithDeployKey("prod:happy-otter-123|eyJ2")}, nil, "Convex prod:happy-otter-123|eyJ2"},
		{"SetAdminAuth", nil, func(c *Client) { c.SetAdminAuth("dev:key") }, "Convex dev:key"},
		{"acting as", nil, func(c *Client) {
			if err := c.SetAdminAuthActingAs("dev:key", identity); err != nil {
				t.Fatal(err)
			}
		}, "Convex dev:key:" + base64.StdEncoding.EncodeToString(rawIdentity)},
		{"admin replaces user", []Option{WithAuth("u")}, func(c *Client) { c.SetAdminAuth("k") }, "Convex k"},
		{"user replaces admin", []Option{WithDeployKey("k")}, func(c *Client) { c.SetAuth("u") }, "Bearer u"},
		{"ClearAuth", []Option{WithAuth("u")}, func(c *Client) { c.ClearAuth() }, ""},
		{"empty token clears", []Option{WithAuth("u")}, func(c *Client) { c.SetAuth("") }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, ok(fixtureSuccessNull))
			c, _ := f.client(tc.opts...)
			if tc.setup != nil {
				tc.setup(c)
			}
			if _, err := c.Query(ctx, "a:b", nil); err != nil {
				t.Fatal(err)
			}
			if got := f.last().Header.Get("Authorization"); got != tc.want {
				t.Fatalf("Authorization = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestActingAsErrors(t *testing.T) {
	c, _ := NewClient("https://x.convex.cloud")
	if err := c.SetAdminAuthActingAs("", nil); err == nil {
		t.Fatal("want error for an empty deploy key")
	}
	if err := c.SetAdminAuthActingAs("k", map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("want error for an identity that cannot be encoded")
	}
}

func TestArgsEncoding(t *testing.T) {
	type sendArgs struct {
		Author string `json:"author"`
		Body   string `json:"body"`
		Count  int64  `json:"count,omitempty"`
	}
	cases := []struct {
		name string
		args any
		want string
	}{
		{"nil args", nil, `{}`},
		{"empty map", map[string]any{}, `{}`},
		{"nil map", map[string]any(nil), `{}`},
		{"struct", sendArgs{Author: "Ada", Body: "hi"}, `{"author":"Ada","body":"hi"}`},
		{"struct pointer", &sendArgs{Author: "Ada", Count: 2}, `{"author":"Ada","body":"","count":{"$integer":"AgAAAAAAAAA="}}`},
		{"typed values", map[string]any{"id": int64(7), "data": []byte("hi"), "ratio": math.Inf(1)}, `{"data":{"$bytes":"aGk="},"id":{"$integer":"BwAAAAAAAAA="},"ratio":{"$float":"AAAAAAAA8H8="}}`},
		{"typed map", map[string]string{"q": "x"}, `{"q":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, ok(fixtureSuccessNull))
			c, _ := f.client()
			if _, err := c.Mutation(ctx, "messages:send", tc.args); err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(f.last().Body["args"])
			// Re-marshal the recorded args so key order is canonical.
			var want any
			_ = json.Unmarshal([]byte(tc.want), &want)
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(got, wantJSON) {
				t.Fatalf("args %s, want %s", got, wantJSON)
			}
		})
	}
}

func TestArgsErrors(t *testing.T) {
	cases := []struct {
		name string
		args any
		want string
	}{
		{"string args", "hello", "must be an object"},
		{"slice args", []any{1.0}, "must be an object"},
		{"number args", 3.0, "must be an object"},
		{"bad value", map[string]any{"n": 1 << 60}, "args.n"},
		{"dollar key", map[string]any{"$bad": 1}, "reserved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, ok(fixtureSuccessNull))
			c, _ := f.client()
			for name, call := range map[string]func() (any, error){
				"query":    func() (any, error) { return c.Query(ctx, "a:b", tc.args) },
				"function": func() (any, error) { return c.Function(ctx, "a:b", tc.args) },
			} {
				_, err := call()
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("%s error = %v, want %q", name, err, tc.want)
				}
			}
			if n := len(f.requests()); n != 0 {
				t.Fatalf("%d requests sent for invalid args, want 0", n)
			}
		})
	}
}

func TestEmptyPath(t *testing.T) {
	c, _ := NewClient("https://x.convex.cloud")
	for _, call := range []func() (any, error){
		func() (any, error) { return c.Query(ctx, "", nil) },
		func() (any, error) { return c.Mutation(ctx, "", nil) },
		func() (any, error) { return c.Action(ctx, "", nil) },
		func() (any, error) { return c.Function(ctx, "", nil) },
	} {
		if _, err := call(); err == nil || !strings.Contains(err.Error(), "empty function path") {
			t.Fatalf("error = %v, want empty function path", err)
		}
	}
}

func TestRunPath(t *testing.T) {
	cases := []struct {
		in, want, wantErr string
	}{
		{in: "messages:list", want: "messages/list"},
		{in: "folder/messages:list", want: "folder/messages/list"},
		{in: "a/b/c:d", want: "a/b/c/d"},
		{in: "messages", want: "messages/default"},
		{in: "messages.js:list", want: "messages.js/list"},
		{in: "my file:list", want: "my%20file/list"},
		{in: "", wantErr: "empty"},
		{in: ":list", wantErr: "invalid"},
		{in: "messages:", wantErr: "invalid"},
		{in: "a//b:c", wantErr: "invalid"},
		{in: "/a:b", wantErr: "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := RunPath(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("RunPath(%q) error = %v, want %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("RunPath(%q) = %q, %v, want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestFunctionEndpoint(t *testing.T) {
	f := newFake(t, ok(fixtureSuccessList))
	c, _ := f.client()
	v, err := c.Function(ctx, "folder/messages:list", map[string]any{"channel": "general"})
	if err != nil {
		t.Fatal(err)
	}
	r := f.last()
	if r.Path != "/api/run/folder/messages/list" {
		t.Fatalf("path %s", r.Path)
	}
	if r.Raw != `{"args":{"channel":"general"},"format":"convex_encoded_json"}` {
		t.Fatalf("body %s", r.Raw)
	}
	if _, isPath := r.Body["path"]; isPath {
		t.Fatal("run body must not carry a path field")
	}
	if list, ok := v.([]any); !ok || len(list) != 1 {
		t.Fatalf("got %#v", v)
	}
}

func TestFunctionDeploymentBasePath(t *testing.T) {
	f := newFake(t, ok(fixtureSuccessNull))
	c, err := NewClient(f.srv.URL + "/proxy/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Function(ctx, "m:f", nil); err != nil {
		t.Fatal(err)
	}
	if p := f.last().Path; p != "/proxy/api/run/m/f" {
		t.Fatalf("path %s, want /proxy/api/run/m/f", p)
	}
}

func TestSuccessValues(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		want    any
	}{
		{"null", fixtureSuccessNull, nil},
		{"list of documents", fixtureSuccessList, []any{map[string]any{
			"_id": "j57d3zfk0rd6sxnc0ddmny9cq97m6hr3", "_creationTime": 1731470410237.1567,
			"author": "Ada", "body": "Hello",
		}}},
		{"typed values", fixtureSuccessTyped, map[string]any{
			"count": int64(42), "avg": 1.5, "blob": []byte{0, 255, 16},
			"inf": math.Inf(1), "ok": true, "none": nil,
		}},
		{"string", fixtureSuccessLogs, "done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, ok(tc.fixture))
			c, _ := f.client()
			got, err := c.Query(ctx, "a:b", nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestFunctionErrors(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		fixture   string
		wantExec  string // substring of ExecutionError message
		wantData  any    // ConvexError data, when a ConvexError is expected
		wantMsg   string // substring of ConvexError message
		wantLines []string
	}{
		{name: "uncaught error", status: 200, fixture: fixtureExecError, wantExec: "Uncaught Error: boom", wantLines: []string{"[ERROR] 'boom'"}},
		{name: "missing function", status: 200, fixture: fixtureNotFound, wantExec: "Could not find public function"},
		{name: "uncaught error on 560", status: 560, fixture: fixtureExecError, wantExec: "boom", wantLines: []string{"[ERROR] 'boom'"}},
		{name: "ConvexError with string data", status: 200, fixture: fixtureConvexErrorString, wantData: "Message too long", wantMsg: "Message too long"},
		{name: "ConvexError with object data", status: 200, fixture: fixtureConvexErrorObject, wantData: map[string]any{"code": "RATE_LIMITED", "retryAfter": int64(1000)}, wantMsg: "RATE_LIMITED"},
		{name: "ConvexError on 560", status: 560, fixture: fixtureConvexErrorObject, wantData: map[string]any{"code": "RATE_LIMITED", "retryAfter": int64(1000)}},
		{name: "ConvexError with null data", status: 200, fixture: fixtureConvexErrorNull, wantData: nil, wantMsg: "ConvexError"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, reply{status: tc.status, body: tc.fixture})
			c, _ := f.client()
			_, err := c.Mutation(ctx, "messages:send", nil)
			if tc.wantExec != "" {
				var ee *ExecutionError
				if !errors.As(err, &ee) {
					t.Fatalf("error %v (%T), want *ExecutionError", err, err)
				}
				if !strings.Contains(ee.Message, tc.wantExec) || ee.Error() != ee.Message {
					t.Fatalf("message %q, want %q", ee.Message, tc.wantExec)
				}
				if tc.wantLines != nil && !reflect.DeepEqual(ee.LogLines, tc.wantLines) {
					t.Fatalf("log lines %q, want %q", ee.LogLines, tc.wantLines)
				}
				var ce *ConvexError
				if errors.As(err, &ce) {
					t.Fatal("an execution error must not match *ConvexError")
				}
				return
			}
			var ce *ConvexError
			if !errors.As(err, &ce) {
				t.Fatalf("error %v (%T), want *ConvexError", err, err)
			}
			if !reflect.DeepEqual(ce.Data, tc.wantData) {
				t.Fatalf("data %#v, want %#v", ce.Data, tc.wantData)
			}
			if tc.wantMsg != "" && !strings.Contains(ce.Error(), tc.wantMsg) {
				t.Fatalf("Error() %q, want it to contain %q", ce.Error(), tc.wantMsg)
			}
		})
	}
}

func TestCapturedBackendResponses(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		f := newFake(t, ok(fixtureCapturedNotFound))
		c, _ := f.client()
		_, err := c.Query(ctx, "messages:missing", nil)
		var ee *ExecutionError
		if !errors.As(err, &ee) || !strings.Contains(ee.Message, "Could not find public function for 'messages:missing'") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("ConvexError with Int64 data", func(t *testing.T) {
		f := newFake(t, ok(fixtureCapturedConvexError))
		c, _ := f.client()
		_, err := c.Mutation(ctx, "messages:send", nil)
		var ce *ConvexError
		if !errors.As(err, &ce) || !reflect.DeepEqual(ce.Data, map[string]any{"code": "TOO_LONG", "max": int64(100)}) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("bad request", func(t *testing.T) {
		f := newFake(t, reply{status: 400, body: fixtureCapturedBadRequest})
		c, _ := f.client()
		_, err := c.Query(ctx, "a:b", nil)
		var he *HTTPError
		if !errors.As(err, &he) || he.Code != "BadJsonBody" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid auth header", func(t *testing.T) {
		f := newFake(t, reply{status: 401, body: fixtureCapturedBadAuth})
		c, _ := f.client(WithAuth("not-a-jwt"))
		_, err := c.Query(ctx, "a:b", nil)
		var he *HTTPError
		if !errors.As(err, &he) || he.StatusCode != 401 || he.Code != "InvalidAuthHeader" {
			t.Fatalf("got %v", err)
		}
		if len(f.requests()) != 1 {
			t.Fatal("401 was retried")
		}
	})
	t.Run("special values", func(t *testing.T) {
		f := newFake(t, ok(fixtureCapturedSpecial))
		c, _ := f.client()
		v, err := c.Query(ctx, "values:special", nil)
		if err != nil {
			t.Fatal(err)
		}
		m := v.(map[string]any)
		if m["int"] != int64(9007199254740993) || !math.IsNaN(m["nan"].(float64)) || !math.Signbit(m["negZero"].(float64)) {
			t.Fatalf("got %#v", m)
		}
	})
}

func TestConvexErrorDecodeInto(t *testing.T) {
	f := newFake(t, ok(fixtureConvexErrorObject))
	c, _ := f.client()
	_, err := c.Action(ctx, "ai:generate", nil)
	var ce *ConvexError
	if !errors.As(err, &ce) {
		t.Fatalf("got %v", err)
	}
	var data struct {
		Code       string        `json:"code"`
		RetryAfter time.Duration `json:"retryAfter"`
	}
	if err := DecodeInto(ce.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Code != "RATE_LIMITED" || data.RetryAfter != 1000 {
		t.Fatalf("got %+v", data)
	}
}

func TestEmptyConvexErrorMessage(t *testing.T) {
	if got := (&ConvexError{}).Error(); got != "convex: ConvexError" {
		t.Fatalf("got %q", got)
	}
}

func TestHTTPErrors(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode string
		wantMsg  string
		wantText string
	}{
		{"bad request", 400, fixtureBadRequest, "BadJsonBody", "missing field `path`", "convex: HTTP 400 BadJsonBody: Failed to deserialize"},
		{"invalid path", 400, fixtureInvalidFunctionPath, "BadConvexModuleIdentifier", "Invalid function path", "HTTP 400 BadConvexModuleIdentifier"},
		{"unauthenticated", 401, fixtureUnauthorized, "Unauthenticated", "Could not verify OIDC token", "HTTP 401 Unauthenticated"},
		{"not found plain text", 404, "404 page not found\n", "", "404 page not found", "convex: HTTP 404: 404 page not found"},
		{"empty body", 403, "", "", "", "convex: HTTP 403"},
		{"json without code", 409, `{"other":1}`, "", `{"other":1}`, "HTTP 409"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, reply{status: tc.status, body: tc.body})
			c, sleeps := f.client()
			_, err := c.Query(ctx, "a:b", nil)
			var he *HTTPError
			if !errors.As(err, &he) {
				t.Fatalf("error %v (%T), want *HTTPError", err, err)
			}
			if he.StatusCode != tc.status || he.Code != tc.wantCode || !strings.Contains(he.Message, tc.wantMsg) {
				t.Fatalf("got %+v", he)
			}
			if !strings.Contains(he.Error(), tc.wantText) {
				t.Fatalf("Error() %q, want %q", he.Error(), tc.wantText)
			}
			if he.Temporary() {
				t.Fatal("4xx must not be temporary")
			}
			if n := len(f.requests()); n != 1 || len(*sleeps) != 0 {
				t.Fatalf("4xx was retried: %d requests", n)
			}
		})
	}
}

func TestLongErrorBodyIsTruncated(t *testing.T) {
	f := newFake(t, reply{status: 418, body: strings.Repeat("x", 10000)})
	c, _ := f.client()
	_, err := c.Query(ctx, "a:b", nil)
	var he *HTTPError
	if !errors.As(err, &he) || len(he.Message) > maxErrorBody+3 || !strings.HasSuffix(he.Message, "...") {
		t.Fatalf("got %v", err)
	}
}

func TestBadResponses(t *testing.T) {
	cases := []struct {
		name string
		rp   reply
		want string
	}{
		{"html on 200", reply{status: 200, body: "<html>hi</html>"}, "body is not JSON"},
		{"unknown status", ok(`{"status":"pending"}`), `unknown status "pending"`},
		{"no status", ok(`{"value":1}`), `unknown status ""`},
		{"success without value", ok(`{"status":"success"}`), "has no value"},
		{"bad int64 in value", ok(`{"status":"success","value":{"$integer":"AA=="}}`), "invalid value"},
		{"bad errorData", ok(`{"status":"error","errorMessage":"x","errorData":{"$set":[]}}`), "invalid errorData"},
		{"garbage on 560", reply{status: 560, body: "oops"}, "body is not JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, tc.rp)
			c, _ := f.client()
			_, err := c.Query(ctx, "a:b", nil)
			var re *ResponseError
			if !errors.As(err, &re) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v (%T), want *ResponseError containing %q", err, err, tc.want)
			}
			if n := len(f.requests()); n != 1 {
				t.Fatalf("bad response was retried: %d requests", n)
			}
		})
	}
}

func TestQueryRetriesOn5xx(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newFake(t, reply{status: status, body: fixtureUnavailable}, reply{status: status, body: "bad gateway"}, ok(fixtureSuccessLogs))
			c, sleeps := f.client()
			v, err := c.Query(ctx, "a:b", nil)
			if err != nil || v != "done" {
				t.Fatalf("got %v, %v", v, err)
			}
			if n := len(f.requests()); n != 3 {
				t.Fatalf("%d requests, want 3", n)
			}
			if !reflect.DeepEqual(*sleeps, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}) {
				t.Fatalf("sleeps %v", *sleeps)
			}
			reqs := f.requests()
			if reqs[0].Raw != reqs[2].Raw {
				t.Fatal("retried body differs from the first one")
			}
		})
	}
}

func TestQueryRetriesExhausted(t *testing.T) {
	f := newFake(t, reply{status: 503, body: fixtureUnavailable})
	c, sleeps := f.client()
	_, err := c.Query(ctx, "a:b", nil)
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != 503 || he.Code != "ServiceUnavailable" || !he.Temporary() {
		t.Fatalf("got %v", err)
	}
	if n := len(f.requests()); n != 1+defaultQueryRetries {
		t.Fatalf("%d requests, want %d", n, 1+defaultQueryRetries)
	}
	if len(*sleeps) != defaultQueryRetries {
		t.Fatalf("%d sleeps", len(*sleeps))
	}
}

func TestQueryRetriesOnNetworkError(t *testing.T) {
	f := newFake(t, reply{hangup: true}, ok(fixtureSuccessLogs))
	c, sleeps := f.client()
	v, err := c.Query(ctx, "a:b", nil)
	if err != nil || v != "done" {
		t.Fatalf("got %v, %v", v, err)
	}
	if len(f.requests()) != 2 || len(*sleeps) != 1 {
		t.Fatalf("%d requests, %d sleeps", len(f.requests()), len(*sleeps))
	}
}

func TestNetworkErrorUnwraps(t *testing.T) {
	f := newFake(t, reply{hangup: true})
	c, _ := f.client(WithQueryRetries(1))
	_, err := c.Query(ctx, "a:b", nil)
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Fatalf("error %v (%T) does not unwrap to *url.Error", err, err)
	}
	if !strings.HasPrefix(err.Error(), "convex: request failed") {
		t.Fatalf("error text %q", err)
	}
	if len(f.requests()) != 2 {
		t.Fatalf("%d requests, want 2", len(f.requests()))
	}
}

func TestNoRetryForWrites(t *testing.T) {
	for name, call := range map[string]func(c *Client) (any, error){
		"mutation": func(c *Client) (any, error) { return c.Mutation(ctx, "a:b", nil) },
		"action":   func(c *Client) (any, error) { return c.Action(ctx, "a:b", nil) },
		"function": func(c *Client) (any, error) { return c.Function(ctx, "a:b", nil) },
	} {
		for _, rp := range []reply{{status: 503, body: fixtureUnavailable}, {hangup: true}} {
			t.Run(name, func(t *testing.T) {
				f := newFake(t, rp, ok(fixtureSuccessNull))
				c, sleeps := f.client()
				if _, err := call(c); err == nil {
					t.Fatal("want the first failure to be returned")
				}
				if len(f.requests()) != 1 || len(*sleeps) != 0 {
					t.Fatalf("%s was retried", name)
				}
			})
		}
	}
}

func TestNoRetryForFunctionFailure(t *testing.T) {
	f := newFake(t, reply{status: 560, body: fixtureExecError}, ok(fixtureSuccessNull))
	c, _ := f.client()
	_, err := c.Query(ctx, "a:b", nil)
	var ee *ExecutionError
	if !errors.As(err, &ee) || len(f.requests()) != 1 {
		t.Fatalf("got %v after %d requests", err, len(f.requests()))
	}
}

func TestRetriesDisabled(t *testing.T) {
	f := newFake(t, reply{status: 500, body: fixtureUnavailable}, ok(fixtureSuccessNull))
	c, _ := f.client(WithQueryRetries(0))
	if _, err := c.Query(ctx, "a:b", nil); err == nil {
		t.Fatal("want an error")
	}
	if len(f.requests()) != 1 {
		t.Fatalf("%d requests", len(f.requests()))
	}
}

func TestBackoffSchedule(t *testing.T) {
	c, _ := NewClient("https://x.convex.cloud", WithBackoff(50*time.Millisecond, 300*time.Millisecond))
	c.jitter = func() float64 { return 1 }
	want := []time.Duration{50, 100, 200, 300, 300, 300}
	for i, w := range want {
		if got := c.backoff(i); got != w*time.Millisecond {
			t.Fatalf("backoff(%d) = %v, want %v", i, got, w*time.Millisecond)
		}
	}
	if got := c.backoff(100); got != 300*time.Millisecond {
		t.Fatalf("backoff(100) = %v", got)
	}
	c.jitter = func() float64 { return 0.5 }
	if got := c.backoff(1); got != 50*time.Millisecond {
		t.Fatalf("half jitter backoff(1) = %v", got)
	}
	c.jitter = func() float64 { return 0 }
	if got := c.backoff(3); got != 0 {
		t.Fatalf("zero jitter backoff = %v", got)
	}
}

func TestOptionsIgnoreBadValues(t *testing.T) {
	c, _ := NewClient("https://x.convex.cloud", WithQueryRetries(-1), WithBackoff(0, -1), WithHTTPClient(nil))
	if c.queryRetries != defaultQueryRetries || c.initialBackoff != defaultInitialBackoff || c.maxBackoff != defaultMaxBackoff || c.httpClient != http.DefaultClient {
		t.Fatalf("defaults changed: %+v", c)
	}
}

func TestRealSleepBackoff(t *testing.T) {
	f := newFake(t, reply{status: 500, body: fixtureUnavailable}, ok(fixtureSuccessLogs))
	c, err := NewClient(f.srv.URL, WithBackoff(time.Millisecond, 2*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c.Query(ctx, "a:b", nil); err != nil || v != "done" {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestContextCanceledDuringBackoff(t *testing.T) {
	f := newFake(t, reply{status: 503, body: fixtureUnavailable})
	c, err := NewClient(f.srv.URL, WithBackoff(time.Hour, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	c.jitter = func() float64 { return 1 }
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = c.Query(cctx, "a:b", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("backoff ignored the context")
	}
	if len(f.requests()) != 1 {
		t.Fatalf("%d requests", len(f.requests()))
	}
}

func TestContextDeadlineDuringRequest(t *testing.T) {
	f := newFake(t, reply{status: 200, body: fixtureSuccessNull, delay: 2 * time.Second})
	c, _ := f.client()
	cctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	_, err := c.Query(cctx, "a:b", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v, want context.DeadlineExceeded", err)
	}
	if len(f.requests()) != 1 {
		t.Fatalf("a canceled request was retried: %d requests", len(f.requests()))
	}
}

func TestContextAlreadyCanceled(t *testing.T) {
	f := newFake(t, ok(fixtureSuccessNull))
	c, _ := f.client()
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.Query(cctx, "a:b", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
}

func TestNilContext(t *testing.T) {
	f := newFake(t, ok(fixtureSuccessLogs))
	c, _ := f.client()
	//lint:ignore SA1012 checking that a nil context is tolerated
	var nilCtx context.Context
	if v, err := c.Query(nilCtx, "a:b", nil); err != nil || v != "done" {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestLogHandler(t *testing.T) {
	type line struct{ path, text string }
	var got []line
	f := newFake(t, ok(fixtureSuccessLogs), ok(fixtureExecError))
	c, _ := f.client(WithLogHandler(func(path, text string) { got = append(got, line{path, text}) }))
	if _, err := c.Query(ctx, "jobs:run", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Mutation(ctx, "jobs:fail", nil); err == nil {
		t.Fatal("want an error")
	}
	want := []line{{"jobs:run", "[LOG] 'starting'"}, {"jobs:run", "[INFO] 'finished'"}, {"jobs:fail", "[ERROR] 'boom'"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDeprecationWarnings(t *testing.T) {
	hdr := map[string]string{"x-convex-deprecation-state": "UpgradeRequired", "x-convex-deprecation-message": "Upgrade the client"}
	f := newFake(t, reply{status: 200, body: fixtureSuccessNull, header: hdr})
	var got []string
	c, _ := f.client(WithWarningHandler(func(state, msg string) { got = append(got, state+": "+msg) }))
	for i := 0; i < 2; i++ {
		if _, err := c.Query(ctx, "a:b", nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != "UpgradeRequired: Upgrade the client" {
		t.Fatalf("got %q", got)
	}
}

func TestDeprecationDefaultLogsOnce(t *testing.T) {
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() { log.SetOutput(prev); log.SetFlags(flags) }()

	hdr := map[string]string{"x-convex-deprecation-state": "Deprecated", "x-convex-deprecation-message": "go-0.1.0 is old"}
	f := newFake(t, reply{status: 200, body: fixtureSuccessNull, header: hdr})
	c, _ := f.client()
	for i := 0; i < 3; i++ {
		if _, err := c.Query(ctx, "a:b", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := buf.String(); got != "convex: Deprecated: go-0.1.0 is old\n" {
		t.Fatalf("log output %q", got)
	}
}

func TestDeprecationNeedsBothHeaders(t *testing.T) {
	f := newFake(t, reply{status: 200, body: fixtureSuccessNull, header: map[string]string{"x-convex-deprecation-state": "Deprecated"}})
	called := false
	c, _ := f.client(WithWarningHandler(func(string, string) { called = true }))
	if _, err := c.Query(ctx, "a:b", nil); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("handler called without a message header")
	}
}

type countingTransport struct {
	mu sync.Mutex
	n  int
	rt http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.rt.RoundTrip(r)
}

func TestWithHTTPClient(t *testing.T) {
	f := newFake(t, ok(fixtureSuccessNull))
	tr := &countingTransport{rt: http.DefaultTransport}
	c, _ := f.client(WithHTTPClient(&http.Client{Transport: tr}))
	if _, err := c.Query(ctx, "a:b", nil); err != nil {
		t.Fatal(err)
	}
	if tr.n != 1 {
		t.Fatalf("custom transport used %d times", tr.n)
	}
}

func TestGenericHelpers(t *testing.T) {
	type doc struct {
		ID     string  `json:"_id"`
		Author string  `json:"author"`
		Time   float64 `json:"_creationTime"`
	}
	f := newFake(t, ok(fixtureSuccessList))
	c, _ := f.client()

	docs, err := QueryAs[[]doc](ctx, c, "messages:list", nil)
	if err != nil || len(docs) != 1 || docs[0].Author != "Ada" || docs[0].ID == "" {
		t.Fatalf("QueryAs = %+v, %v", docs, err)
	}
	if _, err := MutationAs[[]doc](ctx, c, "messages:list", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ActionAs[[]doc](ctx, c, "messages:list", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := FunctionAs[[]doc](ctx, c, "messages:list", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := QueryAs[[]int](ctx, c, "messages:list", nil); err == nil {
		t.Fatal("want a decode error for the wrong type")
	}

	g := newFake(t, ok(fixtureExecError))
	gc, _ := g.client()
	if _, err := QueryAs[string](ctx, gc, "a:b", nil); err == nil {
		t.Fatal("want the call error passed through")
	}
}

func TestConcurrentCalls(t *testing.T) {
	f := newFake(t, ok(fixtureSuccessLogs))
	c, _ := f.client()
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%5 == 0 {
				c.SetAuth("token")
			}
			if _, err := c.Query(ctx, "a:b", map[string]any{"i": i}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if n := len(f.requests()); n != 50 {
		t.Fatalf("%d requests, want 50", n)
	}
}
