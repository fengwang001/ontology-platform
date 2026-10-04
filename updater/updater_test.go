package updater

import (
	"errors"
	"testing"

	"ontology/meta"
)

func role(threshold int, keys ...string) meta.Role {
	return meta.Role{KeyIDs: keys, Threshold: threshold}
}

func baseRoot(version int, expires int64) meta.Root {
	return meta.Root{Version: version, Expires: expires, Roles: map[meta.RoleKind]meta.Role{
		meta.RoleRoot:      role(2, "A", "B", "C"),
		meta.RoleTimestamp: role(1, "T"),
		meta.RoleSnapshot:  role(1, "S"),
		meta.RoleTargets:   role(1, "G"),
	}}
}

func nextRoot(version int, expires int64) meta.Root {
	root := baseRoot(version, expires)
	root.Roles[meta.RoleRoot] = role(2, "C", "D", "E")
	return root
}

func timestampRoot(version int, expires int64, keys ...string) meta.Root {
	root := nextRoot(version, expires)
	root.Roles[meta.RoleTimestamp] = role(1, keys...)
	return root
}

func newTestUpdater(t *testing.T) *Updater {
	t.Helper()
	u, err := New(baseRoot(1, 1000), []string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUpdateRoot(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(*Updater)
		now       int64
		chain     []SignedRoot
		wantIndex int
		wantErr   error
		wantVer   int
		wantClear bool
	}{
		{"accept", nil, 10, []SignedRoot{{nextRoot(2, 1000), []string{"A", "C", "D"}}}, 0, nil, 2, false},
		{"old threshold duplicate", nil, 10, []SignedRoot{{nextRoot(2, 1000), []string{"A", "A", "D"}}}, 0, ErrOldRootSignatures, 1, false},
		{"new threshold", nil, 10, []SignedRoot{{nextRoot(2, 1000), []string{"A", "B", "D"}}}, 0, ErrNewRootSignatures, 1, false},
		{"partial chain", nil, 10, []SignedRoot{{nextRoot(2, 1000), []string{"A", "C", "D"}}, {nextRoot(4, 1000), []string{"C", "D"}}}, 1, ErrVersionMismatch, 2, false},
		{"invalid chain atomic", nil, 10, []SignedRoot{{baseRoot(0, 1000), []string{"A"}}}, -1, ErrInvalidArgument, 1, false},
		{"removal clears", nil, 10, []SignedRoot{{timestampRoot(2, 1000, "T2"), []string{"A", "C", "D"}}}, 0, nil, 2, true},
		{"addition keeps", nil, 10, []SignedRoot{{timestampRoot(2, 1000, "T", "T2"), []string{"A", "C", "D"}}}, 0, nil, 2, false},
		{"intermediate expiry", nil, 200, []SignedRoot{{nextRoot(2, 100), []string{"A", "C", "D"}}, {nextRoot(3, 200), []string{"C", "D"}}}, 1, ErrRootExpired, 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := newTestUpdater(t)
			u.tsVersion, u.snapVersion, u.tgtVersion = 5, 7, 3
			if tt.prepare != nil {
				tt.prepare(u)
			}
			index, err := u.UpdateRoot(tt.now, tt.chain)
			if index != tt.wantIndex || !errors.Is(err, tt.wantErr) {
				t.Fatalf("index=%d err=%v, want %d/%v", index, err, tt.wantIndex, tt.wantErr)
			}
			if u.root.Version != tt.wantVer {
				t.Fatalf("root=%d want %d", u.root.Version, tt.wantVer)
			}
			if tt.wantClear && (u.tsVersion != 0 || u.snapVersion != 0 || u.tgtVersion != 3) {
				t.Fatalf("clear state=%d,%d,%d", u.tsVersion, u.snapVersion, u.tgtVersion)
			}
			if !tt.wantClear && u.root.Version == 2 && (u.tsVersion != 5 || u.snapVersion != 7 || u.tgtVersion != 3) {
				t.Fatalf("state changed=%d,%d,%d", u.tsVersion, u.snapVersion, u.tgtVersion)
			}
		})
	}
}

func timestamp(version int, expires int64, snapVersion int) meta.Timestamp {
	return meta.Timestamp{Version: version, Expires: expires, SnapVersion: snapVersion}
}

func snapshot(version int, expires int64, targetsVersion int) meta.Snapshot {
	return meta.Snapshot{Version: version, Expires: expires, TargetsVersion: targetsVersion}
}

func targets(version int, expires int64) meta.Targets {
	return meta.Targets{Version: version, Expires: expires, Files: map[string]meta.File{"p": {Length: 1, Hash: "h"}}}
}

