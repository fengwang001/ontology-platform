package updater

import (
	"errors"
	"fmt"
	"sync"

	"ontology/keyring"
	"ontology/meta"
)

var (
	ErrInvalidArgument        = meta.ErrInvalidArgument
	ErrClockRollback          = errors.New("clock rollback")
	ErrRootExpired            = errors.New("root metadata expired")
	ErrExpired                = errors.New("metadata expired")
	ErrInsufficientSignatures = errors.New("insufficient signatures")
	ErrOldRootSignatures      = fmt.Errorf("%w by old root", ErrInsufficientSignatures)
	ErrNewRootSignatures      = fmt.Errorf("%w by new root", ErrInsufficientSignatures)
	ErrRollback               = errors.New("metadata version rollback")
	ErrVersionMismatch        = errors.New("metadata version mismatch")
	ErrNotReady               = errors.New("targets metadata not ready")
	ErrNotFound               = errors.New("target file not found")
	ErrLengthMismatch         = errors.New("target file length mismatch")
	ErrHashMismatch           = errors.New("target file hash mismatch")
)

type SignedRoot struct {
	Root       meta.Root
	Signatures []string
}

type roleError struct {
	role meta.RoleKind
	err  error
}

func (e roleError) Error() string {
	return fmt.Sprintf("%s: %s", e.role, e.err)
}

func (e roleError) Unwrap() error { return e.err }

type Updater struct {
	mu          sync.Mutex
	root        meta.Root
	keys        *keyring.Keyring
	lastClock   int64
	tsVersion   int
	snapVersion int
	tgtVersion  int
	files       map[string]meta.File
	rootChecks  int
}

func New(root meta.Root, signatures []string) (*Updater, error) {
	if err := meta.ValidateRoot(root); err != nil {
		return nil, err
	}
	keys := keyring.New(root.Roles)
	if !keys.Verify(meta.RoleRoot, signatures) {
		return nil, roleError{role: meta.RoleRoot, err: ErrInsufficientSignatures}
	}
	return &Updater{
		root:      cloneRoot(root),
		keys:      keyring.New(root.Roles),
		lastClock: -1,
	}, nil
}

func (u *Updater) UpdateRoot(now int64, chain []SignedRoot) (int, error) {
	if len(chain) < 1 || len(chain) > 32 {
		return -1, ErrInvalidArgument
	}
	for _, candidate := range chain {
		if err := meta.ValidateRoot(candidate.Root); err != nil {
			return -1, err
		}
	}
	if now < 0 || now > 1_000_000_000_000 {
		return -1, ErrInvalidArgument
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if now < u.lastClock {
		return -1, ErrClockRollback
	}
	u.lastClock = now

	for index, candidate := range chain {
		u.rootChecks = 0
		if candidate.Root.Version != u.root.Version+1 {
			return index, roleError{role: meta.RoleRoot, err: ErrVersionMismatch}
		}
		u.rootChecks++
		if !u.keys.Verify(meta.RoleRoot, candidate.Signatures) {
			return index, roleError{role: meta.RoleRoot, err: ErrOldRootSignatures}
		}
		candidateKeys := keyring.New(candidate.Root.Roles)
		u.rootChecks++
		if !candidateKeys.Verify(meta.RoleRoot, candidate.Signatures) {
			return index, roleError{role: meta.RoleRoot, err: ErrNewRootSignatures}
		}

		clearTimestampAndSnapshot := removedKey(u.root.Roles[meta.RoleTimestamp], candidate.Root.Roles[meta.RoleTimestamp]) ||
			removedKey(u.root.Roles[meta.RoleSnapshot], candidate.Root.Roles[meta.RoleSnapshot])
		u.root = cloneRoot(candidate.Root)
		u.keys = candidateKeys
		if clearTimestampAndSnapshot {
			u.tsVersion = 0
			u.snapVersion = 0
		}
	}

	if now >= u.root.Expires {
		return len(chain) - 1, ErrRootExpired
	}
	return len(chain) - 1, nil
}

func (u *Updater) Refresh(now int64, ts meta.Timestamp, tsSigs []string, snap meta.Snapshot, snapSigs []string, tgt meta.Targets, tgtSigs []string) error {
	if err := meta.ValidateTimestamp(ts); err != nil {
		return err
	}
	if err := meta.ValidateSnapshot(snap); err != nil {
		return err
	}
	if err := meta.ValidateTargets(tgt); err != nil {
		return err
	}
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if now < u.lastClock {
		return ErrClockRollback
	}
	if now >= u.root.Expires {
		u.lastClock = now
		return ErrRootExpired
	}
	u.lastClock = now

	if !u.keys.Verify(meta.RoleTimestamp, tsSigs) {
		return roleError{role: meta.RoleTimestamp, err: ErrInsufficientSignatures}
	}
	if ts.Version < u.tsVersion {
		return roleError{role: meta.RoleTimestamp, err: ErrRollback}
	}
	if now >= ts.Expires {
		return roleError{role: meta.RoleTimestamp, err: ErrExpired}
	}
	if ts.Version == u.tsVersion {
		return nil
	}
	if ts.SnapVersion < u.snapVersion {
		return roleError{role: meta.RoleSnapshot, err: ErrRollback}
	}
	if !u.keys.Verify(meta.RoleSnapshot, snapSigs) {
		return roleError{role: meta.RoleSnapshot, err: ErrInsufficientSignatures}
	}
	if snap.Version != ts.SnapVersion {
		return roleError{role: meta.RoleSnapshot, err: ErrVersionMismatch}
	}
	if snap.TargetsVersion < u.tgtVersion {
		return roleError{role: meta.RoleTargets, err: ErrRollback}
	}
	if now >= snap.Expires {
		return roleError{role: meta.RoleSnapshot, err: ErrExpired}
	}
	if !u.keys.Verify(meta.RoleTargets, tgtSigs) {
		return roleError{role: meta.RoleTargets, err: ErrInsufficientSignatures}
	}
	if tgt.Version != snap.TargetsVersion {
		return roleError{role: meta.RoleTargets, err: ErrVersionMismatch}
	}
	if now >= tgt.Expires {
		return roleError{role: meta.RoleTargets, err: ErrExpired}
	}

	u.tsVersion = ts.Version
	u.snapVersion = snap.Version
	u.tgtVersion = tgt.Version
	u.files = cloneFiles(tgt.Files)
	return nil
}

func (u *Updater) Verify(path string, length int64, hash string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.files == nil {
		return ErrNotReady
	}
	file, ok := u.files[path]
	if !ok {
		return ErrNotFound
	}
	if file.Length != length {
		return ErrLengthMismatch
	}
	if file.Hash != hash {
		return ErrHashMismatch
	}
	return nil
}

func removedKey(oldRole, newRole meta.Role) bool {
	newKeys := make(map[string]struct{}, len(newRole.KeyIDs))
	for _, keyID := range newRole.KeyIDs {
		newKeys[keyID] = struct{}{}
	}
	for _, keyID := range oldRole.KeyIDs {
		if _, ok := newKeys[keyID]; !ok {
			return true
		}
	}
	return false
}

func cloneRoot(root meta.Root) meta.Root {
	clone := root
	clone.Roles = make(map[meta.RoleKind]meta.Role, len(root.Roles))
	for kind, role := range root.Roles {
		role.KeyIDs = append([]string(nil), role.KeyIDs...)
		clone.Roles[kind] = role
	}
	return clone
}

func cloneFiles(files map[string]meta.File) map[string]meta.File {
	clone := make(map[string]meta.File, len(files))
	for path, file := range files {
		clone[path] = file
	}
	return clone
}
