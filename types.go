package ontology

// Object is a stored entity of some object type.
type Object struct {
	ID    string
	Type  string
	Props map[string]any
}

// Relation is a directed, typed edge between two objects.
type Relation struct {
	ID   string
	Type string
	From string
	To   string
}

func copyObject(o *Object) Object {
	props := make(map[string]any, len(o.Props))
	for k, v := range o.Props {
		props[k] = deepCopy(v)
	}
	return Object{ID: o.ID, Type: o.Type, Props: props}
}

// deepCopy copies maps and slices recursively so that declared default
// parameter values can never be polluted by a previous invocation.
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = deepCopy(val)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, val := range t {
			s[i] = deepCopy(val)
		}
		return s
	default:
		return v
	}
}
