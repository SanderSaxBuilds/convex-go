package convex

import (
	"encoding"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Convex values in Go.
//
// Values returned by this package always use these Go types:
//
//	Convex type   Go type
//	-----------   ---------------
//	Null          nil
//	Int64         int64
//	Float64       float64
//	Boolean       bool
//	String        string
//	Bytes         []byte
//	Array         []any
//	Object        map[string]any
//
// When encoding, int64 (and any named type whose kind is int64) becomes a
// Convex Int64, which is a bigint in JavaScript. The other Go integer types
// (int, int32, uint16 and so on) become a Convex Float64, the same way the
// Python client treats Python ints, so they arrive in JavaScript as a plain
// number. They must fit in the range a float64 represents exactly, -2^53 to
// 2^53. Structs are encoded as objects using their json struct tags.

const (
	// maxSafeInteger is the largest integer a float64 holds exactly. It is one
	// above JavaScript's Number.MAX_SAFE_INTEGER, matching the Python client.
	maxSafeInteger = 1 << 53
	// maxFieldNameLen is the longest object field name Convex accepts.
	maxFieldNameLen = 1024
	// maxDepth guards against reference cycles in Go values.
	maxDepth = 256
)

var (
	textMarshalerType   = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	bytesType           = reflect.TypeOf([]byte(nil))
)

// ValueError reports a value that cannot be converted to or from Convex.
// Path points at the offending element, for example "args.items[2].price".
type ValueError struct {
	Path string
	Msg  string
}

func (e *ValueError) Error() string {
	if e.Path == "" {
		return "convex: " + e.Msg
	}
	return "convex: " + e.Path + ": " + e.Msg
}

func valueErr(path, format string, a ...any) error {
	return &ValueError{Path: path, Msg: fmt.Sprintf(format, a...)}
}

// ToJSON converts a Go value to its Convex JSON form: a tree of nil, bool,
// float64, string, []any and map[string]any that encoding/json can marshal.
// Int64 values become {"$integer": base64}, bytes become {"$bytes": base64}
// and NaN, infinities and negative zero become {"$float": base64}.
//
// It is the Go counterpart of convex_to_json in the Python client.
func ToJSON(v any) (any, error) {
	return toJSON(reflect.ValueOf(v), "", 0)
}

// EncodeJSON converts v with ToJSON and marshals the result.
func EncodeJSON(v any) ([]byte, error) {
	j, err := ToJSON(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(j)
}

// FromJSON converts a Convex JSON tree, as produced by encoding/json
// unmarshaling into an any, into Go values. See the table above for the
// types it returns. json.Number values are accepted and read as Float64.
//
// It is the Go counterpart of json_to_convex in the Python client.
func FromJSON(j any) (any, error) {
	return fromJSON(j, "", 0)
}

// DecodeJSON parses Convex JSON bytes and converts them with FromJSON.
func DecodeJSON(data []byte) (any, error) {
	var j any
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("convex: invalid JSON: %w", err)
	}
	return FromJSON(j)
}

func encodeInt64(n int64) map[string]any {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(n))
	return map[string]any{"$integer": base64.StdEncoding.EncodeToString(b[:])}
}

func encodeBytes(b []byte) map[string]any {
	return map[string]any{"$bytes": base64.StdEncoding.EncodeToString(b)}
}

// isSpecialFloat reports whether f has no plain JSON number form.
func isSpecialFloat(f float64) bool {
	return math.IsNaN(f) || math.IsInf(f, 0) || (f == 0 && math.Signbit(f))
}

func encodeFloat(f float64) any {
	if !isSpecialFloat(f) {
		return f
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], math.Float64bits(f))
	return map[string]any{"$float": base64.StdEncoding.EncodeToString(b[:])}
}

func intToFloat(n int64, path string) (any, error) {
	if n < -maxSafeInteger || n > maxSafeInteger {
		return nil, valueErr(path, "integer %d is outside the range of a Convex Float64 (-2^53 to 2^53), use an int64 to send it as a Convex Int64", n)
	}
	return float64(n), nil
}

func validateFieldName(k, path string) error {
	if len(k) > maxFieldNameLen {
		return valueErr(path, "field name %q exceeds the maximum length of %d", k, maxFieldNameLen)
	}
	if strings.HasPrefix(k, "$") {
		return valueErr(path, "field name %q starts with a '$', which is reserved", k)
	}
	for i := 0; i < len(k); i++ {
		if c := k[i]; c < 32 || c >= 127 {
			return valueErr(path, "field name %q has an invalid character, field names can only contain non-control ASCII characters", k)
		}
	}
	return nil
}

