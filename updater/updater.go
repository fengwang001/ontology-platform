package updater

import (
	"errors"
	"fmt"
	"sync"

	"ontology/keyring"
	"ontology/meta"
)

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrClockRollback    = errors.New("clock rollback")
	ErrVersionGap       = errors.New("version gap")
	ErrRootExpired      = errors.New("root expired")
	ErrTimestampExpired = errors.New("timestamp expired")
	ErrSnapshotExpired  = errors.New("snapshot expired")
	ErrTargetsExpired   = errors.New("targets expired")
	ErrNotReady         = errors.New("not ready")
	ErrFileNotFound     = errors.New("file not found")
	ErrLengthMismatch   = errors.New("length mismatch")
	ErrHashMismatch     = errors.New("hash mismatch")

	ErrRootSignatureInsufficient      = errors.New("root signature insufficient")
	ErrTimestampSignatureInsufficient = errors.New("timestamp signature insufficient")
	ErrSnapshotSignatureInsufficient  = errors.New("snapshot signature insufficient")
	ErrTargetsSignatureInsufficient   = errors.New("targets signature insufficient")

	ErrOldRootSignatureInsufficient = fmt.Errorf("old %w", ErrRootSignatureInsufficient)
	ErrNewRootSignatureInsufficient = fmt.Errorf("new %w", ErrRootSignatureInsufficient)

	ErrTimestampVersionRollback = errors.New("timestamp version rollback")
	ErrSnapshotVersionRollback  = errors.New("snapshot version rollback")
	ErrTargetsVersionRollback   = errors.New("targets version rollback")

	ErrSnapshotVersionMismatch = errors.New("snapshot version mismatch")
	ErrTargetsVersionMismatch  = errors.New("targets version mismatch")
)

type ChainError struct {
	Index int
	Err   error
}

func (e ChainError) Error() string {
	return fmt.Sprintf("root chain candidate %d: %v", e.Index, e.Err)
}

func (e ChainError) Unwrap() error { return e.Err }

type Updater struct {
	mu               sync.Mutex
	lastNow          int64
	root             meta.Root
	timestampVersion int
	snapshotVersion  int
	targetsVersion   int
	files            map[string]meta.FileMeta
}

func New(root meta.Root, signatures []string) (*Updater, error) {
	if err := meta.ValidateRoot(root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	rootRole := root.Roles[meta.RoleRoot]
	if !rootRole.Verify(signatures, nil) {
		return nil, ErrRootSignatureInsufficient
	}

	return &Updater{
		root:  cloneRoot(root),
		files: nil,
	}, nil
}

func (u *Updater) UpdateRoot(now int64, chain []meta.RootCandidate) error {
	if err := meta.ValidateRootChain(chain); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if now < u.lastNow {
		return ErrClockRollback
	}
	u.lastNow = now

	for index, candidate := range chain {
		current := u.root
		next := cloneRoot(candidate.Root)

		if next.Version != current.Version+1 {
			return ChainError{Index: index, Err: ErrVersionGap}
		}
		if !current.Roles[meta.RoleRoot].Verify(candidate.Signatures, nil) {
			return ChainError{Index: index, Err: ErrOldRootSignatureInsufficient}
		}
		if !next.Roles[meta.RoleRoot].Verify(candidate.Signatures, nil) {
			return ChainError{Index: index, Err: ErrNewRootSignatureInsufficient}
		}

		removedTimestampKeys := hasRemovedKey(current.Roles[meta.RoleTimestamp], next.Roles[meta.RoleTimestamp])
		removedSnapshotKeys := hasRemovedKey(current.Roles[meta.RoleSnapshot], next.Roles[meta.RoleSnapshot])
		u.root = next

		if removedTimestampKeys || removedSnapshotKeys {
			u.timestampVersion = 0
			u.snapshotVersion = 0
		}
	}

	if now >= u.root.Expires {
		return ErrRootExpired
	}
	return nil
}

func (u *Updater) Refresh(now int64, ts meta.Timestamp, snap meta.Snapshot, tgt meta.Targets) error {
	if err := meta.ValidateTimestamp(ts); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if err := meta.ValidateSnapshot(snap); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if err := meta.ValidateTargets(tgt); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if now < u.lastNow {
		return ErrClockRollback
	}
	u.lastNow = now

	if now >= u.root.Expires {
		return ErrRootExpired
	}

	if !u.root.Roles[meta.RoleTimestamp].Verify(ts.Signatures, nil) {
		return ErrTimestampSignatureInsufficient
	}
	if ts.Version < u.timestampVersion {
		return ErrTimestampVersionRollback
	}
	if now >= ts.Expires {
		return ErrTimestampExpired
	}
	if ts.Version == u.timestampVersion {
		return nil
	}
	if ts.SnapVersion < u.snapshotVersion {
		return ErrSnapshotVersionRollback
	}

	if !u.root.Roles[meta.RoleSnapshot].Verify(snap.Signatures, nil) {
		return ErrSnapshotSignatureInsufficient
	}
	if snap.Version != ts.SnapVersion {
		return ErrSnapshotVersionMismatch
	}
	if snap.TargetsVersion < u.targetsVersion {
		return ErrTargetsVersionRollback
	}
	if now >= snap.Expires {
		return ErrSnapshotExpired
	}

	if !u.root.Roles[meta.RoleTargets].Verify(tgt.Signatures, nil) {
		return ErrTargetsSignatureInsufficient
	}
	if tgt.Version != snap.TargetsVersion {
		return ErrTargetsVersionMismatch
	}
	if now >= tgt.Expires {
		return ErrTargetsExpired
	}

	u.timestampVersion = ts.Version
	u.snapshotVersion = snap.Version
	u.targetsVersion = tgt.Version
	u.files = cloneFiles(tgt.Files)

	return nil
}

func (u *Updater) Verify(path string, length int64, hash string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.targetsVersion == 0 || u.files == nil {
		return ErrNotReady
	}

	file, exists := u.files[path]
	if !exists {
		return ErrFileNotFound
	}
	if file.Length != length {
		return ErrLengthMismatch
	}
	if file.Hash != hash {
		return ErrHashMismatch
	}
	return nil
}

func hasRemovedKey(oldRole, newRole keyring.Role) bool {
	for _, keyID := range oldRole.KeyIDs() {
		if !newRole.HasKey(keyID) {
			return true
		}
	}
	return false
}

func cloneRoot(root meta.Root) meta.Root {
	roles := make(map[meta.RoleName]keyring.Role, len(root.Roles))
	for roleName, role := range root.Roles {
		roles[roleName] = role
	}
	return meta.Root{Version: root.Version, Expires: root.Expires, Roles: roles}
}

func cloneFiles(files map[string]meta.FileMeta) map[string]meta.FileMeta {
	cloned := make(map[string]meta.FileMeta, len(files))
	for path, file := range files {
		cloned[path] = file
	}
	return cloned
}
