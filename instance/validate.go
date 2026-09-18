package instance

import "fmt"

func (s *Store) validate(objectType string, key PrimaryKey, properties map[string]any) error {
	spec, ok := s.types[objectType]
	if !ok {
		return nil
	}

	known := make(map[string]PropertySpec, len(spec.Properties))
	for _, p := range spec.Properties {
		known[p.Name] = p
	}

	for name, value := range properties {
		p, ok := known[name]
		if !ok {
			return &Error{
				Kind:       KindConstraint,
				ObjectType: objectType,
				Key:        key,
				Reason:     fmt.Sprintf("unknown property %q", name),
			}
		}
		if !valueMatches(value, p.Kind) {
			return &Error{
				Kind:       KindConstraint,
				ObjectType: objectType,
				Key:        key,
				Reason:     fmt.Sprintf("property %q expects %s", name, kindName(p.Kind)),
			}
		}
	}

	for _, p := range spec.Properties {
		if p.Required {
			if _, ok := properties[p.Name]; !ok {
				return &Error{
					Kind:       KindConstraint,
					ObjectType: objectType,
					Key:        key,
					Reason:     fmt.Sprintf("missing required property %q", p.Name),
				}
			}
		}
	}
	return nil
}

func valueMatches(value any, kind ValueKind) bool {
	switch kind {
	case KindString:
		_, ok := value.(string)
		return ok
	case KindInt:
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		}
		return false
	case KindFloat:
		switch value.(type) {
		case float32, float64:
			return true
		}
		return false
	case KindBool:
		_, ok := value.(bool)
		return ok
	default:
		return false
	}
}

func kindName(k ValueKind) string {
	switch k {
	case KindString:
		return "string"
	case KindInt:
		return "integer"
	case KindFloat:
		return "float"
	case KindBool:
		return "bool"
	default:
		return "unknown"
	}
}
