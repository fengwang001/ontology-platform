package updater

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/keyring"
	"ontology/meta"
)

func assertState(t *testing.T, u *Updater, root, timestamp, snapshot, targets int) {
	t.Helper()

	if got := u.root.Version; got != root {
		t.Fatalf("root version = %d, want %d", got, root)
	}
	if got := u.timestampVersion; got != timestamp {
		t.Fatalf("timestamp version = %d, want %d", got, timestamp)
	}
	if got := u.snapshotVersion; got != snapshot {
		t.Fatalf("snapshot version = %d, want %d", got, snapshot)
	}
	if got := u.targetsVersion; got != targets {
		t.Fatalf("targets version = %d, want %d", got, targets)
	}
}

func role(keys ...string) keyring.Role {
	r, err := keyring.NewRole(keys, 1)
	if err != nil {
		panic(err)
	}
	return r
}

func files(version int) map[string]meta.FileMeta {
	return map[string]meta.FileMeta{
		fmt.Sprintf("path-%d", version): {Length: int64(version), Hash: fmt.Sprintf("hash-%d", version)},
	}
}

func target(version int, expires int64, sigs ...string) meta.Targets {
	return meta.Targets{Version: version, Expires: expires, Files: files(version), Signatures: sigs}
}

func snap(version, targets int, expires int64, sigs ...string) meta.Snapshot {
	return meta.Snapshot{Version: version, TargetsVersion: targets, Expires: expires, Signatures: sigs}
}

func ts(version, snapshot int, expires int64, sigs ...string) meta.Timestamp {
	return meta.Timestamp{Version: version, SnapVersion: snapshot, Expires: expires, Signatures: sigs}
}

func specRoot(version int, expires int64, replaceKeys map[string][]string, replaceThreshold map[meta.RoleName]int) meta.Root {
	defaults := map[meta.RoleName][]string{
		meta.RoleRoot:      {"A", "B", "C"},
		meta.RoleTimestamp: {"T"},
		meta.RoleSnapshot:  {"S"},
		meta.RoleTargets:   {"G"},
	}

	roles := make(map[meta.RoleName]keyring.Role, 4)
	for roleName, keys := range defaults {
		threshold := 1
		if roleName == meta.RoleRoot {
			threshold = 2
		}
		if replacement, ok := replaceKeys[string(roleName)]; ok {
			keys = replacement
		}
		if replacement, ok := replaceThreshold[roleName]; ok {
			threshold = replacement
		}
		r, err := keyring.NewRole(keys, threshold)
		if err != nil {
			panic(err)
		}
		roles[roleName] = r
	}

	return meta.Root{Version: version, Expires: expires, Roles: roles}
}

