package updater

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/meta"
)

type modelRole struct {
	keys      []string
	threshold int
}

type modelState struct {
	clock       int64
	rootVersion int
	expires     int64
	roles       map[meta.RoleKind]modelRole
	tsVersion   int
	snapVersion int
	tgtVersion  int
	files       map[string]meta.File
}

type modelRootInput struct {
	version int
	expires int64
	roles   map[meta.RoleKind]modelRole
	sigs    []string
}

type refreshInput struct {
	now      int64
	ts       meta.Timestamp
	tsSigs   []string
	snap     meta.Snapshot
	snapSigs []string
	tgt      meta.Targets
	tgtSigs  []string
}

func newModel() *modelState {
	return &modelState{
		clock:       -1,
		rootVersion: 1,
		expires:     1000,
		roles: map[meta.RoleKind]modelRole{
			meta.RoleRoot:      {[]string{"A", "B", "C"}, 2},
			meta.RoleTimestamp: {[]string{"T"}, 1},
			meta.RoleSnapshot:  {[]string{"S"}, 1},
			meta.RoleTargets:   {[]string{"G"}, 1},
		},
	}
}

func modelVerify(role modelRole, sigs []string) (bool, int) {
	seen := map[string]struct{}{}
	keys := map[string]struct{}{}
	for _, key := range role.keys {
		keys[key] = struct{}{}
	}
	matched, lookups := 0, 0
	for _, sig := range sigs {
		if _, ok := seen[sig]; ok {
			continue
		}
		seen[sig] = struct{}{}
		lookups++
		if _, ok := keys[sig]; ok {
			matched++
		}
	}
	return matched >= role.threshold, lookups
}

func removedModelKey(oldRole, newRole modelRole) bool {
	keys := map[string]struct{}{}
	for _, key := range newRole.keys {
		keys[key] = struct{}{}
	}
	for _, key := range oldRole.keys {
		if _, ok := keys[key]; !ok {
			return true
		}
	}
	return false
}

