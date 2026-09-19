package predicate

import "reflect"

// goTypeName returns a stable, human-readable name for a literal or attribute
// value. "<missing>" denotes an absent attribute and "<nil>" an untyped nil.
func goTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "<nil>"
	case int64:
		return "int64"
	case float64:
		return "float64"
	case string:
		return "string"
	case bool:
		return "bool"
	default:
		t := reflect.TypeOf(v)
		if t == nil {
			return "<nil>"
		}
		return t.String()
	}
}