func newSpecUpdater(t *testing.T) *Updater {
	t.Helper()

	u, err := New(specRoot(1, 1000, nil, nil), []string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func withReady(fn func(*Updater) error) func(*Updater) error {
	return func(u *Updater) error {
		if err := u.Refresh(10, ts(5, 7, 1000, "T"), snap(7, 3, 1000, "S"), target(3, 1000, "G")); err != nil {
			return fmt.Errorf("prepare ready state: %w", err)
		}
		return fn(u)
	}
}

func prepareRefreshed(u *Updater, now int64, tv, sv, gv int) error {
	return u.Refresh(now, ts(tv, sv, 1000, "T"), snap(sv, gv, 1000, "S"), target(gv, 1000, "G"))
}

func TestRefreshOrderingAtomicityAndVerify(t *testing.T) {
	tests := []struct {
		name        string
		prepare     bool
		now         int64
		ts          meta.Timestamp
		snap        meta.Snapshot
		tgt         meta.Targets
		wantErr     error
		wantTS      int
		wantSnap    int
		wantTargets int
	}{
		{name: "initial commit", now: 10, ts: ts(5, 7, 1000, "T"), snap: snap(7, 3, 1000, "S"), tgt: target(3, 1000, "G"), wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "ready", prepare: true, now: 10, ts: ts(5, 7, 1000, "T"), snap: snap(7, 3, 1000, "S"), tgt: target(3, 1000, "G"), wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "root expired", prepare: true, now: 1000, ts: ts(6, 8, 1, "X"), snap: snap(8, 3, 1, "X"), tgt: target(3, 1, "X"), wantErr: ErrRootExpired, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "timestamp signature", prepare: true, now: 10, ts: ts(6, 8, 1000, "X"), snap: snap(8, 3, 1000, "S"), tgt: target(3, 1000, "G"), wantErr: ErrTimestampSignatureInsufficient, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "timestamp rollback", prepare: true, now: 10, ts: ts(4, 7, 1000, "T"), snap: snap(7, 3, 1000, "S"), tgt: target(3, 1000, "G"), wantErr: ErrTimestampVersionRollback, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "timestamp exact expiry", prepare: true, now: 10, ts: ts(6, 8, 10, "T"), snap: snap(8, 3, 1000, "S"), tgt: target(3, 1000, "G"), wantErr: ErrTimestampExpired, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "equal timestamp no change skips snapshot", prepare: true, now: 10, ts: ts(5, 7, 1000, "T"), snap: snap(999, 999, 1000), tgt: target(999, 1000), wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "equal timestamp still checks expiry", prepare: true, now: 10, ts: ts(5, 7, 10, "T"), snap: snap(999, 999, 1000), tgt: target(999, 1000), wantErr: ErrTimestampExpired, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "snapshot rollback", prepare: true, now: 10, ts: ts(6, 6, 1000, "T"), snap: snap(6, 3, 1000, "S"), tgt: target(3, 1000, "G"), wantErr: ErrSnapshotVersionRollback, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "snapshot signature", prepare: true, now: 10, ts: ts(6, 8, 1000, "T"), snap: snap(8, 3, 1000, "X"), tgt: target(3, 1000, "G"), wantErr: ErrSnapshotSignatureInsufficient, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "snapshot mismatch", prepare: true, now: 10, ts: ts(6, 8, 1000, "T"), snap: snap(9, 3, 1000, "S"), tgt: target(3, 1000, "G"), wantErr: ErrSnapshotVersionMismatch, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "targets rollback", prepare: true, now: 10, ts: ts(6, 8, 1000, "T"), snap: snap(8, 2, 1000, "S"), tgt: target(2, 1000, "G"), wantErr: ErrTargetsVersionRollback, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "snapshot expiry", prepare: true, now: 10, ts: ts(6, 8, 1000, "T"), snap: snap(8, 3, 10, "S"), tgt: target(3, 1000, "G"), wantErr: ErrSnapshotExpired, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "targets signature", prepare: true, now: 10, ts: ts(6, 8, 1000, "T"), snap: snap(8, 3, 1000, "S"), tgt: target(3, 1000, "X"), wantErr: ErrTargetsSignatureInsufficient, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "targets mismatch", prepare: true, now: 10, ts: ts(6, 8, 1000, "T"), snap: snap(8, 4, 1000, "S"), tgt: target(5, 1000, "G"), wantErr: ErrTargetsVersionMismatch, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "targets exact expiry", prepare: true, now: 10, ts: ts(6, 8, 1000, "T"), snap: snap(8, 3, 1000, "S"), tgt: target(3, 10, "G"), wantErr: ErrTargetsExpired, wantTS: 5, wantSnap: 7, wantTargets: 3},
		{name: "successful newer commit", prepare: true, now: 10, ts: ts(6, 8, 1000, "T"), snap: snap(8, 3, 1000, "S"), tgt: target(3, 1000, "G"), wantTS: 6, wantSnap: 8, wantTargets: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := newSpecUpdater(t)
			if tt.prepare {
				if err := prepareRefreshed(u, 10, 5, 7, 3); err != nil {
					t.Fatal(err)
				}
			}

			err := u.Refresh(tt.now, tt.ts, tt.snap, tt.tgt)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Refresh() error = %v, want %v", err, tt.wantErr)
			}
			assertState(t, u, 1, tt.wantTS, tt.wantSnap, tt.wantTargets)
		})
	}
}

