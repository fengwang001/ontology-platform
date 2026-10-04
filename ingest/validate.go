package ingest

import (
	"fmt"

	"ontology/mapping"
)

// ErrInvalidArgument covers empty/oversized ids, malformed documents and
// field paths. Such errors are reported before the mapping is touched.
var ErrInvalidArgument = mapping.ErrInvalidArgument

func validateID(id string) error {
	n := len(id)
	if n == 0 || n > 512 {
		return fmt.Errorf("%w: id must be 1..512 bytes, got %d", ErrInvalidArgument, n)
	}
	return nil
}

// validateValue checks the whole document structurally before any mapping
// change. Object nesting depth is counted from the root document (1..8).
func validateValue(v any, depth int) error {
	switch x := v.(type) {
	case nil, bool, int64, float64, string:
		return nil
	case map[string]any:
		if depth > 8 {
			return fmt.Errorf("%w: object nesting exceeds depth 8", ErrInvalidArgument)
		}
		for k, child := range x {
			if err := mapping.ValidateKey(k); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
			}
			if err := validateValue(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for _, el := range x {
			switch el.(type) {
			case nil, bool, int64, float64, string:
			default:
				return fmt.Errorf("%w: array element must be scalar or nil: %T", ErrInvalidArgument, el)
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported value type %T", ErrInvalidArgument, v)
	}
}
