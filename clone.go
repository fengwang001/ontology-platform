package instance

import (
	"fmt"
	"reflect"
)

// cloneAttributes returns a deep, independent copy of attrs and verifies the
// value rules at the same time.
//
// Allowed values are: nil, bool, all numeric kinds, string; slices and arrays
// of allowed values (maps inside are not permitted as elements); and maps keyed
// by string whose values are allowed values. Cyclic values are rejected.
// Structs, pointers, functions, channels and interface keys are not allowed.
func cloneAttributes(attrs map[string]any) (map[string]any, error) {
	if attrs == nil {
		return nil, nil
	}
	seen := map[visit]bool{}
	out := make(map[string]any, len(attrs))
	for key, value := range attrs {
		if key == "" {
			return nil, fmt.Errorf("empty attribute name")
		}
		copied, err := cloneValue(reflect.ValueOf(value), seen)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", key, err)
		}
		out[key] = copied
	}
	return out, nil
}

type visit struct {
	kind reflect.Kind
	ptr  uintptr
}

func cloneValue(v reflect.Value, seen map[visit]bool) (any, error) {
	switch v.Kind() {
	case reflect.Invalid:
		return nil, nil
	case reflect.Bool:
		return v.Interface(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Interface(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Interface(), nil
	case reflect.Float32, reflect.Float64:
		return v.Interface(), nil
	case reflect.String:
		return v.Interface(), nil
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return nil, nil
		}
		return cloneValue(v.Elem(), seen)
	case reflect.Slice, reflect.Array:
		return cloneSequence(v, seen)
	case reflect.Map:
		return cloneMap(v, seen)
	default:
		return nil, fmt.Errorf("unsupported value kind %s", v.Kind())
	}
}

func cloneSequence(v reflect.Value, seen map[visit]bool) ([]any, error) {
	if v.Kind() == reflect.Slice && !v.IsNil() {
		if err := enter(v, seen); err != nil {
			return nil, err
		}
		defer leave(v, seen)
	}
	out := make([]any, v.Len())
	for i := 0; i < v.Len(); i++ {
		copied, err := cloneValue(v.Index(i), seen)
		if err != nil {
			return nil, fmt.Errorf("index %d: %w", i, err)
		}
		out[i] = copied
	}
	return out, nil
}

func cloneMap(v reflect.Value, seen map[visit]bool) (map[string]any, error) {
	if v.Kind() != reflect.Map {
		return nil, fmt.Errorf("unsupported value kind %s", v.Kind())
	}
	if v.IsNil() {
		return nil, nil
	}
	if v.Type().Key().Kind() != reflect.String {
		return nil, fmt.Errorf("map keys must be string, got %s", v.Type().Key().Kind())
	}
	if err := enter(v, seen); err != nil {
		return nil, err
	}
	defer leave(v, seen)
	out := make(map[string]any, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		copied, err := cloneValue(iter.Value(), seen)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", iter.Key().String(), err)
		}
		out[iter.Key().String()] = copied
	}
	return out, nil
}

func enter(v reflect.Value, seen map[visit]bool) error {
	token := visit{kind: v.Kind(), ptr: v.Pointer()}
	if seen[token] {
		return fmt.Errorf("cyclic attribute value")
	}
	seen[token] = true
	return nil
}

func leave(v reflect.Value, seen map[visit]bool) {
	delete(seen, visit{kind: v.Kind(), ptr: v.Pointer()})
}