func TestVerify(t *testing.T) {
	u := newSpecUpdater(t)
	if err := u.Verify("path-3", 3, "hash-3"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("before refresh error = %v", err)
	}
	if err := prepareRefreshed(u, 10, 5, 7, 3); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		length  int64
		hash    string
		wantErr error
	}{
		{name: "exists", path: "path-3", length: 3, hash: "hash-3"},
		{name: "missing", path: "missing", length: 3, hash: "hash-3", wantErr: ErrFileNotFound},
		{name: "length first", path: "path-3", length: 4, hash: "wrong", wantErr: ErrLengthMismatch},
		{name: "hash", path: "path-3", length: 3, hash: "wrong", wantErr: ErrHashMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := u.Verify(tt.path, tt.length, tt.hash); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Verify() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestClearAllowsSmallerTimestampAndRejectionOrder(t *testing.T) {
	u := newSpecUpdater(t)
	if err := prepareRefreshed(u, 10, 9, 9, 4); err != nil {
		t.Fatal(err)
	}
	err := u.UpdateRoot(10, []meta.RootCandidate{{
		Root:       specRoot(2, 1000, map[string][]string{"timestamp": {"T2"}, "root": {"A", "B", "C"}}, nil),
		Signatures: []string{"A", "B"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	err = u.Refresh(10,
		meta.Timestamp{Version: 1, Expires: 1000, SnapVersion: 1, Signatures: []string{"T2"}},
		meta.Snapshot{Version: 1, Expires: 1000, TargetsVersion: 4, Signatures: []string{"S"}},
		target(4, 1000, "G"),
	)
	if err != nil {
		t.Fatalf("smaller timestamp after clear: %v", err)
	}
	assertState(t, u, 2, 1, 1, 4)

	badTS := meta.Timestamp{Version: 0, Expires: 1000, SnapVersion: 1, Signatures: []string{"T2"}}
	if err := u.Refresh(1, badTS, snap(1, 4, 1000, "S"), target(4, 1000, "G")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid argument priority = %v", err)
	}
	if u.lastNow != 10 {
		t.Fatalf("lastNow = %d, want 10", u.lastNow)
	}
	if err := u.Refresh(9, ts(2, 2, 1000, "T2"), snap(2, 4, 1000, "S"), target(4, 1000, "G")); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("clock rollback = %v", err)
	}
	assertState(t, u, 2, 1, 1, 4)
}

type naiveState struct {
	now       int64
	root      meta.Root
	timestamp int
	snapshot  int
	targets   int
	files     map[string]meta.FileMeta
}

func newNaiveState(root meta.Root) naiveState {
	return naiveState{root: cloneRoot(root)}
}

func (s *naiveState) updateRoot(now int64, chain []meta.RootCandidate) (error, int) {
	if err := meta.ValidateRootChain(chain); err != nil {
		return ErrInvalidArgument, -1
	}
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument, -1
	}
	if now < s.now {
		return ErrClockRollback, -1
	}
	s.now = now
	for index, candidate := range chain {
		if candidate.Root.Version != s.root.Version+1 {
			return ChainError{Index: index, Err: ErrVersionGap}, index
		}
		if !s.root.Roles[meta.RoleRoot].Verify(candidate.Signatures, nil) {
			return ChainError{Index: index, Err: ErrOldRootSignatureInsufficient}, index
		}
		if !candidate.Root.Roles[meta.RoleRoot].Verify(candidate.Signatures, nil) {
			return ChainError{Index: index, Err: ErrNewRootSignatureInsufficient}, index
		}

		clearTimestamp := hasRemovedKey(s.root.Roles[meta.RoleTimestamp], candidate.Root.Roles[meta.RoleTimestamp])
		clearSnapshot := hasRemovedKey(s.root.Roles[meta.RoleSnapshot], candidate.Root.Roles[meta.RoleSnapshot])
		s.root = cloneRoot(candidate.Root)
		if clearTimestamp || clearSnapshot {
			s.timestamp = 0
			s.snapshot = 0
		}
	}
	if now >= s.root.Expires {
		return ErrRootExpired, -1
	}
	return nil, -1
}

func (s *naiveState) refresh(now int64, timestampMeta meta.Timestamp, snapshotMeta meta.Snapshot, targetsMeta meta.Targets) error {
	if err := meta.ValidateTimestamp(timestampMeta); err != nil {
		return ErrInvalidArgument
	}
	if err := meta.ValidateSnapshot(snapshotMeta); err != nil {
		return ErrInvalidArgument
	}
	if err := meta.ValidateTargets(targetsMeta); err != nil {
		return ErrInvalidArgument
	}
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	if now < s.now {
		return ErrClockRollback
	}
	s.now = now
	if now >= s.root.Expires {
		return ErrRootExpired
	}
	if !s.root.Roles[meta.RoleTimestamp].Verify(timestampMeta.Signatures, nil) {
		return ErrTimestampSignatureInsufficient
	}
	if timestampMeta.Version < s.timestamp {
		return ErrTimestampVersionRollback
	}
	if now >= timestampMeta.Expires {
		return ErrTimestampExpired
	}
	if timestampMeta.Version == s.timestamp {
		return nil
	}
	if timestampMeta.SnapVersion < s.snapshot {
		return ErrSnapshotVersionRollback
	}
	if !s.root.Roles[meta.RoleSnapshot].Verify(snapshotMeta.Signatures, nil) {
		return ErrSnapshotSignatureInsufficient
	}
	if snapshotMeta.Version != timestampMeta.SnapVersion {
		return ErrSnapshotVersionMismatch
	}
	if snapshotMeta.TargetsVersion < s.targets {
		return ErrTargetsVersionRollback
	}
	if now >= snapshotMeta.Expires {
		return ErrSnapshotExpired
	}
	if !s.root.Roles[meta.RoleTargets].Verify(targetsMeta.Signatures, nil) {
		return ErrTargetsSignatureInsufficient
	}
	if targetsMeta.Version != snapshotMeta.TargetsVersion {
		return ErrTargetsVersionMismatch
	}
	if now >= targetsMeta.Expires {
		return ErrTargetsExpired
	}

	s.timestamp = timestampMeta.Version
	s.snapshot = snapshotMeta.Version
	s.targets = targetsMeta.Version
	s.files = cloneFiles(targetsMeta.Files)
	return nil
}

func errorCode(err error) (string, int) {
	if err == nil {
		return "nil", -1
	}
	candidates := []error{
		ErrInvalidArgument, ErrClockRollback, ErrVersionGap, ErrRootExpired,
		ErrOldRootSignatureInsufficient, ErrNewRootSignatureInsufficient, ErrRootSignatureInsufficient,
		ErrTimestampSignatureInsufficient, ErrSnapshotSignatureInsufficient, ErrTargetsSignatureInsufficient,
		ErrTimestampVersionRollback, ErrSnapshotVersionRollback, ErrTargetsVersionRollback,
		ErrSnapshotVersionMismatch, ErrTargetsVersionMismatch,
		ErrTimestampExpired, ErrSnapshotExpired, ErrTargetsExpired,
		ErrNotReady, ErrFileNotFound, ErrLengthMismatch, ErrHashMismatch,
	}
	for _, candidate := range candidates {
		if errors.Is(err, candidate) {
			var chainErr ChainError
			index := -1
			if errors.As(err, &chainErr) {
				index = chainErr.Index
			}
			return candidate.Error(), index
		}
	}
	return err.Error(), -1
}

func randomRoot(rng *rand.Rand, version int) (meta.Root, map[meta.RoleName][]string) {
	names := []meta.RoleName{meta.RoleRoot, meta.RoleTimestamp, meta.RoleSnapshot, meta.RoleTargets}
	roles := make(map[meta.RoleName]keyring.Role, len(names))
	keysByRole := make(map[meta.RoleName][]string, len(names))
	for _, name := range names {
		prefix := string(name[0:1])
		keys := []string{
			fmt.Sprintf("%s-v%d-k0", prefix, version),
			fmt.Sprintf("%s-v%d-k1", prefix, version),
		}
		keysByRole[name] = keys
		threshold := 2
		if name != meta.RoleRoot {
			threshold = 1
			if rng.Intn(3) == 0 {
				keys = keys[:1]
			}
		}
		roleValue, err := keyring.NewRole(keys, threshold)
		if err != nil {
			panic(err)
		}
		roles[name] = roleValue
	}

	return meta.Root{Version: version, Expires: int64(100 + rng.Intn(901)), Roles: roles}, keysByRole
}

func evolvedRoot(rng *rand.Rand, current meta.Root, version int) meta.Root {
	names := []meta.RoleName{meta.RoleRoot, meta.RoleTimestamp, meta.RoleSnapshot, meta.RoleTargets}
	roles := make(map[meta.RoleName]keyring.Role, len(names))

	for _, name := range names {
		oldKeys := current.Roles[name].KeyIDs()
		keys := []string{fmt.Sprintf("%s-v%d-new", name, version)}
		if rng.Intn(2) == 0 && len(oldKeys) > 0 {
			keys = append(keys, oldKeys[rng.Intn(len(oldKeys))])
		}
		if name != meta.RoleRoot && rng.Intn(2) == 0 {
			keys = append(keys, oldKeys...)
		}

		threshold := 1
		if name == meta.RoleRoot {
			threshold = 2
		}
		if len(keys) < threshold {
			keys = append(keys, oldKeys...)
		}

		uniqueKeys := make([]string, 0, len(keys))
		seen := map[string]struct{}{}
		for _, key := range keys {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			uniqueKeys = append(uniqueKeys, key)
		}

		roleValue, err := keyring.NewRole(uniqueKeys, threshold)
		if err != nil {
			panic(err)
		}
		roles[name] = roleValue
	}

	return meta.Root{Version: version, Expires: int64(50 + rng.Intn(951)), Roles: roles}
}

func randomSignatures(rng *rand.Rand, currentKeys, nextKeys []string) []string {
	pool := append(append([]string{}, currentKeys...), nextKeys...)
	pool = append(pool, "unknown-a", "unknown-b")
	count := 1 + rng.Intn(5)
	signatures := make([]string, 0, count)
	for range count {
		signatures = append(signatures, pool[rng.Intn(len(pool))])
	}
	return signatures
}

func randomFileMap(rng *rand.Rand, version int) map[string]meta.FileMeta {
	if rng.Intn(4) == 0 {
		return map[string]meta.FileMeta{}
	}
	return map[string]meta.FileMeta{
		fmt.Sprintf("path-%d", version): {Length: int64(version), Hash: fmt.Sprintf("hash-%d", version)},
	}
}

func assertSameState(t *testing.T, u *Updater, model *naiveState, step int, log *strings.Builder) {
	t.Helper()

	if u.root.Version != model.root.Version || u.timestampVersion != model.timestamp || u.snapshotVersion != model.snapshot || u.targetsVersion != model.targets || u.lastNow != model.now {
		t.Fatalf("state mismatch at step %d\nactual(root=%d now=%d ts=%d snap=%d tgt=%d)\nmodel(root=%d now=%d ts=%d snap=%d tgt=%d)\nlog:\n%s",
			step, u.root.Version, u.lastNow, u.timestampVersion, u.snapshotVersion, u.targetsVersion,
			model.root.Version, model.now, model.timestamp, model.snapshot, model.targets, log.String())
	}
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1429))
	var log strings.Builder

	initialRoot, initialKeys := randomRoot(rng, 1)
	initialSigs := []string{initialKeys[meta.RoleRoot][0], initialKeys[meta.RoleRoot][1]}
	u, err := New(initialRoot, initialSigs)
	if err != nil {
		t.Fatal(err)
	}
	model := newNaiveState(initialRoot)

	fmt.Fprintf(&log, "INIT rootVersion=%d sigs=%v reason=self-root-threshold accepted\n", initialRoot.Version, initialSigs)

	for step := 0; step < 1500; step++ {
		now := int64(rng.Intn(1001))
		if rng.Intn(5) == 0 {
			now = model.now
		}

		var actualErr, modelErr error
		var actualIndex, modelIndex int

		switch rng.Intn(3) {
		case 0:
			count := 1 + rng.Intn(3)
			chain := make([]meta.RootCandidate, 0, count)
			currentModelRoot := model.root
			for j := 0; j < count; j++ {
				var candidateRoot meta.Root
				if rng.Intn(7) == 0 {
					candidateRoot, _ = randomRoot(rng, currentModelRoot.Version+2+rng.Intn(2))
				} else {
					candidateRoot = evolvedRoot(rng, currentModelRoot, currentModelRoot.Version+1)
				}
				chain = append(chain, meta.RootCandidate{
					Root:       candidateRoot,
					Signatures: randomSignatures(rng, currentModelRoot.Roles[meta.RoleRoot].KeyIDs(), candidateRoot.Roles[meta.RoleRoot].KeyIDs()),
				})
				currentModelRoot = candidateRoot
			}

			fmt.Fprintf(&log, "STEP %d UpdateRoot now=%d chainLen=%d input=%+v\n", step, now, len(chain), chain)
			modelErr, modelIndex = model.updateRoot(now, chain)
			actualErr = u.UpdateRoot(now, chain)
			_, actualIndex = errorCode(actualErr)
			fmt.Fprintf(&log, "  actual=%v(%d) model=%v(%d) reason=chain-version-old-threshold-new-threshold-final-expiry\n", actualErr, actualIndex, modelErr, modelIndex)

		case 1:
			timestampVersion := model.timestamp
			if rng.Intn(3) > 0 {
				timestampVersion += 1 + rng.Intn(3)
			}
			if rng.Intn(6) == 0 {
				timestampVersion = rng.Intn(20) + 1
			}

			snapshotVersion := model.snapshot
			if rng.Intn(2) == 0 {
				snapshotVersion = timestampVersion
			} else {
				snapshotVersion += rng.Intn(3)
			}
			targetsVersion := model.targets
			if rng.Intn(2) == 0 {
				targetsVersion = snapshotVersion
			}

			timestampMeta := meta.Timestamp{
				Version:     timestampVersion,
				Expires:     int64(1 + rng.Intn(1200)),
				SnapVersion: snapshotVersion,
				Signatures:  []string{model.root.Roles[meta.RoleTimestamp].KeyIDs()[0]},
			}
			snapshotMeta := meta.Snapshot{
				Version:        snapshotVersion,
				Expires:        int64(1 + rng.Intn(1200)),
				TargetsVersion: targetsVersion,
				Signatures:     []string{model.root.Roles[meta.RoleSnapshot].KeyIDs()[0]},
			}
			targetsMeta := meta.Targets{
				Version:    targetsVersion,
				Expires:    int64(1 + rng.Intn(1200)),
				Files:      randomFileMap(rng, targetsVersion),
				Signatures: []string{model.root.Roles[meta.RoleTargets].KeyIDs()[0]},
			}

			if rng.Intn(8) == 0 {
				timestampMeta.Signatures = []string{"unknown"}
			}
			if rng.Intn(10) == 0 {
				snapshotMeta.Signatures = []string{"unknown"}
			}
			if rng.Intn(12) == 0 {
				targetsMeta.Signatures = []string{"unknown"}
			}

			fmt.Fprintf(&log, "STEP %d Refresh now=%d ts=%+v snap=%+v tgt=%+v\n", step, now, timestampMeta, snapshotMeta, targetsMeta)
			modelErr = model.refresh(now, timestampMeta, snapshotMeta, targetsMeta)
			actualErr = u.Refresh(now, timestampMeta, snapshotMeta, targetsMeta)
			fmt.Fprintf(&log, "  actual=%v model=%v reason=ordered-validation-atomic-commit\n", actualErr, modelErr)

		case 2:
			path := fmt.Sprintf("path-%d", model.targets)
			length := int64(model.targets)
			hash := fmt.Sprintf("hash-%d", model.targets)
			switch rng.Intn(5) {
			case 1:
				path = "missing"
			case 2:
				length++
			case 3:
				hash = "wrong"
			}

			fmt.Fprintf(&log, "STEP %d Verify path=%q length=%d hash=%q\n", step, path, length, hash)
			actualErr = u.Verify(path, length, hash)
			if model.targets == 0 {
				modelErr = ErrNotReady
			} else {
				file, ok := model.files[path]
				switch {
				case !ok:
					modelErr = ErrFileNotFound
				case file.Length != length:
					modelErr = ErrLengthMismatch
				case file.Hash != hash:
					modelErr = ErrHashMismatch
				}
			}
			fmt.Fprintf(&log, "  actual=%v model=%v reason=ready-existence-length-hash\n", actualErr, modelErr)
		}

		actualCode, gotIndex := errorCode(actualErr)
		modelCode, wantIndex := errorCode(modelErr)
		if actualIndex == 0 && actualErr == nil {
			gotIndex = -1
		}
		if actualCode != modelCode || gotIndex != wantIndex {
			t.Fatalf("error mismatch at step %d: actual=(%s,%d), model=(%s,%d)\nlog:\n%s", step, actualCode, gotIndex, modelCode, wantIndex, log.String())
		}
		assertSameState(t, u, &model, step, &log)
	}

	t.Logf("random replay log:\n%s", log.String())
}

func TestConcurrentOperations(t *testing.T) {
	u := newSpecUpdater(t)
	var wg sync.WaitGroup

	for worker := 0; worker < 24; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			for range 20 {
				err := u.Refresh(10, ts(1, 1, 1000, "T"), snap(1, 1, 1000, "S"), target(1, 1000, "G"))
				if err != nil {
					t.Errorf("Refresh() error = %v", err)
					return
				}
				if err := u.Verify("path-1", 1, "hash-1"); err != nil {
					t.Errorf("Verify() error = %v", err)
					return
				}
			}
		}(worker)
	}

	wg.Wait()

	if u.timestampVersion != 1 || u.snapshotVersion != 1 || u.targetsVersion != 1 {
		t.Fatalf("non-serializable versions: ts=%d snap=%d tgt=%d", u.timestampVersion, u.snapshotVersion, u.targetsVersion)
	}
}

