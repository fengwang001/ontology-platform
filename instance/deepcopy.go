package instance

import "reflect"

// deepCopyProperties returns a property map that shares no mutable
// backing storage with props. It understands the JSON-like property
// values used by instances: maps, slices, strings, numbers, bools and
// pointers to those.
func deepCopyProperties(props map[string]any) map[string]any {
	if props == nil {
		return nil
	}
	dst := make(map[string]any, len(props))
	for k, v := range props {
		dst[k] = deepCopy(v)
	}
	return dst
}

// deepCopy returns a value that shares no mutable backing storage
// with v. It understands the JSON-like property values used by
// instances: maps, slices, strings, numbers, bools and pointers to
// those. Values of unsupported kinds are copied by value.
func deepCopy(v any) any {
	if v == nil {
		return nil
	}
	return copyValue(reflect.ValueOf(v)).Interface()
}

func copyValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		dst := reflect.New(v.Elem().Type())
		dst.Elem().Set(copyValue(v.Elem()))
		return dst
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		return copyValue(v.Elem())
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		dst := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			dst.SetMapIndex(iter.Key(), copyValue(iter.Value()))
		}
		return dst
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		dst := reflect.MakeSlice(v.Type(), v.Len(), v.Cap())
		for i := 0; i < v.Len(); i++ {
			dst.Index(i).Set(copyValue(v.Index(i)))
		}
		return dst
	case reflect.Array:
		dst := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			dst.Index(i).Set(copyValue(v.Index(i)))
		}
		return dst
	default:
		// Strings, numbers, bools and other value types are immutable
		// or copied by value, so a direct copy is safe.
		return v
	}
}
