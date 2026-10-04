package meta

import (
	"errors"
	"testing"
)

func validRole(keys ...string) Role {
	return Role{KeyIDs: keys, Threshold: 1}
}

func validRoot() Root {
	return Root{
		Version: 1,
		Expires: 100,
		Roles: map[RoleKind]Role{
			RoleRoot:      validRole("A", "B", "C"),
			RoleTimestamp: validRole("T"),
			RoleSnapshot:  validRole("S"),
			RoleTargets:   validRole("G"),
		},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		check func() error
		want  bool
	}{
		{"root valid", func() error { return ValidateRoot(validRoot()) }, true},
		{"root version zero", func() error { root := validRoot(); root.Version = 0; return ValidateRoot(root) }, false},
		{"root missing role", func() error {
			root := validRoot()
			delete(root.Roles, RoleTargets)
			return ValidateRoot(root)
		}, false},
		{"root duplicate key", func() error {
			root := validRoot()
			role := root.Roles[RoleRoot]
			role.KeyIDs = []string{"A", "A"}
			root.Roles[RoleRoot] = role
			return ValidateRoot(root)
		}, false},
		{"root threshold zero", func() error {
			root := validRoot()
			role := root.Roles[RoleRoot]
			role.Threshold = 0
			root.Roles[RoleRoot] = role
			return ValidateRoot(root)
		}, false},
		{"root threshold too high", func() error {
			root := validRoot()
			role := root.Roles[RoleRoot]
			role.Threshold = 4
			root.Roles[RoleRoot] = role
			return ValidateRoot(root)
		}, false},
		{"timestamp valid", func() error { return ValidateTimestamp(Timestamp{Version: 1, SnapVersion: 1}) }, true},
		{"timestamp zero snap", func() error { return ValidateTimestamp(Timestamp{Version: 1}) }, false},
		{"snapshot valid", func() error { return ValidateSnapshot(Snapshot{Version: 1, TargetsVersion: 1}) }, true},
		{"snapshot zero targets", func() error { return ValidateSnapshot(Snapshot{Version: 1}) }, false},
		{"targets valid", func() error {
			return ValidateTargets(Targets{Version: 1, Files: map[string]File{"p": {Length: 1, Hash: "h"}}})
		}, true},
		{"targets bad length", func() error {
			return ValidateTargets(Targets{Version: 1, Files: map[string]File{"p": {Length: -1, Hash: "h"}}})
		}, false},
		{"targets empty hash", func() error {
			return ValidateTargets(Targets{Version: 1, Files: map[string]File{"p": {Length: 1}}})
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check()
			if tt.want && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.want && !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("want invalid argument, got %v", err)
			}
		})
	}
}
