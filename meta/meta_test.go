package meta

import (
	"errors"
	"testing"

	"ontology/keyring"
)

func TestValidation(t *testing.T) {
	validTS := Timestamp{Version: 1, Expires: 10, SnapVersion: 1}
	validSnap := Snapshot{Version: 1, Expires: 10, TargetsVersion: 1}
	validTgt := Targets{Version: 1, Expires: 10, Files: map[string]FileMeta{}}

	tests := []struct {
		name  string
		check func() error
		want  error
	}{
		{name: "root", check: func() error { return ValidateRoot(validRoot()) }},
		{name: "root bad version", check: func() error { root := validRoot(); root.Version = 0; return ValidateRoot(root) }, want: ErrInvalidArgument},
		{name: "root missing role", check: func() error { root := validRoot(); delete(root.Roles, RoleTargets); return ValidateRoot(root) }, want: ErrInvalidArgument},
		{name: "root chain", check: func() error { return ValidateRootChain([]RootCandidate{{Root: validRoot()}}) }},
		{name: "root chain empty", check: func() error { return ValidateRootChain(nil) }, want: ErrInvalidArgument},
		{name: "root chain too long", check: func() error { return ValidateRootChain(make([]RootCandidate, 33)) }, want: ErrInvalidArgument},
		{name: "timestamp", check: func() error { return ValidateTimestamp(validTS) }},
		{name: "timestamp bad snap", check: func() error { v := validTS; v.SnapVersion = 0; return ValidateTimestamp(v) }, want: ErrInvalidArgument},
		{name: "snapshot", check: func() error { return ValidateSnapshot(validSnap) }},
		{name: "snapshot bad targets", check: func() error { v := validSnap; v.TargetsVersion = 0; return ValidateSnapshot(v) }, want: ErrInvalidArgument},
		{name: "targets", check: func() error { return ValidateTargets(validTgt) }},
		{name: "targets missing files", check: func() error { v := validTgt; v.Files = nil; return ValidateTargets(v) }, want: ErrInvalidArgument},
		{name: "targets bad path", check: func() error {
			v := validTgt
			v.Files = map[string]FileMeta{"": {Length: 1, Hash: "h"}}
			return ValidateTargets(v)
		}, want: ErrInvalidArgument},
		{name: "targets negative length", check: func() error {
			v := validTgt
			v.Files = map[string]FileMeta{"p": {Length: -1, Hash: "h"}}
			return ValidateTargets(v)
		}, want: ErrInvalidArgument},
		{name: "targets empty hash", check: func() error {
			v := validTgt
			v.Files = map[string]FileMeta{"p": {Length: 1, Hash: ""}}
			return ValidateTargets(v)
		}, want: ErrInvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.check(); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func validRoot() Root {
	roles := make(map[RoleName]keyring.Role, 4)
	for _, name := range []RoleName{RoleRoot, RoleTimestamp, RoleSnapshot, RoleTargets} {
		role, err := keyring.NewRole([]string{string(name)}, 1)
		if err != nil {
			panic(err)
		}
		roles[name] = role
	}
	return Root{Version: 1, Expires: 100, Roles: roles}
}
