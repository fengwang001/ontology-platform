package keyring

import (
	"fmt"
	"testing"

	"ontology/meta"
)

func TestVerifyAndLookups(t *testing.T) {
	many := make([]string, 4000)
	for index := range many {
		many[index] = fmt.Sprintf("K%04d", index)
	}
	roles := map[meta.RoleKind]meta.Role{
		meta.RoleRoot:      {KeyIDs: []string{"A", "B", "C"}, Threshold: 2},
		meta.RoleTimestamp: {KeyIDs: []string{"T"}, Threshold: 1},
	}
	largeRoles := map[meta.RoleKind]meta.Role{
		meta.RoleRoot: {KeyIDs: many, Threshold: 1},
	}

	tests := []struct {
		name       string
		ring       *Keyring
		role       meta.RoleKind
		signatures []string
		want       bool
		lookups    int
	}{
		{"threshold met with unknown", New(roles), meta.RoleRoot, []string{"A", "B", "Z"}, true, 3},
		{"duplicates count once", New(roles), meta.RoleRoot, []string{"A", "A", "Z"}, false, 2},
		{"threshold met duplicates", New(roles), meta.RoleRoot, []string{"A", "A", "B"}, true, 2},
		{"duplicate after threshold does not add lookup", New(roles), meta.RoleRoot, []string{"A", "B", "A", "Z"}, true, 3},
		{"unknown role", New(roles), meta.RoleSnapshot, []string{"A"}, false, 0},
		{"large key set bounded lookups", New(largeRoles), meta.RoleRoot, []string{"unknown", "unknown", many[3999]}, true, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ring.Verify(tt.role, tt.signatures)
			if got != tt.want || tt.ring.Lookups() != tt.lookups {
				t.Fatalf("Verify=%v lookups=%d, want %v and %d", got, tt.ring.Lookups(), tt.want, tt.lookups)
			}
		})
	}
}
