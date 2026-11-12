package convex

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

type celsius float64
type userID string
type counter int64
type small int16
type blob []byte

type textID struct{ n int }

func (t textID) MarshalText() ([]byte, error) { return []byte("id-" + string(rune('0'+t.n))), nil }

type badText struct{}

func (badText) MarshalText() ([]byte, error) { return nil, errors.New("no text") }

type Base struct {
	ID      string `json:"_id"`
	Created float64
}

type Message struct {
	Base
	Author  string   `json:"author"`
	Body    string   `json:"body,omitempty"`
	Likes   int64    `json:"likes"`
	Tags    []string `json:"tags,omitempty"`
	Secret  string   `json:"-"`
	private string
}

func nanBits(bits uint64) float64 { return math.Float64frombits(bits) }

func TestEncodeJSON(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, `null`},
		{"true", true, `true`},
		{"false", false, `false`},
		{"string", "abc", `"abc"`},
		{"empty string", "", `""`},
		{"unicode string", "héllo 世界", `"héllo 世界"`},
		{"float", 0.123, `0.123`},
		{"float zero", 0.0, `0`},
		{"float large", 1e300, `1e+300`},
		{"float32", float32(1.5), `1.5`},
		{"named float", celsius(36.6), `36.6`},
		{"int becomes float", 17, `17`},
		{"int8 becomes float", int8(-8), `-8`},
		{"int32 becomes float", int32(1 << 30), `1073741824`},
		{"uint16 becomes float", uint16(65535), `65535`},
		{"uint becomes float", uint(42), `42`},
		{"int at 2^53", 1 << 53, `9007199254740992`},
		{"int at -2^53", -(1 << 53), `-9007199254740992`},
		{"int64 zero", int64(0), `{"$integer":"AAAAAAAAAAA="}`},
		{"int64 one", int64(1), `{"$integer":"AQAAAAAAAAA="}`},
		{"int64 minus one", int64(-1), `{"$integer":"//////////8="}`},
		{"int64 123", int64(123), `{"$integer":"ewAAAAAAAAA="}`},
		{"int64 max", int64(math.MaxInt64), `{"$integer":"/////////38="}`},
		{"int64 min", int64(math.MinInt64), `{"$integer":"AAAAAAAAAIA="}`},
		{"int64 past 2^53", int64(1<<53 + 1), `{"$integer":"AQAAAAAAIAA="}`},
		{"int64 negative", int64(-1234567890123), `{"$integer":"NfsEjuD+//8="}`},
		{"named int64", counter(1), `{"$integer":"AQAAAAAAAAA="}`},
		{"duration is int64", time.Duration(1), `{"$integer":"AQAAAAAAAAA="}`},
		{"uint64", uint64(123), `{"$integer":"ewAAAAAAAAA="}`},
		{"positive infinity", math.Inf(1), `{"$float":"AAAAAAAA8H8="}`},
		{"negative infinity", math.Inf(-1), `{"$float":"AAAAAAAA8P8="}`},
		{"negative zero", math.Copysign(0, -1), `{"$float":"AAAAAAAAAIA="}`},
		{"canonical NaN", nanBits(0x7FF8000000000000), `{"$float":"AAAAAAAA+H8="}`},
		{"math.NaN keeps its bits", math.NaN(), `{"$float":"AQAAAAAA+H8="}`},
		{"float32 infinity", float32(math.Inf(1)), `{"$float":"AAAAAAAA8H8="}`},
		{"bytes", []byte("abc"), `{"$bytes":"YWJj"}`},
		{"bytes binary", []byte{0, 255, 16}, `{"$bytes":"AP8Q"}`},
		{"empty bytes", []byte{}, `{"$bytes":""}`},
		{"nil bytes", []byte(nil), `null`},
		{"named bytes", blob("abc"), `{"$bytes":"YWJj"}`},
		{"byte array", [3]byte{'a', 'b', 'c'}, `{"$bytes":"YWJj"}`},
		{"array of any", []any{1.0, "a", nil, true}, `[1,"a",null,true]`},
		{"empty array", []any{}, `[]`},
		{"nil slice", []string(nil), `null`},
		{"typed slice", []string{"a", "b"}, `["a","b"]`},
		{"go array", [2]int{1, 2}, `[1,2]`},
		{"nested arrays", [][]int64{{1}}, `[[{"$integer":"AQAAAAAAAAA="}]]`},
		{"object", map[string]any{"a": 1.0, "b": "x"}, `{"a":1,"b":"x"}`},
		{"empty object", map[string]any{}, `{}`},
		{"nil map", map[string]any(nil), `null`},
		{"typed map", map[string]int{"n": 3}, `{"n":3}`},
		{"named key map", map[userID]bool{"u1": true}, `{"u1":true}`},
		{"nested object", map[string]any{"o": map[string]any{"i": int64(1)}}, `{"o":{"i":{"$integer":"AQAAAAAAAAA="}}}`},
		{"underscore field", map[string]any{"_id": "abc"}, `{"_id":"abc"}`},
		{"named string", userID("u1"), `"u1"`},
		{"named int16", small(7), `7`},
		{"pointer", ptr("p"), `"p"`},
		{"nil pointer", (*string)(nil), `null`},
		{"pointer to pointer", ptr(ptr(int64(1))), `{"$integer":"AQAAAAAAAAA="}`},
		{"text marshaler", textID{n: 7}, `"id-7"`},
		{"time", time.Date(2026, 11, 13, 4, 5, 6, 0, time.UTC), `"2026-11-13T04:05:06Z"`},
		{
			"struct with tags and embedding",
			Message{Base: Base{ID: "m1", Created: 1.5}, Author: "Ada", Likes: 2, Secret: "x", private: "y"},
			`{"Created":1.5,"_id":"m1","author":"Ada","likes":{"$integer":"AgAAAAAAAAA="}}`,
		},
		{
			"struct omitempty kept when set",
			Message{Author: "Ada", Body: "hi", Tags: []string{"t"}},
			`{"Created":0,"_id":"","author":"Ada","body":"hi","likes":{"$integer":"AAAAAAAAAAA="},"tags":["t"]}`,
		},
		{"struct pointer", &struct{ A bool }{true}, `{"A":true}`},
		{"anonymous struct", struct {
			X float64 `json:"x"`
		}{2}, `{"x":2}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EncodeJSON(tc.in)
			if err != nil {
				t.Fatalf("EncodeJSON(%#v): %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Fatalf("EncodeJSON(%#v)\n got %s\nwant %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestEncodeErrors(t *testing.T) {
	ch := make(chan int)
	type cyclic struct{ Next *cyclic }
	loop := &cyclic{}
	loop.Next = loop
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"int past 2^53", 1<<53 + 1, "outside the range of a Convex Float64"},
		{"int below -2^53", -(1<<53 + 1), "outside the range of a Convex Float64"},
		{"uint past 2^53", uint(1<<53 + 1), "outside the range of a Convex Float64"},
		{"uint64 past int64", uint64(math.MaxUint64), "does not fit in a Convex Int64"},
		{"dollar key", map[string]any{"$a": 1.0}, "starts with a '$'"},
		{"control char key", map[string]any{"a\nb": 1.0}, "non-control ASCII"},
		{"non-ascii key", map[string]any{"ключ": 1.0}, "non-control ASCII"},
		{"long key", map[string]any{strings.Repeat("k", 1025): 1.0}, "maximum length"},
		{"int keys", map[int]any{1: 1.0}, "keys must be strings"},
		{"nested int keys", map[string]any{"b": map[int]int{2: 1}}, "keys must be strings"},
		{"channel", ch, "not a supported Convex type"},
		{"func", func() {}, "not a supported Convex type"},
		{"complex", complex(1, 2), "not a supported Convex type"},
		{"text marshaler error", badText{}, "no text"},
		{"cycle", loop, "nested more than"},
		{"struct dollar tag", struct {
			A int `json:"$a"`
		}{1}, "starts with a '$'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ToJSON(tc.in)
			if err == nil {
				t.Fatalf("ToJSON(%v) succeeded, want error containing %q", tc.name, tc.want)
			}
			var ve *ValueError
			if !errors.As(err, &ve) {
				t.Fatalf("error %v is %T, want *ValueError", err, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestEncodeErrorPath(t *testing.T) {
	_, err := ToJSON(map[string]any{"items": []any{1.0, map[string]any{"price": 1 << 60}}})
	var ve *ValueError
	if !errors.As(err, &ve) || ve.Path != "items[1].price" {
		t.Fatalf("got %v, want a ValueError at items[1].price", err)
	}
}

func TestDecodeJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want any
	}{
		{"null", `null`, nil},
		{"true", `true`, true},
		{"string", `"abc"`, "abc"},
		{"number", `0.123`, 0.123},
		{"integer number is float", `17`, 17.0},
		{"big integer number is float", `12345678901234567890`, 12345678901234567890.0},
		{"int64", `{"$integer":"AQAAAAAAAAA="}`, int64(1)},
		{"int64 max", `{"$integer":"/////////38="}`, int64(math.MaxInt64)},
		{"int64 min", `{"$integer":"AAAAAAAAAIA="}`, int64(math.MinInt64)},
		{"bytes", `{"$bytes":"YWJj"}`, []byte("abc")},
		{"empty bytes", `{"$bytes":""}`, []byte{}},
		{"infinity", `{"$float":"AAAAAAAA8H8="}`, math.Inf(1)},
		{"negative infinity", `{"$float":"AAAAAAAA8P8="}`, math.Inf(-1)},
		{"array", `[1,"a",null,{"$integer":"AgAAAAAAAAA="}]`, []any{1.0, "a", nil, int64(2)}},
		{"object", `{"a":1,"b":{"c":[]}}`, map[string]any{"a": 1.0, "b": map[string]any{"c": []any{}}}},
		{"empty object", `{}`, map[string]any{}},
		{"system fields", `{"_id":"k1","_creationTime":1731470000000.5}`, map[string]any{"_id": "k1", "_creationTime": 1731470000000.5}},
		{"single key object", `{"integer":"x"}`, map[string]any{"integer": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeJSON([]byte(tc.in))
			if err != nil {
				t.Fatalf("DecodeJSON(%s): %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DecodeJSON(%s) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDecodeSpecialFloats(t *testing.T) {
	got, err := DecodeJSON([]byte(`{"$float":"AAAAAAAA+H8="}`))
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := got.(float64); !ok || !math.IsNaN(f) || math.Float64bits(f) != 0x7FF8000000000000 {
		t.Fatalf("got %#v, want canonical NaN", got)
	}
	got, err = DecodeJSON([]byte(`{"$float":"AAAAAAAAAIA="}`))
	if err != nil {
		t.Fatal(err)
	}
	if f := got.(float64); f != 0 || !math.Signbit(f) {
		t.Fatalf("got %v, want negative zero", f)
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"not json", `{`, "invalid JSON"},
		{"int64 too short", `{"$integer":"AQAAAA=="}`, "exactly eight bytes"},
		{"int64 bad base64", `{"$integer":"!!!"}`, "invalid base64"},
		{"int64 not string", `{"$integer":1}`, "base64 string"},
		{"float too long", `{"$float":"AAAAAAAAAAAAAAAA"}`, "exactly eight bytes"},
		{"float that is a plain number", `{"$float":"AAAAAAAA8D8="}`, "should be encoded as a number"},
		{"float zero is plain", `{"$float":"AAAAAAAAAAA="}`, "should be encoded as a number"},
		{"bytes bad base64", `{"$bytes":"a"}`, "invalid base64"},
		{"set", `{"$set":[]}`, "no longer supported"},
		{"map", `{"$map":[]}`, "no longer supported"},
		{"unknown special", `{"$a":1}`, "unrecognized special value"},
		{"dollar key among others", `{"$a":1,"b":2}`, "starts with a '$'"},
		{"nested error path", `{"a":[{"$integer":"x"}]}`, "a[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeJSON([]byte(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("DecodeJSON(%s) error = %v, want it to contain %q", tc.in, err, tc.want)
			}
		})
	}
}

func TestFromJSONAcceptsNumber(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader([]byte(`{"n": 1.25}`)))
	dec.UseNumber()
	var j any
	if err := dec.Decode(&j); err != nil {
		t.Fatal(err)
	}
	got, err := FromJSON(j)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]any{"n": 1.25}) {
		t.Fatalf("got %#v", got)
	}
	if _, err := FromJSON(json.Number("x")); err == nil {
		t.Fatal("want error for an invalid json.Number")
	}
	if _, err := FromJSON(struct{}{}); err == nil {
		t.Fatal("want error for a non-JSON Go type")
	}
}

// Every canonical value must survive Go -> JSON -> Go unchanged.
func TestRoundTrip(t *testing.T) {
	values := []any{
		nil, true, false, "", "abc", 0.0, -1.5, 1e-300, math.MaxFloat64,
		math.Inf(1), math.Inf(-1), math.Copysign(0, -1),
		int64(0), int64(-7), int64(math.MaxInt64), int64(math.MinInt64),
		[]byte{}, []byte{1, 2, 3},
		[]any{}, []any{int64(1), 2.0, "three", []byte("4"), nil},
		map[string]any{}, map[string]any{"a": map[string]any{"b": []any{int64(9)}}},
	}
	for _, v := range values {
		data, err := EncodeJSON(v)
		if err != nil {
			t.Fatalf("EncodeJSON(%#v): %v", v, err)
		}
		back, err := DecodeJSON(data)
		if err != nil {
			t.Fatalf("DecodeJSON(%s): %v", data, err)
		}
		if !sameValue(back, v) {
			t.Fatalf("round trip of %#v gave %#v (wire %s)", v, back, data)
		}
		again, err := EncodeJSON(back)
		if err != nil || !bytes.Equal(again, data) {
			t.Fatalf("re-encoding %#v gave %s, want %s (err %v)", back, again, data, err)
		}
	}
}

// sameValue is reflect.DeepEqual that also tells negative zero apart and
// treats NaN as equal to NaN with the same bits.
func sameValue(a, b any) bool {
	fa, okA := a.(float64)
	fb, okB := b.(float64)
	if okA && okB {
		return math.Float64bits(fa) == math.Float64bits(fb)
	}
	switch av := a.(type) {
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !sameValue(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k := range av {
			if !sameValue(av[k], bv[k]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func ptr[T any](v T) *T { return &v }
