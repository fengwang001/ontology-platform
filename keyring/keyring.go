package keyring

import "ontology/meta"

type Keyring struct {
	roles      map[meta.RoleKind]map[string]struct{}
	thresholds map[meta.RoleKind]int
	lookups    int
}

func New(roles map[meta.RoleKind]meta.Role) *Keyring {
	k := &Keyring{
		roles:      make(map[meta.RoleKind]map[string]struct{}, len(roles)),
		thresholds: make(map[meta.RoleKind]int, len(roles)),
	}
	for kind, role := range roles {
		keys := make(map[string]struct{}, len(role.KeyIDs))
		for _, keyID := range role.KeyIDs {
			keys[keyID] = struct{}{}
		}
		k.roles[kind] = keys
		k.thresholds[kind] = role.Threshold
	}
	return k
}

func (k *Keyring) Verify(role meta.RoleKind, signatures []string) bool {
	k.lookups = 0
	keys, ok := k.roles[role]
	if !ok {
		return false
	}
	threshold := k.thresholds[role]
	matched := 0
	seen := make(map[string]struct{}, len(signatures))
	for _, signature := range signatures {
		if _, duplicate := seen[signature]; duplicate {
			continue
		}
		seen[signature] = struct{}{}
		k.lookups++
		if _, authorized := keys[signature]; authorized {
			matched++
		}
	}
	return matched >= threshold
}

func (k *Keyring) Lookups() int { return k.lookups }
