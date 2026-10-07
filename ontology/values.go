package ontology

import "fmt"

// keyEmpty reports whether a primary/group key value is the empty key.
func keyEmpty(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

// groupKey renders an attribute value into the canonical string group key.
func groupKey(v any) (string, bool) {
	if keyEmpty(v) {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, true
	case int:
		return fmt.Sprintf("%d", t), true
	case int64:
		return fmt.Sprintf("%d", t), true
	case float64:
		return fmt.Sprintf("%v", t), true
	case float32:
		return fmt.Sprintf("%v", t), true
	default:
		return fmt.Sprintf("%v", t), true
	}
}

// numberValue coerces a numeric attribute value to float64.
func numberValue(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case float64:
		return t, true
	case float32:
		return float64(t), true
	default:
		return 0, false
	}
}

// typeMatches checks an attribute value against its declared type.
func typeMatches(spec AttrSpec, v any) bool {
	switch spec.Type {
	case AttrInt:
		_, ok := v.(int)
		return ok
	case AttrFloat:
		_, ok := v.(float64)
		return ok
	case AttrString:
		_, ok := v.(string)
		return ok
	default:
		return false
	}
}

// typeName renders the Go value kind for error detail.
func typeName(v any) string {
	switch v.(type) {
	case int:
		return "int"
	case float64:
		return "float"
	case string:
		return "string"
	case nil:
		return "nil"
	default:
		return fmt.Sprintf("%T", v)
	}
}
