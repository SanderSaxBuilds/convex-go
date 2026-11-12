package convex

import (
	"encoding"
	"math"
	"reflect"
	"strings"
	"sync"
)

// DecodeInto copies a Convex value, as returned by Query, Mutation, Action,
// Function or FromJSON, into the Go value that out points to.
//
// It follows the same rules as encoding/json: struct fields are matched by
// their json tag or name (exact match first, then case-insensitive), unknown
// object fields are ignored and pointers are allocated as needed. Numbers are
// converted between Int64 and Float64 when the target type asks for it and
// the conversion is exact, so a Convex Float64 of 3 fits an int field but 3.5
// does not. Strings decode into types that implement encoding.TextUnmarshaler,
// such as time.Time.
func DecodeInto(value any, out any) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &ValueError{Msg: "DecodeInto needs a non-nil pointer"}
	}
	return assign(rv.Elem(), value, "", 0)
}

func assign(dst reflect.Value, src any, path string, depth int) error {
	if depth > maxDepth {
		return valueErr(path, "value is nested more than %d levels deep", maxDepth)
	}

	if s, ok := src.(string); ok && dst.Kind() != reflect.Interface {
		if dst.CanAddr() && dst.Addr().CanInterface() && dst.Addr().Type().Implements(textUnmarshalerType) {
			if err := dst.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s)); err != nil {
				return valueErr(path, "%v", err)
			}
			return nil
		}
	}

	switch dst.Kind() {
	case reflect.Interface:
		if src == nil {
			dst.Set(reflect.Zero(dst.Type()))
			return nil
		}
		sv := reflect.ValueOf(src)
		if !sv.Type().AssignableTo(dst.Type()) {
			return mismatch(path, src, dst.Type())
		}
		dst.Set(sv)
		return nil
	case reflect.Pointer:
		if src == nil {
			dst.Set(reflect.Zero(dst.Type()))
			return nil
		}
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return assign(dst.Elem(), src, path, depth+1)
	}

	if src == nil {
		// Like encoding/json, null leaves non-nullable values unchanged and
		// clears maps and slices.
		switch dst.Kind() {
		case reflect.Map, reflect.Slice:
			dst.Set(reflect.Zero(dst.Type()))
		}
		return nil
	}

	switch dst.Kind() {
	case reflect.Bool:
		b, ok := src.(bool)
		if !ok {
			return mismatch(path, src, dst.Type())
		}
		dst.SetBool(b)
		return nil
	case reflect.String:
		s, ok := src.(string)
		if !ok {
			return mismatch(path, src, dst.Type())
		}
		dst.SetString(s)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var n int64
		switch v := src.(type) {
		case int64:
			n = v
		case float64:
			if v != math.Trunc(v) || math.IsInf(v, 0) || v < math.MinInt64 || v >= math.MaxInt64 {
				return valueErr(path, "Float64 %v does not fit in %s", v, dst.Type())
			}
			n = int64(v)
		default:
			return mismatch(path, src, dst.Type())
		}
		if dst.OverflowInt(n) {
			return valueErr(path, "%d overflows %s", n, dst.Type())
		}
		dst.SetInt(n)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var n uint64
		switch v := src.(type) {
		case int64:
			if v < 0 {
				return valueErr(path, "%d does not fit in %s", v, dst.Type())
			}
			n = uint64(v)
		case float64:
			if v != math.Trunc(v) || math.IsInf(v, 0) || v < 0 || v >= math.MaxUint64 {
				return valueErr(path, "Float64 %v does not fit in %s", v, dst.Type())
			}
			n = uint64(v)
		default:
			return mismatch(path, src, dst.Type())
		}
		if dst.OverflowUint(n) {
			return valueErr(path, "%d overflows %s", n, dst.Type())
		}
		dst.SetUint(n)
		return nil
	case reflect.Float32, reflect.Float64:
		var f float64
		switch v := src.(type) {
		case float64:
			f = v
		case int64:
			f = float64(v)
			if int64(f) != v {
				return valueErr(path, "Int64 %d cannot be represented exactly as %s", v, dst.Type())
			}
		default:
			return mismatch(path, src, dst.Type())
		}
		if dst.OverflowFloat(f) {
			return valueErr(path, "%v overflows %s", f, dst.Type())
		}
		dst.SetFloat(f)
		return nil
	case reflect.Slice:
		if dst.Type().Elem().Kind() == reflect.Uint8 {
			b, ok := src.([]byte)
			if !ok {
				return mismatch(path, src, dst.Type())
			}
			cp := reflect.MakeSlice(dst.Type(), len(b), len(b))
			reflect.Copy(cp, reflect.ValueOf(b))
			dst.Set(cp)
			return nil
		}
		arr, ok := src.([]any)
		if !ok {
			return mismatch(path, src, dst.Type())
		}
		s := reflect.MakeSlice(dst.Type(), len(arr), len(arr))
		for i, e := range arr {
			if err := assign(s.Index(i), e, joinIndex(path, i), depth+1); err != nil {
				return err
			}
		}
		dst.Set(s)
		return nil
	case reflect.Array:
		if dst.Type().Elem().Kind() == reflect.Uint8 {
			b, ok := src.([]byte)
			if !ok {
				return mismatch(path, src, dst.Type())
			}
			if len(b) != dst.Len() {
				return valueErr(path, "got %d bytes, %s needs %d", len(b), dst.Type(), dst.Len())
			}
			reflect.Copy(dst, reflect.ValueOf(b))
			return nil
		}
		arr, ok := src.([]any)
		if !ok {
			return mismatch(path, src, dst.Type())
		}
		if len(arr) != dst.Len() {
			return valueErr(path, "got %d elements, %s needs %d", len(arr), dst.Type(), dst.Len())
		}
		for i, e := range arr {
			if err := assign(dst.Index(i), e, joinIndex(path, i), depth+1); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		obj, ok := src.(map[string]any)
		if !ok {
			return mismatch(path, src, dst.Type())
		}
		if dst.Type().Key().Kind() != reflect.String {
			return valueErr(path, "cannot decode an object into %s, map keys must be strings", dst.Type())
		}
		m := reflect.MakeMapWithSize(dst.Type(), len(obj))
		for k, e := range obj {
			ev := reflect.New(dst.Type().Elem()).Elem()
			if err := assign(ev, e, joinKey(path, k), depth+1); err != nil {
				return err
			}
			m.SetMapIndex(reflect.ValueOf(k).Convert(dst.Type().Key()), ev)
		}
		dst.Set(m)
		return nil
	case reflect.Struct:
		obj, ok := src.(map[string]any)
		if !ok {
			return mismatch(path, src, dst.Type())
		}
		fields := cachedFields(dst.Type())
		for k, e := range obj {
			f := matchField(fields, k)
			if f == nil {
				continue
			}
			fv, err := fieldForSet(dst, f.index, joinKey(path, k))
			if err != nil {
				return err
			}
			if err := assign(fv, e, joinKey(path, k), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return valueErr(path, "cannot decode into %s", dst.Type())
}

func mismatch(path string, src any, t reflect.Type) error {
	return valueErr(path, "cannot decode %s into %s", convexTypeName(src), t)
}

func convexTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "Boolean"
	case string:
		return "String"
	case float64:
		return "Float64"
	case int64:
		return "Int64"
	case []byte:
		return "Bytes"
	case []any:
		return "Array"
	case map[string]any:
		return "Object"
	}
	return reflect.TypeOf(v).String()
}

// Struct field handling, a small subset of encoding/json's rules.

type field struct {
	name      string
	index     []int
	omitEmpty bool
}

var fieldCache sync.Map // reflect.Type -> []field

func cachedFields(t reflect.Type) []field {
	if f, ok := fieldCache.Load(t); ok {
		return f.([]field)
	}
	f, _ := fieldCache.LoadOrStore(t, typeFields(t))
	return f.([]field)
}

func typeFields(t reflect.Type) []field {
	var out []field
	seen := map[string]bool{}
	// Breadth-first over embedded structs so shallower fields win.
	type level struct {
		t     reflect.Type
		index []int
	}
	current := []level{{t: t}}
	visited := map[reflect.Type]bool{}
	for len(current) > 0 {
		var next []level
		names := map[string]int{}
		var found []field
		for _, l := range current {
			if visited[l.t] {
				continue
			}
			visited[l.t] = true
			for i := 0; i < l.t.NumField(); i++ {
				sf := l.t.Field(i)
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts, _ := strings.Cut(tag, ",")
				index := append(append([]int(nil), l.index...), i)
				if sf.Anonymous && name == "" {
					ft := sf.Type
					if ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if ft.Kind() == reflect.Struct {
						next = append(next, level{t: ft, index: index})
						continue
					}
				}
				if !sf.IsExported() {
					continue
				}
				if name == "" {
					name = sf.Name
				}
				found = append(found, field{name: name, index: index, omitEmpty: hasOption(opts, "omitempty")})
				names[name]++
			}
		}
		for _, f := range found {
			// Two fields with the same name at the same depth cancel out.
			if seen[f.name] || names[f.name] > 1 {
				continue
			}
			out = append(out, f)
		}
		for _, f := range found {
			seen[f.name] = true
		}
		current = next
	}
	return out
}

func hasOption(opts, want string) bool {
	for opts != "" {
		var o string
		o, opts, _ = strings.Cut(opts, ",")
		if o == want {
			return true
		}
	}
	return false
}

func matchField(fields []field, key string) *field {
	for i := range fields {
		if fields[i].name == key {
			return &fields[i]
		}
	}
	for i := range fields {
		if strings.EqualFold(fields[i].name, key) {
			return &fields[i]
		}
	}
	return nil
}

// fieldByIndex walks into embedded structs for reading. It reports false when
// an embedded pointer on the way is nil.
func fieldByIndex(v reflect.Value, index []int) (reflect.Value, bool) {
	for i, x := range index {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}, false
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v, true
}

// fieldForSet walks into embedded structs for writing, allocating embedded
// pointers when needed.
func fieldForSet(v reflect.Value, index []int, path string) (reflect.Value, error) {
	for i, x := range index {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				if !v.CanSet() {
					return reflect.Value{}, valueErr(path, "cannot set embedded pointer to unexported struct %s", v.Type().Elem())
				}
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v, nil
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}
