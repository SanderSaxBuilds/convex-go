package convex

// These tests mirror tests/test_values.py from the Python client
// (github.com/convex/convex-py), with each Python value replaced by its
// closest Go counterpart, so the two clients agree on the wire format.

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// strictRoundtrip mirrors strict_roundtrip: the value encodes, the JSON
// survives serialization, decoding gives back an equal value of the same type
// and re-encoding gives the same JSON.
func strictRoundtrip(t *testing.T, original any) {
	t.Helper()
	jsonObject, err := ToJSON(original)
	if err != nil {
		t.Fatalf("ToJSON(%#v): %v", original, err)
	}
	serialized, err := json.Marshal(jsonObject)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var reparsed any
	if err := json.Unmarshal(serialized, &reparsed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reparsed, jsonObject) {
		t.Fatalf("JSON form %#v did not survive serialization, got %#v", jsonObject, reparsed)
	}
	roundtripped, err := FromJSON(jsonObject)
	if err != nil {
		t.Fatalf("FromJSON(%#v): %v", jsonObject, err)
	}
	if !sameValue(roundtripped, original) {
		t.Fatalf("roundtrip of %#v gave %#v", original, roundtripped)
	}
	if reflect.TypeOf(roundtripped) != reflect.TypeOf(original) {
		t.Fatalf("roundtrip changed type %T to %T", original, roundtripped)
	}
	again, err := ToJSON(roundtripped)
	if err != nil {
		t.Fatal(err)
	}
	againSerialized, _ := json.Marshal(again)
	if string(againSerialized) != string(serialized) {
		t.Fatalf("re-encoded JSON %s differs from %s", againSerialized, serialized)
	}
}

// coercedRoundtrip mirrors coerced_roundtrip: the value may change type when
// it is coerced, but the coerced value must then roundtrip strictly.
func coercedRoundtrip(t *testing.T, original any) {
	t.Helper()
	data, err := EncodeJSON(original)
	if err != nil {
		t.Fatalf("EncodeJSON(%#v): %v", original, err)
	}
	coerced, err := DecodeJSON(data)
	if err != nil {
		t.Fatalf("DecodeJSON(%s): %v", data, err)
	}
	strictRoundtrip(t, coerced)
}

func coercedRoundtripRaises(t *testing.T, original any, message string) {
	t.Helper()
	_, err := ToJSON(original)
	if err == nil {
		t.Fatalf("ToJSON(%#v) succeeded, want an error", original)
	}
	if message != "" && !strings.Contains(err.Error(), message) {
		t.Fatalf("error %q does not contain %q", err, message)
	}
}

func TestPyStrictValues(t *testing.T) {
	for name, v := range map[string]any{
		"None":         nil,
		"True":         true,
		"False":        false,
		"0.123":        0.123,
		"abc":          "abc",
		"dict a":       map[string]any{"a": 0.123},
		"empty dict":   map[string]any{},
		"dict a b":     map[string]any{"a": 1.0, "b": 2.0},
		"bytes abc":    []byte("abc"),
		"inf":          math.Inf(1),
		"negative 0.0": math.Copysign(0, -1),
	} {
		t.Run(name, func(t *testing.T) { strictRoundtrip(t, v) })
	}
}

func TestPyConvexInt64(t *testing.T) {
	strictRoundtrip(t, int64(123))
	v, err := FromJSON(map[string]any{"$integer": "ewAAAAAAAAA="})
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := v.(int64); !ok || n != 123 {
		t.Fatalf("got %#v, want int64(123)", v)
	}
}

// test_floats_get_treated_as_ints: a plain int arrives as a float.
func TestPyIntsBecomeFloats(t *testing.T) {
	data, err := EncodeJSON(123)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := back.(float64); !ok {
		t.Fatalf("int 123 came back as %T, want float64", back)
	}
}

// test_subclassed_values: Go named types play the role of Python subclasses.
func TestPySubclassedValues(t *testing.T) {
	type intSub int
	type floatSub float64
	type strSub string
	type bytesSub []byte
	type dictSub map[string]int
	type listSub []string
	for name, v := range map[string]any{
		"IntSubclass":   intSub(0),
		"FloatSubclass": floatSub(123.0),
		"StrSubclass":   strSub("asdf"),
		"BytesSubclass": bytesSub("adsf"),
		"DictSubclass":  dictSub{"a": 1},
		"ListSubclass":  listSub{"a", "b", "c"},
	} {
		t.Run(name, func(t *testing.T) { coercedRoundtrip(t, v) })
	}
}

// test_coercion: tuples, ranges, bytearrays and Counters have Go cousins in
// arrays, int slices, byte arrays and counting maps.
func TestPyCoercion(t *testing.T) {
	rng := make([]int, 10)
	for i := range rng {
		rng[i] = i
	}
	for name, v := range map[string]any{
		"0":         0,
		"1":         1,
		"tuple":     [2]int{1, 2},
		"range":     rng,
		"bytearray": [3]byte{'a', 'b', 'c'},
		"Counter":   map[string]int{"a": 1, "s": 1, "d": 1, "f": 1},
	} {
		t.Run(name, func(t *testing.T) { coercedRoundtrip(t, v) })
	}
}

func TestPyNonValues(t *testing.T) {
	type object struct{ ch chan int }
	coercedRoundtripRaises(t, make(chan int), "")
	coercedRoundtripRaises(t, func() {}, "")
	coercedRoundtripRaises(t, []func(){func() {}}, "")
	coercedRoundtripRaises(t, map[int]int{1: 1}, "")
	coercedRoundtripRaises(t, struct{ C chan int }{}, "")

	// Integers at the edge of the float64 range are fine, one past is not.
	coercedRoundtrip(t, 1<<53)
	coercedRoundtrip(t, -(1 << 53))
	coercedRoundtripRaises(t, 1<<53+1, "")
	coercedRoundtripRaises(t, -(1<<53 + 1), "")

	// A struct with only unexported fields is an empty object, like
	// encoding/json, not an error.
	coercedRoundtrip(t, object{})
}

func TestPyContextErrors(t *testing.T) {
	coercedRoundtripRaises(t, map[string]any{"$a": 1}, "starts with a '$'")
	coercedRoundtripRaises(t, map[string]any{"b": map[int]int{2: 1}}, "must be strings")
}

func TestPyDecodeJSON(t *testing.T) {
	_, err := FromJSON(map[string]any{"$a": 1.0})
	if err == nil || !strings.Contains(err.Error(), "$") {
		t.Fatalf("FromJSON({$a: 1}) error = %v, want one mentioning '$'", err)
	}
}
