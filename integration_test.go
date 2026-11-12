//go:build integration

package convex

// Integration tests against a real Convex deployment, usually the
// open-source local backend, with the functions in testdata/convex-app
// deployed to it. See "Integration tests" in README.md.
//
//	CONVEX_URL=http://127.0.0.1:3210 CONVEX_ADMIN_KEY=... go test -tags integration ./...

import (
	"context"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func integrationClient(t *testing.T, opts ...Option) *Client {
	t.Helper()
	u := os.Getenv("CONVEX_URL")
	if u == "" {
		t.Skip("CONVEX_URL is not set")
	}
	c, err := NewClient(u, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func adminKey(t *testing.T) string {
	t.Helper()
	k := os.Getenv("CONVEX_ADMIN_KEY")
	if k == "" {
		t.Skip("CONVEX_ADMIN_KEY is not set")
	}
	return k
}

func ictx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestIntegrationEchoRoundTrip(t *testing.T) {
	c := integrationClient(t)
	args := map[string]any{
		"null":    nil,
		"bool":    true,
		"float":   1.25,
		"int64":   int64(math.MaxInt64),
		"neg64":   int64(math.MinInt64),
		"string":  "héllo",
		"bytes":   []byte{0, 1, 254, 255},
		"empty":   []byte{},
		"inf":     math.Inf(1),
		"negInf":  math.Inf(-1),
		"negZero": math.Copysign(0, -1),
		"array":   []any{1.0, "two", int64(3)},
		"object":  map[string]any{"nested": map[string]any{"deep": int64(-7)}},
	}
	got, err := c.Query(ictx(t), "values:echo", args)
	if err != nil {
		t.Fatal(err)
	}
	if !sameValue(got, args) {
		t.Fatalf("echo changed the value\n got %#v\nwant %#v", got, args)
	}
}

func TestIntegrationNaN(t *testing.T) {
	c := integrationClient(t)
	got, err := c.Query(ictx(t), "values:echo", map[string]any{"nan": math.Float64frombits(0x7FF8000000000000)})
	if err != nil {
		t.Fatal(err)
	}
	f, ok := got.(map[string]any)["nan"].(float64)
	if !ok || !math.IsNaN(f) {
		t.Fatalf("got %#v", got)
	}
}

func TestIntegrationJavaScriptTypes(t *testing.T) {
	c := integrationClient(t)
	got, err := c.Query(ictx(t), "values:describe", map[string]any{
		"int":     5,
		"int64":   int64(5),
		"float":   5.5,
		"bytes":   []byte("x"),
		"list":    []string{"a"},
		"null":    nil,
		"negZero": math.Copysign(0, -1),
		"nan":     math.NaN(),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"int": "number", "int64": "bigint", "float": "number", "bytes": "ArrayBuffer",
		"list": "array", "null": "null", "negZero": "-0", "nan": "NaN",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestIntegrationSpecialResults(t *testing.T) {
	c := integrationClient(t)
	got, err := c.Query(ictx(t), "values:special", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["int"] != int64(9007199254740993) || m["float"] != 1.5 || !reflect.DeepEqual(m["bytes"], []byte{0, 255, 16}) {
		t.Fatalf("got %#v", m)
	}
	if !math.IsInf(m["inf"].(float64), 1) || !math.IsInf(m["negInf"].(float64), -1) || !math.IsNaN(m["nan"].(float64)) {
		t.Fatalf("got %#v", m)
	}
	if z := m["negZero"].(float64); z != 0 || !math.Signbit(z) {
		t.Fatalf("negZero = %v", z)
	}
}

func TestIntegrationMutationQueryAction(t *testing.T) {
	c := integrationClient(t)
	author := "go-" + time.Now().Format("150405.000000000")
	id, err := c.Mutation(ictx(t), "messages:send", map[string]any{"author": author, "body": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := id.(string); !ok || s == "" {
		t.Fatalf("insert returned %#v, want a document id", id)
	}
	type message struct {
		ID      string  `json:"_id"`
		Created float64 `json:"_creationTime"`
		Author  string  `json:"author"`
		Body    string  `json:"body"`
	}
	msgs, err := QueryAs[[]message](ictx(t), c, "messages:list", map[string]any{"author": author})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != id || msgs[0].Body != "hello" || msgs[0].Created == 0 {
		t.Fatalf("got %+v", msgs)
	}
	var lines []string
	ac := integrationClient(t, WithLogHandler(func(_, line string) { lines = append(lines, line) }))
	shout, err := ac.Action(ictx(t), "messages:shout", map[string]any{"text": "hey"})
	if err != nil || shout != "HEY" {
		t.Fatalf("got %v, %v", shout, err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "shouting") {
		t.Fatalf("log lines %q", lines)
	}
}

func TestIntegrationFunctionByPath(t *testing.T) {
	c := integrationClient(t)
	for path, want := range map[string]any{
		"folder/nested:where": "folder/nested:where",
		"folder/nested":       "folder/nested:default",
	} {
		got, err := c.Function(ictx(t), path, nil)
		if err != nil || got != want {
			t.Fatalf("Function(%q) = %v, %v, want %v", path, got, err, want)
		}
	}
	got, err := c.Function(ictx(t), "values:special", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["int"] != int64(9007199254740993) {
		t.Fatalf("run endpoint lost the Int64 type: %#v", got)
	}
}

func TestIntegrationErrors(t *testing.T) {
	c := integrationClient(t)

	_, err := c.Query(ictx(t), "messages:fail", nil)
	var ee *ExecutionError
	if !errors.As(err, &ee) || !strings.Contains(ee.Message, "Uncaught Error: boom") {
		t.Fatalf("fail: %v (%T)", err, err)
	}

	_, err = c.Query(ictx(t), "messages:missing", nil)
	if !errors.As(err, &ee) || !strings.Contains(ee.Message, "Could not find public function") {
		t.Fatalf("missing: %v (%T)", err, err)
	}

	_, err = c.Mutation(ictx(t), "messages:send", map[string]any{"author": "a", "body": strings.Repeat("x", 101)})
	var ce *ConvexError
	if !errors.As(err, &ce) {
		t.Fatalf("send: %v (%T)", err, err)
	}
	if !reflect.DeepEqual(ce.Data, map[string]any{"code": "TOO_LONG", "max": int64(100)}) {
		t.Fatalf("data %#v", ce.Data)
	}

	_, err = c.Mutation(ictx(t), "messages:failWithString", nil)
	if !errors.As(err, &ce) || ce.Data != "plain message" {
		t.Fatalf("failWithString: %v (%T)", err, err)
	}

	_, err = c.Mutation(ictx(t), "messages:send", map[string]any{"author": 1})
	if !errors.As(err, &ee) || !strings.Contains(ee.Message, "ArgumentValidationError") {
		t.Fatalf("validator: %v (%T)", err, err)
	}

	bad := integrationClient(t, WithAuth("not-a-jwt"))
	_, err = bad.Query(ictx(t), "messages:whoami", nil)
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != 401 || he.Code == "" {
		t.Fatalf("bad token: %v (%T)", err, err)
	}
}

func TestIntegrationAdminAuth(t *testing.T) {
	key := adminKey(t)
	c := integrationClient(t)

	if _, err := c.Query(ictx(t), "messages:secret", nil); err == nil {
		t.Fatal("an internal query ran without admin auth")
	}
	c.SetAdminAuth(key)
	got, err := c.Query(ictx(t), "messages:secret", nil)
	if err != nil || got != "internal only" {
		t.Fatalf("admin: %v, %v", got, err)
	}

	if err := c.SetAdminAuthActingAs(key, map[string]any{"subject": "user-42", "issuer": "https://issuer.example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err = c.Query(ictx(t), "messages:whoami", nil)
	if err != nil || got != "user-42" {
		t.Fatalf("acting as: %v, %v", got, err)
	}

	c.ClearAuth()
	got, err = c.Query(ictx(t), "messages:whoami", nil)
	if err != nil || got != nil {
		t.Fatalf("cleared: %v, %v", got, err)
	}
}