func TestRefresh(t *testing.T) {
	goodTS := timestamp(6, 2000, 8)
	goodSnap := snapshot(8, 2000, 3)
	goodTgt := targets(3, 2000)

	tests := []struct {
		name     string
		now      int64
		ts       meta.Timestamp
		tsSigs   []string
		snap     meta.Snapshot
		snapSigs []string
		tgt      meta.Targets
		tgtSigs  []string
		wantErr  error
		want     [3]int
	}{
		{"success", 100, goodTS, []string{"T"}, goodSnap, []string{"S"}, goodTgt, []string{"G"}, nil, [3]int{6, 8, 3}},
		{"same version skips snapshot", 100, timestamp(5, 2000, 99), []string{"T"}, goodSnap, nil, goodTgt, nil, nil, [3]int{5, 7, 3}},
		{"root expired", 2000, goodTS, nil, goodSnap, nil, goodTgt, nil, ErrRootExpired, [3]int{5, 7, 3}},
		{"timestamp signature", 100, goodTS, nil, goodSnap, []string{"S"}, goodTgt, []string{"G"}, ErrInsufficientSignatures, [3]int{5, 7, 3}},
		{"timestamp rollback", 100, timestamp(4, 2000, 8), []string{"T"}, goodSnap, []string{"S"}, goodTgt, []string{"G"}, ErrRollback, [3]int{5, 7, 3}},
		{"timestamp expired before same", 100, timestamp(5, 100, 7), []string{"T"}, goodSnap, nil, goodTgt, nil, ErrExpired, [3]int{5, 7, 3}},
		{"snapshot rollback", 100, timestamp(7, 2000, 6), []string{"T"}, snapshot(6, 2000, 3), []string{"S"}, goodTgt, []string{"G"}, ErrRollback, [3]int{5, 7, 3}},
		{"snapshot signature", 100, timestamp(7, 2000, 9), []string{"T"}, snapshot(9, 2000, 3), nil, goodTgt, []string{"G"}, ErrInsufficientSignatures, [3]int{5, 7, 3}},
		{"snapshot mismatch", 100, timestamp(7, 2000, 9), []string{"T"}, snapshot(8, 2000, 3), []string{"S"}, goodTgt, []string{"G"}, ErrVersionMismatch, [3]int{5, 7, 3}},
		{"targets rollback", 100, timestamp(7, 2000, 9), []string{"T"}, snapshot(9, 2000, 2), []string{"S"}, targets(2, 2000), []string{"G"}, ErrRollback, [3]int{5, 7, 3}},
		{"snapshot expired", 100, timestamp(7, 2000, 9), []string{"T"}, snapshot(9, 100, 3), []string{"S"}, goodTgt, []string{"G"}, ErrExpired, [3]int{5, 7, 3}},
		{"targets signature", 100, timestamp(7, 2000, 9), []string{"T"}, snapshot(9, 2000, 3), []string{"S"}, goodTgt, nil, ErrInsufficientSignatures, [3]int{5, 7, 3}},
		{"targets mismatch", 100, timestamp(7, 2000, 9), []string{"T"}, snapshot(9, 2000, 4), []string{"S"}, goodTgt, []string{"G"}, ErrVersionMismatch, [3]int{5, 7, 3}},
		{"targets expired", 100, timestamp(7, 2000, 9), []string{"T"}, snapshot(9, 2000, 3), []string{"S"}, targets(3, 100), []string{"G"}, ErrExpired, [3]int{5, 7, 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := newTestUpdater(t)
			u.tsVersion, u.snapVersion, u.tgtVersion = 5, 7, 3
			err := u.Refresh(tt.now, tt.ts, tt.tsSigs, tt.snap, tt.snapSigs, tt.tgt, tt.tgtSigs)
			got := [3]int{u.tsVersion, u.snapVersion, u.tgtVersion}
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("err=%v state=%v, want %v state=%v", err, got, tt.wantErr, tt.want)
			}
		})
	}
}

func TestRefreshRejectOrderAndClock(t *testing.T) {
	u := newTestUpdater(t)
	_, err := u.UpdateRoot(200, []SignedRoot{{nextRoot(2, 3000), []string{"A", "C", "D"}}})
	if err != nil {
		t.Fatal(err)
	}
	err = u.Refresh(100, timestamp(0, 1, 0), nil, snapshot(0, 1, 0), nil, targets(0, 1), nil)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid priority: %v", err)
	}
	if u.lastClock != 200 {
		t.Fatalf("invalid argument moved clock to %d", u.lastClock)
	}
	err = u.Refresh(100, timestamp(7, 3000, 9), nil, snapshot(9, 3000, 1), nil, targets(1, 3000), nil)
	if !errors.Is(err, ErrClockRollback) {
		t.Fatalf("clock rollback priority: %v", err)
	}
	if u.lastClock != 200 {
		t.Fatalf("clock rollback moved clock to %d", u.lastClock)
	}
}