func TestUpdateRootPerformsAtMostTwoThresholdChecks(t *testing.T) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "updater.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		function, ok := node.(*ast.FuncDecl)
		if !ok || function.Name.Name != "UpdateRoot" {
			return true
		}

		ast.Inspect(function.Body, func(callNode ast.Node) bool {
			call, ok := callNode.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "Verify" {
				count++
			}
			return true
		})
		return false
	})

	if count != 2 {
		t.Fatalf("UpdateRoot threshold checks = %d, want exactly 2", count)
	}
}

func TestNewAndRootChain(t *testing.T) {
	tests := []struct {
		name        string
		setup       func() (*Updater, error)
		run         func(*Updater) error
		wantErr     error
		wantIndex   int
		wantRoot    int
		wantTS      int
		wantSnap    int
		wantTargets int
	}{
		{
			name:    "new root insufficient self signature",
			setup:   func() (*Updater, error) { return New(specRoot(1, 1000, nil, nil), []string{"X"}) },
			wantErr: ErrRootSignatureInsufficient,
		},
		{
			name:  "duplicate old signatures count once",
			setup: func() (*Updater, error) { return New(specRoot(1, 1000, nil, nil), []string{"A", "B"}) },
			run: func(u *Updater) error {
				return u.UpdateRoot(10, []meta.RootCandidate{{Root: specRoot(2, 1000, map[string][]string{"root": {"C", "D", "E"}}, nil), Signatures: []string{"A", "A", "D"}}})
			},
			wantErr:   ErrOldRootSignatureInsufficient,
			wantIndex: 0,
			wantRoot:  1,
		},
		{
			name:  "new root signature insufficient",
			setup: func() (*Updater, error) { return New(specRoot(1, 1000, nil, nil), []string{"A", "B"}) },
			run: func(u *Updater) error {
				return u.UpdateRoot(10, []meta.RootCandidate{{Root: specRoot(2, 1000, map[string][]string{"root": {"C", "D", "E"}}, nil), Signatures: []string{"A", "B", "D"}}})
			},
			wantErr:   ErrNewRootSignatureInsufficient,
			wantIndex: 0,
			wantRoot:  1,
		},
		{
			name: "rotation removing timestamp key clears timestamp and snapshot only",
			setup: func() (*Updater, error) {
				u, err := New(specRoot(1, 1000, nil, nil), []string{"A", "B"})
				return u, err
			},
			run: func(u *Updater) error {
				if err := prepareRefreshed(u, 10, 5, 7, 3); err != nil {
					return err
				}
				return u.UpdateRoot(10, []meta.RootCandidate{{Root: specRoot(2, 1000, map[string][]string{"root": {"C", "D", "E"}, "timestamp": {"T2"}}, nil), Signatures: []string{"A", "C", "D"}}})
			},
			wantRoot:    2,
			wantTS:      0,
			wantSnap:    0,
			wantTargets: 3,
		},
		{
			name:  "partial chain keeps accepted root and reports index",
			setup: func() (*Updater, error) { return New(specRoot(1, 1000, nil, nil), []string{"A", "B"}) },
			run: func(u *Updater) error {
				return u.UpdateRoot(10, []meta.RootCandidate{
					{Root: specRoot(2, 1000, map[string][]string{"root": {"C", "D", "E"}}, nil), Signatures: []string{"A", "C", "D"}},
					{Root: specRoot(4, 1000, map[string][]string{"root": {"C", "D", "E"}}, nil), Signatures: []string{"C", "D"}},
				})
			},
			wantErr:   ErrVersionGap,
			wantIndex: 1,
			wantRoot:  2,
		},
		{
			name:  "adding timestamp key does not clear",
			setup: func() (*Updater, error) { return New(specRoot(1, 1000, nil, nil), []string{"A", "B"}) },
			run: func(u *Updater) error {
				if err := prepareRefreshed(u, 10, 1, 1, 1); err != nil {
					return err
				}
				return u.UpdateRoot(10, []meta.RootCandidate{{Root: specRoot(2, 1000, map[string][]string{"timestamp": {"T", "T2"}}, nil), Signatures: []string{"A", "B"}}})
			},
			wantRoot:    2,
			wantTS:      1,
			wantSnap:    1,
			wantTargets: 1,
		},
		{
			name:  "threshold-only change does not clear",
			setup: func() (*Updater, error) { return New(specRoot(1, 1000, nil, nil), []string{"A", "B"}) },
			run: func(u *Updater) error {
				if err := prepareRefreshed(u, 10, 1, 1, 1); err != nil {
					return err
				}
				return u.UpdateRoot(10, []meta.RootCandidate{{Root: specRoot(2, 1000, nil, map[meta.RoleName]int{meta.RoleTimestamp: 1}), Signatures: []string{"A", "B"}}})
			},
			wantRoot:    2,
			wantTS:      1,
			wantSnap:    1,
			wantTargets: 1,
		},
		{
			name:  "expired intermediate root accepted and final live root succeeds",
			setup: func() (*Updater, error) { return New(specRoot(1, 100, nil, nil), []string{"A", "B"}) },
			run: func(u *Updater) error {
				return u.UpdateRoot(200, []meta.RootCandidate{
					{Root: specRoot(2, 100, map[string][]string{"root": {"C", "D", "E"}}, nil), Signatures: []string{"A", "C", "D"}},
					{Root: specRoot(3, 500, map[string][]string{"root": {"E", "F", "G2"}}, nil), Signatures: []string{"C", "D", "E", "F"}},
				})
			},
			wantRoot: 3,
		},
		{
			name:  "final root exactly expired advances then recovery succeeds",
			setup: func() (*Updater, error) { return New(specRoot(1, 1000, nil, nil), []string{"A", "B"}) },
			run: func(u *Updater) error {
				err := u.UpdateRoot(100, []meta.RootCandidate{{Root: specRoot(2, 100, map[string][]string{"root": {"C", "D", "E"}}, nil), Signatures: []string{"A", "C", "D"}}})
				if !errors.Is(err, ErrRootExpired) {
					return fmt.Errorf("first update = %v", err)
				}
				if err = u.Refresh(100, ts(1, 1, 500, "T2"), snap(1, 1, 500, "S"), target(1, 500, "G")); !errors.Is(err, ErrRootExpired) {
					return fmt.Errorf("refresh expired root = %v", err)
				}
				return u.UpdateRoot(100, []meta.RootCandidate{{Root: specRoot(3, 500, map[string][]string{"root": {"E", "F", "G2"}}, nil), Signatures: []string{"C", "D", "E", "F"}}})
			},
			wantRoot: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, setupErr := tt.setup()
			if setupErr != nil {
				if !errors.Is(setupErr, tt.wantErr) {
					t.Fatalf("setup error = %v, want %v", setupErr, tt.wantErr)
				}
				return
			}
			err := tt.run(u)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				var chainErr ChainError
				if !errors.As(err, &chainErr) || chainErr.Index != tt.wantIndex {
					t.Fatalf("chain failure = %v, want index %d", err, tt.wantIndex)
				}
			}
			assertState(t, u, tt.wantRoot, tt.wantTS, tt.wantSnap, tt.wantTargets)
		})
	}
}
