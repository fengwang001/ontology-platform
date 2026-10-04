package meta

import "errors"

var ErrInvalidArgument = errors.New("invalid metadata argument")

type RoleKind string

const (
	RoleRoot      RoleKind = "root"
	RoleTimestamp RoleKind = "timestamp"
	RoleSnapshot  RoleKind = "snapshot"
	RoleTargets   RoleKind = "targets"
)

type Role struct {
	KeyIDs    []string
	Threshold int
}

type Root struct {
	Version int
	Expires int64
	Roles   map[RoleKind]Role
}

type Timestamp struct {
	Version     int
	Expires     int64
	SnapVersion int
}

type Snapshot struct {
	Version        int
	Expires        int64
	TargetsVersion int
}

type File struct {
	Length int64
	Hash   string
}

type Targets struct {
	Version int
	Expires int64
	Files   map[string]File
}

func ValidateRoot(root Root) error {
	if root.Version < 1 || root.Roles == nil {
		return ErrInvalidArgument
	}
	for _, role := range []RoleKind{RoleRoot, RoleTimestamp, RoleSnapshot, RoleTargets} {
		if err := ValidateRole(root.Roles[role]); err != nil {
			return err
		}
	}
	return nil
}

func ValidateRole(role Role) error {
	if len(role.KeyIDs) < 1 || len(role.KeyIDs) > 4096 || role.Threshold < 1 || role.Threshold > len(role.KeyIDs) {
		return ErrInvalidArgument
	}
	seen := make(map[string]struct{}, len(role.KeyIDs))
	for _, keyID := range role.KeyIDs {
		if keyID == "" {
			return ErrInvalidArgument
		}
		if _, ok := seen[keyID]; ok {
			return ErrInvalidArgument
		}
		seen[keyID] = struct{}{}
	}
	return nil
}

func ValidateTimestamp(ts Timestamp) error {
	if ts.Version < 1 || ts.SnapVersion < 1 {
		return ErrInvalidArgument
	}
	return nil
}

func ValidateSnapshot(snap Snapshot) error {
	if snap.Version < 1 || snap.TargetsVersion < 1 {
		return ErrInvalidArgument
	}
	return nil
}

func ValidateTargets(tgt Targets) error {
	if tgt.Version < 1 {
		return ErrInvalidArgument
	}
	for path, file := range tgt.Files {
		if path == "" || file.Length < 0 || file.Hash == "" {
			return ErrInvalidArgument
		}
	}
	return nil
}
