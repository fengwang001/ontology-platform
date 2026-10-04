package keyring

import "errors"

var ErrInvalidArgument = errors.New("invalid argument")

type Role struct {
	keyIDs    map[string]struct{}
	threshold int
}

const (
	minKeyIDs = 1
	maxKeyIDs = 4096
)

func NewRole(keyIDs []string, threshold int) (Role, error) {
	if len(keyIDs) < minKeyIDs || len(keyIDs) > maxKeyIDs {
		return Role{}, ErrInvalidArgument
	}

	keys := make(map[string]struct{}, len(keyIDs))
	for _, keyID := range keyIDs {
		if _, exists := keys[keyID]; exists {
			return Role{}, ErrInvalidArgument
		}
		keys[keyID] = struct{}{}
	}

	if threshold < 1 || threshold > len(keyIDs) {
		return Role{}, ErrInvalidArgument
	}

	return Role{keyIDs: keys, threshold: threshold}, nil
}

func (r Role) Threshold() int { return r.threshold }

func (r Role) KeyIDs() []string {
	keys := make([]string, 0, len(r.keyIDs))
	for keyID := range r.keyIDs {
		keys = append(keys, keyID)
	}
	return keys
}

func (r Role) HasKey(keyID string) bool {
	_, exists := r.keyIDs[keyID]
	return exists
}

func (r Role) Verify(signatures []string, lookups *int) bool {
	if r.threshold < 1 {
		return false
	}

	seen := make(map[string]struct{}, len(signatures))
	weight := 0
	for _, keyID := range signatures {
		if _, duplicate := seen[keyID]; duplicate {
			continue
		}
		seen[keyID] = struct{}{}

		if lookups != nil {
			*lookups++
		}
		if r.HasKey(keyID) {
			weight++
		}
	}

	return weight >= r.threshold
}