func uniqueStrings(r *rand.Rand, prefix string, count int) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, count)
	for len(out) < count {
		key := fmt.Sprintf("%s%d-%d", prefix, len(out), r.Intn(1000000))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func randomSigs(r *rand.Rand, pools ...[]string) []string {
	var all []string
	for _, pool := range pools {
		all = append(all, pool...)
	}
	all = append(all, "unknown-1", "unknown-2")
	out := make([]string, r.Intn(6))
	for i := range out {
		out[i] = all[r.Intn(len(all))]
	}
	return out
}

func makeModelRoot(r *rand.Rand, m *modelState, forceVersion int) modelRootInput {
	oldRoot := m.roles[meta.RoleRoot]
	oldKeys := append([]string{}, oldRoot.keys...)
	r.Shuffle(len(oldKeys), func(i, j int) { oldKeys[i], oldKeys[j] = oldKeys[j], oldKeys[i] })
	keepCount := r.Intn(len(oldKeys) + 1)
	rootKeys := append(append([]string{}, oldKeys[:keepCount]...), uniqueStrings(r, "new-root-", 1+r.Intn(4))...)

	roles := map[meta.RoleKind]modelRole{}
	for kind, role := range m.roles {
		roles[kind] = modelRole{append([]string{}, role.keys...), role.threshold}
	}
	if r.Intn(2) == 0 {
		roles[meta.RoleTimestamp] = modelRole{uniqueStrings(r, "ts-", 1+r.Intn(3)), 1}
	} else if r.Intn(2) == 0 {
		role := roles[meta.RoleTimestamp]
		role.keys = append(role.keys, uniqueStrings(r, "added-ts-", 1)...)
		roles[meta.RoleTimestamp] = role
	}
	if r.Intn(2) == 0 {
		roles[meta.RoleSnapshot] = modelRole{uniqueStrings(r, "snap-", 1+r.Intn(3)), 1}
	} else if r.Intn(2) == 0 {
		role := roles[meta.RoleSnapshot]
		role.keys = append(role.keys, uniqueStrings(r, "added-snap-", 1)...)
		roles[meta.RoleSnapshot] = role
	}
	roles[meta.RoleRoot] = modelRole{rootKeys, 1 + r.Intn(len(rootKeys))}

	return modelRootInput{
		version: forceVersion,
		expires: int64(r.Intn(1200)),
		roles:   roles,
		sigs:    randomSigs(r, m.roles[meta.RoleRoot].keys, roles[meta.RoleRoot].keys),
	}
}

func makeRefresh(r *rand.Rand, m *modelState) refreshInput {
	tsVersion := m.tsVersion + 1
	if m.tsVersion > 0 && r.Intn(5) == 0 {
		tsVersion = m.tsVersion - 1
	}
	snapDeclared := m.snapVersion + 1
	if m.snapVersion > 1 && r.Intn(3) == 0 {
		snapDeclared = m.snapVersion - 1
	}
	snapVersion := snapDeclared
	if r.Intn(5) == 0 {
		snapVersion++
	}
	targetsDeclared := m.tgtVersion + 1
	if m.tgtVersion > 1 && r.Intn(4) == 0 {
		targetsDeclared = m.tgtVersion - 1
	}
	targetsVersion := targetsDeclared
	if r.Intn(6) == 0 {
		targetsVersion++
	}
	files := map[string]meta.File{}
	if r.Intn(3) > 0 {
		files["p"] = meta.File{Length: int64(r.Intn(3)), Hash: fmt.Sprintf("h%d", r.Intn(3))}
	}
	return refreshInput{
		now:      m.clock + int64(r.Intn(30)),
		ts:       meta.Timestamp{Version: tsVersion, Expires: int64(r.Intn(1200)), SnapVersion: snapDeclared},
		tsSigs:   randomSigs(r, m.roles[meta.RoleTimestamp].keys),
		snap:     meta.Snapshot{Version: snapVersion, Expires: int64(r.Intn(1200)), TargetsVersion: targetsDeclared},
		snapSigs: randomSigs(r, m.roles[meta.RoleSnapshot].keys),
		tgt:      meta.Targets{Version: targetsVersion, Expires: int64(r.Intn(1200)), Files: files},
		tgtSigs:  randomSigs(r, m.roles[meta.RoleTargets].keys),
	}
}

func (m *modelState) updateRoot(now int64, in modelRootInput) (error, string) {
	if now < m.clock {
		return ErrClockRollback, "clock is older than accepted clock"
	}
	m.clock = now
	if in.version != m.rootVersion+1 {
		return ErrVersionMismatch, "candidate version is not current root version plus one"
	}
	if ok, lookups := modelVerify(m.roles[meta.RoleRoot], in.sigs); !ok {
		return ErrOldRootSignatures, fmt.Sprintf("old-root threshold failed after %d deduplicated lookups", lookups)
	}
	if ok, lookups := modelVerify(in.roles[meta.RoleRoot], in.sigs); !ok {
		return ErrNewRootSignatures, fmt.Sprintf("new-root threshold failed after %d deduplicated lookups", lookups)
	}
	removed := removedModelKey(m.roles[meta.RoleTimestamp], in.roles[meta.RoleTimestamp]) ||
		removedModelKey(m.roles[meta.RoleSnapshot], in.roles[meta.RoleSnapshot])
	m.rootVersion = in.version
	m.expires = in.expires
	m.roles = in.roles
	if removed {
		m.tsVersion = 0
		m.snapVersion = 0
	}
	if now >= m.expires {
		return ErrRootExpired, "root accepted but final root is expired at or after now"
	}
	return nil, fmt.Sprintf("root accepted; timestamp-or-snapshot key removed=%v", removed)
}

func (m *modelState) refresh(in refreshInput) (error, string) {
	if in.now < m.clock {
		return ErrClockRollback, "refresh clock is older than accepted clock"
	}
	if in.now >= m.expires {
		m.clock = in.now
		return ErrRootExpired, "trusted root is expired"
	}
	m.clock = in.now
	if ok, _ := modelVerify(m.roles[meta.RoleTimestamp], in.tsSigs); !ok {
		return roleError{meta.RoleTimestamp, ErrInsufficientSignatures}, "timestamp threshold failed"
	}
	if in.ts.Version < m.tsVersion {
		return roleError{meta.RoleTimestamp, ErrRollback}, "timestamp version decreased"
	}
	if in.now >= in.ts.Expires {
		return roleError{meta.RoleTimestamp, ErrExpired}, "timestamp expired at or before now"
	}
	if in.ts.Version == m.tsVersion {
		return nil, "timestamp version unchanged; snapshot and targets are not consulted"
	}
	if in.ts.SnapVersion < m.snapVersion {
		return roleError{meta.RoleSnapshot, ErrRollback}, "timestamp declares an older snapshot version"
	}
	if ok, _ := modelVerify(m.roles[meta.RoleSnapshot], in.snapSigs); !ok {
		return roleError{meta.RoleSnapshot, ErrInsufficientSignatures}, "snapshot threshold failed"
	}
	if in.snap.Version != in.ts.SnapVersion {
		return roleError{meta.RoleSnapshot, ErrVersionMismatch}, "snapshot version differs from timestamp"
	}
	if in.snap.TargetsVersion < m.tgtVersion {
		return roleError{meta.RoleTargets, ErrRollback}, "snapshot declares an older targets version"
	}
	if in.now >= in.snap.Expires {
		return roleError{meta.RoleSnapshot, ErrExpired}, "snapshot expired at or before now"
	}
	if ok, _ := modelVerify(m.roles[meta.RoleTargets], in.tgtSigs); !ok {
		return roleError{meta.RoleTargets, ErrInsufficientSignatures}, "targets threshold failed"
	}
	if in.tgt.Version != in.snap.TargetsVersion {
		return roleError{meta.RoleTargets, ErrVersionMismatch}, "targets version differs from snapshot"
	}
	if in.now >= in.tgt.Expires {
		return roleError{meta.RoleTargets, ErrExpired}, "targets expired at or before now"
	}
	m.tsVersion = in.ts.Version
	m.snapVersion = in.snap.Version
	m.tgtVersion = in.tgt.Version
	m.files = in.tgt.Files
	return nil, "timestamp, snapshot and targets committed atomically"
}

func modelRootFromInput(in modelRootInput) meta.Root {
	roles := map[meta.RoleKind]meta.Role{}
	for kind, role := range in.roles {
		roles[kind] = meta.Role{KeyIDs: append([]string{}, role.keys...), Threshold: role.threshold}
	}
	return meta.Root{Version: in.version, Expires: in.expires, Roles: roles}
}

func assertModelState(t *testing.T, iteration int, u *Updater, m *modelState) {
	t.Helper()
	rootKeys := map[meta.RoleKind][]string{}
	for kind, role := range u.root.Roles {
		rootKeys[kind] = append([]string{}, role.KeyIDs...)
	}
	modelKeys := map[meta.RoleKind][]string{}
	for kind, role := range m.roles {
		modelKeys[kind] = append([]string{}, role.keys...)
	}
	if u.root.Version != m.rootVersion || u.root.Expires != m.expires ||
		u.tsVersion != m.tsVersion || u.snapVersion != m.snapVersion ||
		u.tgtVersion != m.tgtVersion || u.lastClock != m.clock ||
		!reflectMaps(rootKeys, modelKeys) || !reflect.DeepEqual(u.files, m.files) {
		t.Fatalf("iter=%d state mismatch impl={root:%d expires:%d clock:%d versions:%d,%d,%d keys:%v files:%v} model={root:%d expires:%d clock:%d versions:%d,%d,%d keys:%v files:%v}",
			iteration, u.root.Version, u.root.Expires, u.lastClock, u.tsVersion, u.snapVersion, u.tgtVersion, rootKeys, u.files,
			m.rootVersion, m.expires, m.clock, m.tsVersion, m.snapVersion, m.tgtVersion, modelKeys, m.files)
	}
}

func reflectMaps[T comparable](left, right map[T][]string) bool {
	return reflect.DeepEqual(left, right)
}

func TestDifferentialRandomOperations(t *testing.T) {
	r := rand.New(rand.NewSource(14291429))
	u := newTestUpdater(t)
	m := newModel()

	for iteration := 0; iteration < 1500; iteration++ {
		switch r.Intn(3) {
		case 0:
			forceVersion := m.rootVersion + 1
			if r.Intn(6) == 0 {
				forceVersion += 1 + r.Intn(3)
			}
			in := makeModelRoot(r, m, forceVersion)
			now := m.clock + 1 + int64(r.Intn(50))
			if r.Intn(8) == 0 {
				now = m.clock - 1
			}
			if now < 0 {
				now = 0
			}
			wantErr, reason := m.updateRoot(now, in)
			gotIndex, gotErr := u.UpdateRoot(now, []SignedRoot{{modelRootFromInput(in), in.sigs}})
			t.Logf("iter=%d op=UpdateRoot input={now:%d version:%d expires:%d sigs:%v} output={index:%d err:%v} decision=%q",
				iteration, now, in.version, in.expires, in.sigs, gotIndex, gotErr, reason)
			wantIndex := gotIndex
			if errors.Is(wantErr, ErrClockRollback) {
				wantIndex = -1
			}
			if gotIndex != wantIndex || !errors.Is(gotErr, wantErr) {
				t.Fatalf("root mismatch got=(%d,%v) want=(%d,%v)", gotIndex, gotErr, wantIndex, wantErr)
			}
			assertModelState(t, iteration, u, m)
		case 1:
			in := makeRefresh(r, m)
			if err := meta.ValidateTimestamp(in.ts); err != nil || meta.ValidateSnapshot(in.snap) != nil || meta.ValidateTargets(in.tgt) != nil {
				gotErr := u.Refresh(in.now, in.ts, in.tsSigs, in.snap, in.snapSigs, in.tgt, in.tgtSigs)
				t.Logf("iter=%d op=InvalidRefresh input=%+v output=%v decision=%q", iteration, in, gotErr, "structurally invalid metadata")
				if !errors.Is(gotErr, ErrInvalidArgument) {
					t.Fatalf("invalid refresh got %v", gotErr)
				}
				continue
			}
			wantErr, reason := m.refresh(in)
			gotErr := u.Refresh(in.now, in.ts, in.tsSigs, in.snap, in.snapSigs, in.tgt, in.tgtSigs)
			t.Logf("iter=%d op=Refresh input=%+v output=%v decision=%q", iteration, in, gotErr, reason)
			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("refresh mismatch got=%v want=%v", gotErr, wantErr)
			}
			assertModelState(t, iteration, u, m)
		case 2:
			var path string
			var length int64
			hash := fmt.Sprintf("h%d", r.Intn(3))
			if r.Intn(2) == 0 {
				path = "p"
				length = int64(r.Intn(3))
			} else {
				path = "missing"
			}
			var wantErr error
			switch {
			case m.files == nil:
				wantErr = ErrNotReady
			default:
				file, ok := m.files[path]
				switch {
				case !ok:
					wantErr = ErrNotFound
				case file.Length != length:
					wantErr = ErrLengthMismatch
				case file.Hash != hash:
					wantErr = ErrHashMismatch
				}
			}
			gotErr := u.Verify(path, length, hash)
			t.Logf("iter=%d op=Verify input={path:%q length:%d hash:%q} output=%v decision=%q", iteration, path, length, hash, gotErr, "check readiness, existence, length then hash")
			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("verify mismatch got=%v want=%v", gotErr, wantErr)
			}
		}
	}
}
