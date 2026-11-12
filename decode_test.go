package convex

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

type Task struct {
	ID          string    `json:"_id"`
	CreatedAt   float64   `json:"_creationTime"`
	Text        string    `json:"text"`
	IsCompleted bool      `json:"isCompleted"`
	Priority    int       `json:"priority"`
	Votes       int64     `json:"votes"`
	Owner       *string   `json:"owner"`
	Labels      []string  `json:"labels"`
	Avatar      []byte    `json:"avatar"`
	Due         time.Time `json:"due"`
	Meta        map[string]any
	Ignored     string `json:"-"`
}

func TestDecodeIntoStruct(t *testing.T) {
	src := map[string]any{
		"_id":           "k57abc",
		"_creationTime": 1731470000000.25,
		"text":          "Buy milk",
		"isCompleted":   true,
		"priority":      3.0,
		"votes":         int64(9007199254740993),
		"owner":         "ada",
		"labels":        []any{"home", "food"},
		"avatar":        []byte{1, 2},
		"due":           "2026-11-20T10:00:00Z",
		"meta":          map[string]any{"k": int64(1)},
		"unknownField":  "dropped",
		"-":             "not the ignored field",
	}
	var got Task
	if err := DecodeInto(src, &got); err != nil {
		t.Fatal(err)
	}
	owner := "ada"
	want := Task{
		ID: "k57abc", CreatedAt: 1731470000000.25, Text: "Buy milk", IsCompleted: true,
		Priority: 3, Votes: 9007199254740993, Owner: &owner, Labels: []string{"home", "food"},
		Avatar: []byte{1, 2}, Due: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		Meta: map[string]any{"k": int64(1)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestDecodeIntoValues(t *testing.T) {
	type pair struct {
		A float64
		B string `json:"b"`
	}
	type embedded struct {
		Base
		Extra string `json:"extra"`
	}
	type ptrEmbedded struct {
		*Base
		Extra string `json:"extra"`
	}
	cases := []struct {
		name string
		src  any
		out  any // pointer to zero value of the target type
		want any
	}{
		{"any keeps value", map[string]any{"a": int64(1)}, new(any), map[string]any{"a": int64(1)}},
		{"any nil", nil, new(any), nil},
		{"bool", true, new(bool), true},
		{"string", "s", new(string), "s"},
		{"named string", "u1", new(userID), userID("u1")},
		{"int from int64", int64(-5), new(int), -5},
		{"int from integral float", 42.0, new(int), 42},
		{"int8 from float", -128.0, new(int8), int8(-128)},
		{"uint from int64", int64(7), new(uint), uint(7)},
		{"uint16 from float", 65535.0, new(uint16), uint16(65535)},
		{"int64 from int64", int64(math.MaxInt64), new(int64), int64(math.MaxInt64)},
		{"float from float", 1.5, new(float64), 1.5},
		{"float from int64", int64(3), new(float64), 3.0},
		{"float32", 0.5, new(float32), float32(0.5)},
		{"bytes", []byte("x"), new([]byte), []byte("x")},
		{"named bytes", []byte("x"), new(blob), blob("x")},
		{"byte array", []byte{1, 2, 3}, new([3]byte), [3]byte{1, 2, 3}},
		{"slice of float", []any{1.0, 2.0}, new([]float64), []float64{1, 2}},
		{"array of ints", []any{1.0, int64(2)}, new([2]int), [2]int{1, 2}},
		{"slice of structs", []any{map[string]any{"A": 1.0, "b": "x"}}, new([]pair), []pair{{1, "x"}}},
		{"case-insensitive field", map[string]any{"a": 2.0, "B": "y"}, new(pair), pair{2, "y"}},
		{"map of int64", map[string]any{"x": int64(1)}, new(map[string]int64), map[string]int64{"x": 1}},
		{"map named key", map[string]any{"u": true}, new(map[userID]bool), map[userID]bool{"u": true}},
		{"pointer allocated", 2.0, new(*float64), ptr(2.0)},
		{"null pointer", nil, new(*float64), (*float64)(nil)},
		{"null slice", nil, new([]string), []string(nil)},
		{"null into int leaves zero", nil, new(int), 0},
		{"embedded struct", map[string]any{"_id": "i", "Created": 2.0, "extra": "e"}, new(embedded), embedded{Base{"i", 2}, "e"}},
		{"embedded pointer", map[string]any{"_id": "i", "extra": "e"}, new(ptrEmbedded), ptrEmbedded{&Base{ID: "i"}, "e"}},
		{"text unmarshaler", "2026-01-02T03:04:05Z", new(time.Time), time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := DecodeInto(tc.src, tc.out); err != nil {
				t.Fatalf("DecodeInto: %v", err)
			}
			got := reflect.ValueOf(tc.out).Elem().Interface()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDecodeIntoErrors(t *testing.T) {
	type fixed struct {
		N int `json:"n"`
	}
	cases := []struct {
		name string
		src  any
		out  any
		want string
	}{
		{"nil target", 1.0, nil, "non-nil pointer"},
		{"non-pointer target", 1.0, 3, "non-nil pointer"},
		{"string into int", "x", new(int), "cannot decode String into int"},
		{"fractional float into int", 1.5, new(int), "does not fit"},
		{"NaN into int", math.NaN(), new(int), "does not fit"},
		{"huge float into int64", 1e19, new(int64), "does not fit"},
		{"overflow int8", 300.0, new(int8), "overflows int8"},
		{"negative into uint", int64(-1), new(uint), "does not fit"},
		{"negative float into uint", -1.0, new(uint32), "does not fit"},
		{"overflow uint8", int64(256), new(uint8), "overflows uint8"},
		{"inexact int64 into float", int64(1<<53 + 1), new(float64), "exactly"},
		{"overflow float32", 1e300, new(float32), "overflows float32"},
		{"bool into string", true, new(string), "cannot decode Boolean"},
		{"string into bytes", "abc", new([]byte), "cannot decode String"},
		{"wrong array length", []any{1.0}, new([2]int), "needs 2"},
		{"wrong byte array length", []byte{1}, new([2]byte), "needs 2"},
		{"object into slice", map[string]any{}, new([]int), "cannot decode Object"},
		{"array into struct", []any{}, new(fixed), "cannot decode Array"},
		{"int map keys", map[string]any{"1": 1.0}, new(map[int]int), "map keys must be strings"},
		{"nested path", map[string]any{"n": "x"}, new(fixed), "n: cannot decode String"},
		{"bad time", "yesterday", new(time.Time), "cannot parse"},
		{"interface not satisfied", "x", new(error), "cannot decode String"},
		{"chan target", 1.0, new(chan int), "cannot decode into"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := DecodeInto(tc.src, tc.out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("DecodeInto error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestEncodeDecodeStructRoundTrip(t *testing.T) {
	in := Message{Base: Base{ID: "m", Created: 3}, Author: "Ada", Body: "hi", Likes: math.MaxInt64, Tags: []string{"a"}}
	data, err := EncodeJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	v, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	var out Message
	if err := DecodeInto(v, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("got %+v, want %+v", out, in)
	}
}

func TestOmitEmptyKinds(t *testing.T) {
	type opts struct {
		B  bool           `json:"b,omitempty"`
		I  int            `json:"i,omitempty"`
		I6 int64          `json:"i6,omitempty"`
		U  uint           `json:"u,omitempty"`
		F  float64        `json:"f,omitempty"`
		S  string         `json:"s,omitempty"`
		P  *string        `json:"p,omitempty"`
		A  any            `json:"a,omitempty"`
		M  map[string]int `json:"m,omitempty"`
		L  []int          `json:"l,omitempty"`
		Ar [0]int         `json:"ar,omitempty"`
		St struct{}       `json:"st,omitempty"`
	}
	data, err := EncodeJSON(opts{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"st":{}}` {
		t.Fatalf("got %s, want only the struct kept, like encoding/json", data)
	}
	data, err = EncodeJSON(opts{B: true, I: 1, I6: 1, U: 1, F: 1, S: "s", P: ptr(""), A: 0.0, M: map[string]int{"k": 1}, L: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":0,"b":true,"f":1,"i":1,"i6":{"$integer":"AQAAAAAAAAA="},"l":[1],"m":{"k":1},"p":"","s":"s","st":{},"u":1}`
	if string(data) != want {
		t.Fatalf("got  %s\nwant %s", data, want)
	}
}

func TestNilEmbeddedPointerEncodes(t *testing.T) {
	type outer struct {
		*Base
		Name string `json:"name"`
	}
	data, err := EncodeJSON(outer{Name: "n"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"name":"n"}` {
		t.Fatalf("got %s", data)
	}
	if got := convexTypeName(struct{}{}); got != "struct {}" {
		t.Fatalf("convexTypeName = %q", got)
	}
}

func TestDuplicateFieldNamesCancel(t *testing.T) {
	type A struct{ Name string }
	type B struct{ Name string }
	type both struct {
		A
		B
		Other string
	}
	data, err := EncodeJSON(both{A{"a"}, B{"b"}, "o"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"Other":"o"}` {
		t.Fatalf("got %s, want the ambiguous Name dropped like encoding/json", data)
	}
}
