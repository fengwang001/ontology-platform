package meta

import (
	"errors"

	"ontology/keyring"
)

var ErrInvalidArgument = errors.New("invalid argument")

const (
	minRootChainLength = 1
	maxRootChainLength = 32
)

type RoleName string

const (
	RoleRoot      RoleName = "root"
	RoleTimestamp RoleName = "timestamp"
	RoleSnapshot  RoleName = "snapshot"
	RoleTargets   RoleName = "targets"
)

type Root struct {
	Version int
	Expires int64
	Roles   map[RoleName]keyring.Role
}

type RootCandidate struct {
	Root       Root
	Signatures []string
}

type Timestamp struct {
	Version     int
	Expires     int64
	SnapVersion int
	Signatures  []string
}

type Snapshot struct {
	Version        int
	Expires        int64
	TargetsVersion int
	Signatures     []string
}

type FileMeta struct {
	Length int64
	Hash   string
}

type Targets struct {
	Version    int
	Expires    int64
	Files      map[string]FileMeta
	Signatures []string
}

func ValidateRoot(root Root) error {
	if root.Version < 1 || root.Roles == nil {
		return ErrInvalidArgument
	}

	requiredRoles := []RoleName{RoleRoot, RoleTimestamp, RoleSnapshot, RoleTargets}
	for _, roleName := range requiredRoles {
		role, exists := root.Roles[roleName]
		if !exists || role.Threshold() < 1 {
			return ErrInvalidArgument
		}
	}

	return nil
}

func ValidateRootChain(chain []RootCandidate) error {
	if len(chain) < minRootChainLength || len(chain) > maxRootChainLength {
		return ErrInvalidArgument
	}

	for _, candidate := range chain {
		if err := ValidateRoot(candidate.Root); err != nil {
			return err
		}
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
	if tgt.Version < 1 || tgt.Files == nil {
		return ErrInvalidArgument
	}

	for path, file := range tgt.Files {
		if path == "" || file.Length < 0 || file.Hash == "" {
			return ErrInvalidArgument
		}
	}

	return nil
}
