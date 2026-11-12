package convex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Response bodies shaped like the ones documented at
// https://docs.convex.dev/http-api/ and produced by the open-source backend
// (crates/local_backend/src/public_api.rs, UdfResponse). Values use the
// convex_encoded_json format the client asks for. The not-found, ConvexError
// with an Int64, bad-request and invalid-auth bodies were captured from the
// local backend release precompiled-2026-11-12-cc07c9e, which answers
// function errors with HTTP 200.
const (
	fixtureCapturedNotFound    = `{"status":"error","errorMessage":"[Request ID: 22a6eaf9a578d5d5] Server Error\nCould not find public function for 'messages:missing'.\n"}`
	fixtureCapturedConvexError = `{"status":"error","errorMessage":"[Request ID: 3f40d550f4c6f280] Server Error\nUncaught ConvexError: {\"code\":\"TOO_LONG\",\"max\":\"100n\"}\n    at handler (../convex/messages.ts:14:11)\n","errorData":{"code":"TOO_LONG","max":{"$integer":"ZAAAAAAAAAA="}}}`
	fixtureCapturedBadRequest  = `{"code":"BadJsonBody","message":"Failed to deserialize the JSON body into the target type: missing field ` + "`path`" + ` at line 1 column 11"}`
	fixtureCapturedBadAuth     = `{"code":"InvalidAuthHeader","message":"Could not parse JWT payload. Check that the token is a valid JWT format with three base64-encoded parts separated by dots."}`
	fixtureCapturedSpecial     = `{"status":"success","value":{"bytes":{"$bytes":"AP8Q"},"float":1.5,"inf":{"$float":"AAAAAAAA8H8="},"int":{"$integer":"AQAAAAAAIAA="},"nan":{"$float":"AAAAAAAA+H8="},"negInf":{"$float":"AAAAAAAA8P8="},"negZero":{"$float":"AAAAAAAAAIA="}}}`
)

const (
	fixtureSuccessList         = `{"status":"success","value":[{"_id":"j57d3zfk0rd6sxnc0ddmny9cq97m6hr3","_creationTime":1731470410237.1567,"author":"Ada","body":"Hello"}],"logLines":[]}`
	fixtureSuccessNull         = `{"status":"success","value":null}`
	fixtureSuccessTyped        = `{"status":"success","value":{"count":{"$integer":"KgAAAAAAAAA="},"avg":1.5,"blob":{"$bytes":"AP8Q"},"inf":{"$float":"AAAAAAAA8H8="},"ok":true,"none":null}}`
	fixtureSuccessLogs         = `{"status":"success","value":"done","logLines":["[LOG] 'starting'","[INFO] 'finished'"]}`
	fixtureExecError           = `{"status":"error","errorMessage":"[Request ID: 5a1c0e7f13b2f4de] Server Error\nUncaught Error: boom\n    at handler (../convex/messages.ts:12:9)","logLines":["[ERROR] 'boom'"]}`
	fixtureNotFound            = `{"status":"error","errorMessage":"[Request ID: 9f0e4d3c2b1a0918] Server Error\nCould not find public function for 'messages:missing'. Did you forget to run ` + "`npx convex dev`" + ` or ` + "`npx convex deploy`" + `?"}`
	fixtureConvexErrorString   = `{"status":"error","errorMessage":"[CONVEX M(messages:send)] Uncaught ConvexError: Message too long","errorData":"Message too long","logLines":[]}`
	fixtureConvexErrorObject   = `{"status":"error","errorMessage":"Uncaught ConvexError: {\"code\":\"RATE_LIMITED\",\"retryAfter\":{\"$integer\":\"6AMAAAAAAAA=\"}}","errorData":{"code":"RATE_LIMITED","retryAfter":{"$integer":"6AMAAAAAAAA="}}}`
	fixtureConvexErrorNull     = `{"status":"error","errorMessage":"Uncaught ConvexError: null","errorData":null}`
	fixtureBadRequest          = `{"code":"BadJsonBody","message":"Failed to deserialize the JSON body into the target type: missing field ` + "`path`" + ` at line 1 column 2"}`
	fixtureUnauthorized        = `{"code":"Unauthenticated","message":"Could not verify OIDC token claim. Check that the token signature is valid and the token hasn't expired."}`
	fixtureInvalidFunctionPath = `{"code":"BadConvexModuleIdentifier","message":"Invalid function path"}`
	fixtureUnavailable         = `{"code":"ServiceUnavailable","message":"Service temporarily unavailable"}`
)

type recorded struct {
	Method string
	Path   string
	Header http.Header
	Body   map[string]any
	Raw    string
}

type reply struct {
	status int
	body   string
	header map[string]string
	hangup bool          // close the connection without a response
	delay  time.Duration // wait before answering
}

// fakeDeployment is an httptest server that plays back replies in order and
// records every request. After the script runs out it repeats the last reply.
type fakeDeployment struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	replies []reply
	reqs    []recorded
}

func newFake(t *testing.T, replies ...reply) *fakeDeployment {
	t.Helper()
	f := &fakeDeployment{t: t, replies: replies}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func ok(body string) reply { return reply{status: 200, body: body} }

func (f *fakeDeployment) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	rec := recorded{Method: r.Method, Path: r.URL.EscapedPath(), Header: r.Header.Clone(), Raw: string(raw)}
	_ = json.Unmarshal(raw, &rec.Body)

	f.mu.Lock()
	idx := len(f.reqs)
	f.reqs = append(f.reqs, rec)
	var rp reply
	if len(f.replies) > 0 {
		if idx < len(f.replies) {
			rp = f.replies[idx]
		} else {
			rp = f.replies[len(f.replies)-1]
		}
	} else {
		rp = ok(fixtureSuccessNull)
	}
	f.mu.Unlock()

	if rp.delay > 0 {
		select {
		case <-time.After(rp.delay):
		case <-r.Context().Done():
			return
		}
	}
	if rp.hangup {
		hj, ok := w.(http.Hijacker)
		if !ok {
			f.t.Error("server does not support hijacking")
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			conn.Close()
		}
		return
	}
	for k, v := range rp.header {
		w.Header().Set(k, v)
	}
	if rp.body != "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(rp.status)
	io.WriteString(w, rp.body)
}

func (f *fakeDeployment) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func (f *fakeDeployment) last() recorded {
	r := f.requests()
	if len(r) == 0 {
		f.t.Fatal("no request was made")
	}
	return r[len(r)-1]
}

// client returns a client for the fake with instant, recorded backoff.
func (f *fakeDeployment) client(opts ...Option) (*Client, *[]time.Duration) {
	f.t.Helper()
	c, err := NewClient(f.srv.URL, opts...)
	if err != nil {
		f.t.Fatal(err)
	}
	var mu sync.Mutex
	sleeps := &[]time.Duration{}
	c.jitter = func() float64 { return 1 }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		*sleeps = append(*sleeps, d)
		mu.Unlock()
		return ctx.Err()
	}
	return c, sleeps
}