func joinKey(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func joinIndex(path string, i int) string {
	return path + "[" + strconv.Itoa(i) + "]"
}

func toJSON(rv reflect.Value, path string, depth int) (any, error) {
	if depth > maxDepth {
		return nil, valueErr(path, "value is nested more than %d levels deep", maxDepth)
	}
	if !rv.IsValid() {
		return nil, nil
	}

	// Fast paths for the canonical types.
	if rv.CanInterface() {
		switch v := rv.Interface().(type) {
		case nil:
			return nil, nil
		case bool:
			return v, nil
		case string:
			return v, nil
		case float64:
			return encodeFloat(v), nil
		case int64:
			return encodeInt64(v), nil
		case []byte:
			if v == nil {
				return nil, nil
			}
			return encodeBytes(v), nil
		}
	}

	t := rv.Type()
	if rv.CanInterface() && t.Implements(textMarshalerType) && !(rv.Kind() == reflect.Pointer && rv.IsNil()) {
		text, err := rv.Interface().(encoding.TextMarshaler).MarshalText()
		if err != nil {
			return nil, valueErr(path, "%v", err)
		}
		return string(text), nil
	}

	switch rv.Kind() {
	case reflect.Interface, reflect.Pointer:
		if rv.IsNil() {
			return nil, nil
		}
		return toJSON(rv.Elem(), path, depth+1)
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.String:
		return rv.String(), nil
	case reflect.Int64:
		return encodeInt64(rv.Int()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		return intToFloat(rv.Int(), path)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uintptr:
		u := rv.Uint()
		if u > maxSafeInteger {
			return nil, valueErr(path, "integer %d is outside the range of a Convex Float64 (-2^53 to 2^53)", u)
		}
		return float64(u), nil
	case reflect.Uint64:
		u := rv.Uint()
		if u > math.MaxInt64 {
			return nil, valueErr(path, "integer %d does not fit in a Convex Int64", u)
		}
		return encodeInt64(int64(u)), nil
	case reflect.Float32, reflect.Float64:
		return encodeFloat(rv.Float()), nil
	case reflect.Slice:
		if rv.IsNil() {
			return nil, nil
		}
		if t.Elem().Kind() == reflect.Uint8 {
			return encodeBytes(rv.Bytes()), nil
		}
		return sliceToJSON(rv, path, depth)
	case reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			b := make([]byte, rv.Len())
			reflect.Copy(reflect.ValueOf(b), rv)
			return encodeBytes(b), nil
		}
		return sliceToJSON(rv, path, depth)
	case reflect.Map:
		if rv.IsNil() {
			return nil, nil
		}
		if t.Key().Kind() != reflect.String {
			return nil, valueErr(path, "Convex object keys must be strings, found map key type %s", t.Key())
		}
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			k := iter.Key().String()
			p := joinKey(path, k)
			if err := validateFieldName(k, p); err != nil {
				return nil, err
			}
			v, err := toJSON(iter.Value(), p, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	case reflect.Struct:
		fields := cachedFields(t)
		out := make(map[string]any, len(fields))
		for _, f := range fields {
			fv, ok := fieldByIndex(rv, f.index)
			if !ok {
				continue
			}
			if f.omitEmpty && isEmptyValue(fv) {
				continue
			}
			p := joinKey(path, f.name)
			if err := validateFieldName(f.name, p); err != nil {
				return nil, err
			}
			v, err := toJSON(fv, p, depth+1)
			if err != nil {
				return nil, err
			}
			out[f.name] = v
		}
		return out, nil
	}
	return nil, valueErr(path, "%s is not a supported Convex type, see https://docs.convex.dev/database/types", t)
}

func sliceToJSON(rv reflect.Value, path string, depth int) (any, error) {
	n := rv.Len()
	out := make([]any, n)
	for i := 0; i < n; i++ {
		v, err := toJSON(rv.Index(i), joinIndex(path, i), depth+1)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func fromJSON(j any, path string, depth int) (any, error) {
	if depth > maxDepth {
		return nil, valueErr(path, "value is nested more than %d levels deep", maxDepth)
	}
	switch v := j.(type) {
	case nil:
		return nil, nil
	case bool, string, float64:
		return v, nil
	case json.Number:
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return nil, valueErr(path, "invalid number %q", string(v))
		}
		return f, nil
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			c, err := fromJSON(e, joinIndex(path, i), depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case map[string]any:
		if len(v) == 1 {
			for k, inner := range v {
				if strings.HasPrefix(k, "$") {
					return decodeSpecial(k, inner, path)
				}
			}
		}
		out := make(map[string]any, len(v))
		for k, e := range v {
			p := joinKey(path, k)
			if strings.HasPrefix(k, "$") {
				return nil, valueErr(p, "field name %q starts with a '$', which is reserved", k)
			}
			c, err := fromJSON(e, p, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	}
	return nil, valueErr(path, "%T is not a JSON value", j)
}

func decodeSpecial(key string, inner any, path string) (any, error) {
	switch key {
	case "$integer", "$float", "$bytes":
	case "$set", "$map":
		return nil, valueErr(path, "received a %s, which is no longer supported as a Convex type", strings.TrimPrefix(key, "$"))
	default:
		return nil, valueErr(path, "unrecognized special value %q", key)
	}
	s, ok := inner.(string)
	if !ok {
		return nil, valueErr(path, "%s must hold a base64 string, got %T", key, inner)
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, valueErr(path, "%s holds invalid base64: %v", key, err)
	}
	switch key {
	case "$bytes":
		return b, nil
	case "$integer":
		if len(b) != 8 {
			return nil, valueErr(path, "Int64 must be exactly eight bytes, got %d", len(b))
		}
		return int64(binary.LittleEndian.Uint64(b)), nil
	default: // "$float"
		if len(b) != 8 {
			return nil, valueErr(path, "Float64 must be exactly eight bytes, got %d", len(b))
		}
		f := math.Float64frombits(binary.LittleEndian.Uint64(b))
		if !isSpecialFloat(f) {
			return nil, valueErr(path, "Float64 %v should be encoded as a number", f)
		}
		return f, nil
	}
}
