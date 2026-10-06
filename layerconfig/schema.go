package layerconfig

import "sync"

// ValueType enumerates the legal value types.
type ValueType int

const (
	TypeString ValueType = iota + 1
	TypeInt
	TypeBool
	TypeStringList
)

// MergeMode enumerates key merge policies.
type MergeMode int

const (
	// MergeOverride: narrow value replaces the broad value.
	MergeOverride MergeMode = iota + 1
	// MergeAppend: lists concatenate broad-to-narrow, keeping first positions.
	MergeAppend
)

// Schema is the registered pattern of a key.
type Schema struct {
	Key      string
	Type     ValueType
	Required bool
	Min      int
	Max      int
	Merge    MergeMode
}

// registry holds the latest schema of each key. Schemas are not versioned.
type registry struct {
	mu      sync.RWMutex
	schemas map[string]Schema
}

func newRegistry() *registry {
	return &registry{schemas: map[string]Schema{}}
}

// validateSchema checks the pattern itself: key present, type known, merge
// mode compatible with the type and, for ints, a non-empty range.
func (r *registry) validateSchema(s Schema) error {
	if s.Key == "" {
		return errInvalid("schema key must not be empty")
	}
	switch s.Type {
	case TypeString, TypeInt, TypeBool, TypeStringList:
	default:
		return errInvalid("key %q: unknown value type %d", s.Key, s.Type)
	}
	switch s.Merge {
	case MergeOverride:
	case MergeAppend:
		if s.Type != TypeStringList {
			return errInvalid("key %q: append merge is only valid for string lists", s.Key)
		}
	default:
		return errInvalid("key %q: unknown merge mode %d", s.Key, s.Merge)
	}
	if s.Type == TypeInt && s.Min > s.Max {
		return errInvalid("key %q: int range min (%d) must be <= max (%d)", s.Key, s.Min, s.Max)
	}
	return nil
}

// register creates or replaces the latest schema of a key.
func (r *registry) register(s Schema) error {
	if err := r.validateSchema(s); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := s
	r.schemas[s.Key] = cp
	return nil
}

// get returns the latest schema of a key.
func (r *registry) get(key string) (Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.schemas[key]
	return s, ok
}

// checkValue validates a value against its schema (type and int range).
func checkValue(s Schema, v Value) error {
	switch s.Type {
	case TypeString:
		// Any string (including "") is accepted; emptiness is distinguished
		// from unset by the entry presence, not by the value.
		return nil
	case TypeBool:
		return nil
	case TypeStringList:
		// A nil list is normalized; empty list is a legitimate value.
		return nil
	case TypeInt:
		if v.Int < s.Min || v.Int > s.Max {
			return errType("key %q: int %d out of range [%d, %d]", s.Key, v.Int, s.Min, s.Max)
		}
		return nil
	default:
		return errType("key %q: unknown schema type", s.Key)
	}
}

// valueTypeConsistent reports whether the value payload could plausibly carry
// the schema type. Go's Value struct has one field per type and no
// discriminator, so every payload is representable; this hook is retained for
// explicit documentation and future tightening.
func valueTypeConsistent(s Schema, v Value) bool {
	_ = v
	switch s.Type {
	case TypeString, TypeInt, TypeBool, TypeStringList:
		return true
	default:
		return false
	}
}