func TestVerify(t *testing.T) {
	u := newTestUpdater(t)
	if err := u.Verify("p", 1, "h"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("not ready: %v", err)
	}
	if err := u.Refresh(100, timestamp(1, 2000, 1), []string{"T"}, snapshot(1, 2000, 1), []string{"S"}, targets(1, 2000), []string{"G"}); err != nil {
		t.Fatal(err)
	}
	if err := u.Verify("missing", 1, "h"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if err := u.Verify("p", 2, "h"); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("length: %v", err)
	}
	if err := u.Verify("p", 1, "bad"); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("hash: %v", err)
	}
	if err := u.Verify("p", 1, "h"); err != nil {
		t.Fatalf("valid: %v", err)
	}
}

func TestClearAllowsLowerTimestampAndRootCheckLimit(t *testing.T) {
	u := newTestUpdater(t)
	u.tsVersion, u.snapVersion, u.tgtVersion = 5, 7, 3
	index, err := u.UpdateRoot(10, []SignedRoot{{timestampRoot(2, 2000, "T2"), []string{"A", "C", "D"}}})
	if index != 0 || err != nil || u.rootChecks != 2 {
		t.Fatalf("rotation index=%d err=%v checks=%d", index, err, u.rootChecks)
	}
	err = u.Refresh(20, timestamp(1, 2000, 1), []string{"T2"}, snapshot(1, 2000, 3), []string{"S"}, targets(3, 2000), []string{"G"})
	if err != nil {
		t.Fatal(err)
	}
	if u.tsVersion != 1 || u.snapVersion != 1 || u.tgtVersion != 3 {
		t.Fatalf("post-clear versions=%d,%d,%d", u.tsVersion, u.snapVersion, u.tgtVersion)
	}
}

func TestConcurrentOperationsAreSerialized(t *testing.T) {
	u := newTestUpdater(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 32 {
			_, _ = u.UpdateRoot(10, []SignedRoot{{nextRoot(2, 2000), []string{"A", "C"}}})
		}
	}()
	for range 32 {
		_ = u.Verify("p", 1, "h")
	}
	<-done
	if u.root.Version != 1 || u.files != nil {
		t.Fatalf("concurrent failed operations changed state: root=%d files=%v", u.root.Version, u.files)
	}
}

func TestRoleSpecificErrorsAndWholeChainValidation(t *testing.T) {
	u := newTestUpdater(t)
	u.tsVersion, u.snapVersion, u.tgtVersion = 5, 7, 3
	_, err := u.UpdateRoot(10, []SignedRoot{{nextRoot(2, 2000), []string{"A", "A", "D"}}})
	var roleErr roleError
	if !errors.As(err, &roleErr) || roleErr.role != meta.RoleRoot {
		t.Fatalf("root error role=%v err=%v", roleErr.role, err)
	}

	err = u.Refresh(100, timestamp(4, 2000, 8), []string{"T"}, snapshot(8, 2000, 3), []string{"S"}, targets(3, 2000), []string{"G"})
	if !errors.As(err, &roleErr) || roleErr.role != meta.RoleTimestamp {
		t.Fatalf("timestamp error role=%v err=%v", roleErr.role, err)
	}
	err = u.Refresh(100, timestamp(7, 2000, 6), []string{"T"}, snapshot(6, 2000, 3), []string{"S"}, targets(3, 2000), []string{"G"})
	if !errors.As(err, &roleErr) || roleErr.role != meta.RoleSnapshot {
		t.Fatalf("snapshot error role=%v err=%v", roleErr.role, err)
	}
	err = u.Refresh(100, timestamp(7, 2000, 9), []string{"T"}, snapshot(9, 2000, 4), []string{"S"}, targets(3, 2000), []string{"G"})
	if !errors.As(err, &roleErr) || roleErr.role != meta.RoleTargets {
		t.Fatalf("targets error role=%v err=%v", roleErr.role, err)
	}

	index, err := u.UpdateRoot(200, []SignedRoot{
		{nextRoot(2, 2000), []string{"A", "C", "D"}},
		{baseRoot(0, 2000), []string{"A"}},
	})
	if index != -1 || !errors.Is(err, ErrInvalidArgument) || u.root.Version != 1 {
		t.Fatalf("whole-chain validation index=%d err=%v root=%d", index, err, u.root.Version)
	}

	if _, err := New(baseRoot(1, 1000), []string{"A"}); err == nil {
		t.Fatal("New accepted root below its threshold")
	}
	if _, err := New(baseRoot(1, 1000), []string{"A", "B"}); err != nil {
		t.Fatal(err)
	}
}
