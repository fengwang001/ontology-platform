package stm

import (
	"errors"
	"reflect"
	"testing"
)

func mustManager(t *testing.T, cfg Config) *Manager {
	t.Helper()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager(%+v): %v", cfg, err)
	}
	return m
}

func mustOpen(t *testing.T, m *Manager, id, o int, write bool) OpenResult {
	t.Helper()
	res, err := m.Open(id, o, write)
	if err != nil {
		t.Fatalf("Open(%d, %d, %v): %v", id, o, write, err)
	}
	return res
}

func mustWait(t *testing.T, m *Manager, id, o int, write bool, wantDelay int64) {
	t.Helper()
	res := mustOpen(t, m, id, o, write)
	if !res.Wait || res.Delay != wantDelay || len(res.Aborted) != 0 {
		t.Fatalf("Open(%d, %d, %v) = %+v, want wait with delay %d", id, o, write, res, wantDelay)
	}
}

func mustAbortWin(t *testing.T, m *Manager, id, o int, write bool, want ...int) {
	t.Helper()
	res := mustOpen(t, m, id, o, write)
	if res.Wait || !reflect.DeepEqual(res.Aborted, want) {
		t.Fatalf("Open(%d, %d, %v) = %+v, want aborted %v", id, o, write, res, want)
	}
}

func mustGrant(t *testing.T, m *Manager, id, o int, write bool) {
	t.Helper()
	res := mustOpen(t, m, id, o, write)
	if res.Wait || len(res.Aborted) != 0 {
		t.Fatalf("Open(%d, %d, %v) = %+v, want plain grant", id, o, write, res)
	}
}

func checkStats(t *testing.T, m *Manager, id, wantKp, wantAb int) {
	t.Helper()
	kp, ab, err := m.Stats(id)
	if err != nil {
		t.Fatalf("Stats(%d): %v", id, err)
	}
	if kp != wantKp || ab != wantAb {
		t.Fatalf("Stats(%d) = (kp=%d, ab=%d), want (kp=%d, ab=%d)", id, kp, ab, wantKp, wantAb)
	}
}

func mustRestart(t *testing.T, m *Manager, id int, wantDelay int64) {
	t.Helper()
	d, err := m.Restart(id)
	if err != nil {
		t.Fatalf("Restart(%d): %v", id, err)
	}
	if d != wantDelay {
		t.Fatalf("Restart(%d) delay = %d, want %d", id, d, wantDelay)
	}
}

// baseCfg returns a config with no privilege (L=16), no forcing (Q=16),
// no score cap pressure (P=1000) and no delay cap (E=20), overridable per
// test.
func baseCfg() Config {
	return Config{Objects: 8, PrivThreshold: 16, BaseDelay: 10, ExpCap: 20, ScoreCap: 1000, ForceThreshold: 16}
}

// TestSpecExample replays the worked example from the specification:
// M=2, L=2, D=10, E=3, P=100, Q=16.
func TestSpecExample(t *testing.T) {
	m := mustManager(t, Config{Objects: 2, PrivThreshold: 2, BaseDelay: 10, ExpCap: 3, ScoreCap: 100, ForceThreshold: 16})
	t1 := m.Begin()
	t2 := m.Begin()

	mustGrant(t, m, t1, 0, true)
	mustGrant(t, m, t2, 1, true)
	checkStats(t, m, t1, 1, 0)
	checkStats(t, m, t2, 1, 0)

	// k=0: kp(t2)+0 = 1 is not greater than kp(t1)=1, wait 10.
	mustWait(t, m, t2, 0, true, 10)
	// k=1: 1+1 = 2 > 1, t1 is aborted (ab=1, kp=ceil(1/2)=1), t2 scores.
	mustAbortWin(t, m, t2, 0, true, t1)
	checkStats(t, m, t1, 1, 1)
	checkStats(t, m, t2, 2, 0)

	mustRestart(t, m, t1, 10)
	// t1 faces kp(t2)=2: k=0 -> 1, k=1 -> 2, both not greater than 2.
	mustWait(t, m, t1, 0, true, 10)
	mustWait(t, m, t1, 0, true, 20)
	// k=2: 1+2 = 3 > 2, t2 is aborted (ab=1, kp=ceil(2/2)=1), t1 scores.
	mustAbortWin(t, m, t1, 0, true, t2)
	checkStats(t, m, t1, 2, 1)
	checkStats(t, m, t2, 1, 1)
}

// TestScoreTieLosesByOneWins: kp(t)+k equal to kp(e) never wins; one
// more point of kp does.
func TestScoreTieLosesByOneWins(t *testing.T) {
	cfg := baseCfg()
	cfg.Objects = 5
	m := mustManager(t, cfg)
	t1 := m.Begin()
	t2 := m.Begin()
	mustGrant(t, m, t1, 0, true)
	mustGrant(t, m, t1, 1, true) // kp(t1) = 2
	mustGrant(t, m, t2, 2, true)
	mustGrant(t, m, t2, 3, true) // kp(t2) = 2

	// Equal scores, k=0: 2+0 is not > 2, wait.
	mustWait(t, m, t2, 0, true, 10)
	// t2 earns one more point elsewhere.
	mustGrant(t, m, t2, 4, true) // kp(t2) = 3
	// On a fresh object (att=0): 3+0 > 2, t1 is aborted.
	mustAbortWin(t, m, t2, 1, true, t1)
	checkStats(t, m, t1, 1, 1) // ceil(2/2)
	checkStats(t, m, t2, 4, 0)
}

// TestRetryCounterOvertakesStronger: a weak transaction eventually
// prevails purely through att accumulating over retries.
func TestRetryCounterOvertakesStronger(t *testing.T) {
	cfg := baseCfg()
	cfg.BaseDelay = 2
	m := mustManager(t, cfg)
	t1 := m.Begin()
	t2 := m.Begin()
	mustGrant(t, m, t1, 0, true)
	mustGrant(t, m, t1, 1, true)
	mustGrant(t, m, t1, 2, true) // kp(t1) = 3

	// kp(t2)=0; needs k such that 0+k > 3, i.e. k=4.
	mustWait(t, m, t2, 0, true, 2)  // k=0
	mustWait(t, m, t2, 0, true, 4)  // k=1
	mustWait(t, m, t2, 0, true, 8)  // k=2
	mustWait(t, m, t2, 0, true, 16) // k=3
	mustAbortWin(t, m, t2, 0, true, t1)
	checkStats(t, m, t1, 2, 1) // ceil(3/2)
	checkStats(t, m, t2, 1, 0)
}

// TestAttClearedOnGrantAndRestart: after winning an object and later
// being aborted and restarted, the retry counter starts again from 0
// (first wait costs D, not D*2^k).
func TestAttClearedOnGrantAndRestart(t *testing.T) {
	cfg := baseCfg()
	m := mustManager(t, cfg)
	t1 := m.Begin()
	t2 := m.Begin()
	mustGrant(t, m, t1, 0, true) // kp(t1) = 1

	// t2 (kp=0) accumulates att on object 0 up to k=2, then wins.
	mustWait(t, m, t2, 0, true, 10) // k=0
	mustWait(t, m, t2, 0, true, 20) // k=1
	mustAbortWin(t, m, t2, 0, true, t1)
	checkStats(t, m, t2, 1, 0)

	// t1 restarts and takes the object back: kp(t1)=1 (ceil(1/2)) vs
	// kp(t2)=1, so t1 waits at k=0 and wins at k=1.
	mustRestart(t, m, t1, 10)
	mustWait(t, m, t1, 0, true, 10)
	mustAbortWin(t, m, t1, 0, true, t2)
	checkStats(t, m, t1, 2, 1)
	checkStats(t, m, t2, 1, 1)

	// t2 restarts: att must have been cleared (by the grant, the abort
	// and the restart), so its first wait costs D, not D*2^k.
	mustRestart(t, m, t2, 10)
	mustWait(t, m, t2, 0, true, 10) // k=0 again
	mustWait(t, m, t2, 0, true, 20) // k=1
	mustAbortWin(t, m, t2, 0, true, t1)
}

// TestCeilHalfOnAbort: aborted transactions keep ceil(kp/2), checked for
// odd and even scores.
func TestCeilHalfOnAbort(t *testing.T) {
	cfg := baseCfg()
	cfg.ForceThreshold = 1 // k=1 beats any non-privileged adversary
	m := mustManager(t, cfg)

	// Odd score: kp=3 -> ceil(3/2) = 2.
	t1 := m.Begin()
	mustGrant(t, m, t1, 0, true)
	mustGrant(t, m, t1, 1, true)
	mustGrant(t, m, t1, 2, true)
	t2 := m.Begin()
	mustWait(t, m, t2, 0, true, 10) // k=0: 0>=1 false, 0>3 false
	mustAbortWin(t, m, t2, 0, true, t1)
	checkStats(t, m, t1, 2, 1)

	// Even score: kp=4 -> ceil(4/2) = 2.
	t3 := m.Begin()
	mustGrant(t, m, t3, 3, true)
	mustGrant(t, m, t3, 4, true)
	mustGrant(t, m, t3, 5, true)
	mustGrant(t, m, t3, 6, true)
	t4 := m.Begin()
	mustWait(t, m, t4, 3, true, 10)
	mustAbortWin(t, m, t4, 3, true, t3)
	checkStats(t, m, t3, 2, 1)

	// Score one: kp=1 -> ceil(1/2) = 1.
	t5 := m.Begin()
	mustGrant(t, m, t5, 7, true)
	t6 := m.Begin()
	mustWait(t, m, t6, 7, true, 10)
	mustAbortWin(t, m, t6, 7, true, t5)
	checkStats(t, m, t5, 1, 1)
}

// TestRestartKeepsScoreDropsHolds: Restart preserves kp and ab but
// releases every held object.
func TestRestartKeepsScoreDropsHolds(t *testing.T) {
	cfg := baseCfg()
	cfg.ForceThreshold = 1
	m := mustManager(t, cfg)
	t1 := m.Begin()
	mustGrant(t, m, t1, 0, true)
	mustGrant(t, m, t1, 1, true)
	mustGrant(t, m, t1, 2, true) // kp=3
	t2 := m.Begin()
	mustWait(t, m, t2, 0, true, 10)
	mustAbortWin(t, m, t2, 0, true, t1) // t1: kp=2, ab=1

	mustRestart(t, m, t1, 10)
	checkStats(t, m, t1, 2, 1) // kp and ab preserved
	holds, err := m.Holds(t1)
	if err != nil {
		t.Fatalf("Holds(%d): %v", t1, err)
	}
	if len(holds) != 0 {
		t.Fatalf("Holds(%d) = %v, want empty after restart", t1, holds)
	}
	// The formerly held objects 1 and 2 are free: a third transaction
	// takes them without aborting anyone.
	t3 := m.Begin()
	mustGrant(t, m, t3, 1, true)
	mustGrant(t, m, t3, 2, true)
}

// TestReadUpgradeNoExtraScore: upgrading a read to a write, or
// re-opening an already held object, does not score again.
func TestReadUpgradeNoExtraScore(t *testing.T) {
	cfg := baseCfg()
	m := mustManager(t, cfg)
	t1 := m.Begin()
	mustGrant(t, m, t1, 0, false) // read, kp=1
	checkStats(t, m, t1, 1, 0)
	mustGrant(t, m, t1, 0, false) // re-read: no change
	checkStats(t, m, t1, 1, 0)
	mustGrant(t, m, t1, 0, true) // upgrade to write: no new point
	checkStats(t, m, t1, 1, 0)
	holds, err := m.Holds(t1)
	if err != nil {
		t.Fatalf("Holds(%d): %v", t1, err)
	}
	if holds[0] != ModeWrite {
		t.Fatalf("Holds(%d)[0] = %v, want ModeWrite", t1, holds[0])
	}
	mustGrant(t, m, t1, 0, false) // write satisfies read: no change
	checkStats(t, m, t1, 1, 0)
}

// TestScoreCapTieDoesNotWin: with kp capped at P, a transaction that
// "would" have more points still only ties and does not win.
func TestScoreCapTieDoesNotWin(t *testing.T) {
	cfg := baseCfg()
	cfg.Objects = 6
	cfg.ScoreCap = 2
	m := mustManager(t, cfg)
	t1 := m.Begin()
	t2 := m.Begin()
	mustGrant(t, m, t1, 0, true)
	mustGrant(t, m, t1, 1, true) // kp(t1) = 2
	mustGrant(t, m, t2, 2, true)
	mustGrant(t, m, t2, 3, true)
	mustGrant(t, m, t2, 4, true)
	mustGrant(t, m, t2, 5, true) // kp(t2) capped at 2
	checkStats(t, m, t2, 2, 0)
	// 2+0 is not > 2: the capped score only ties and loses.
	mustWait(t, m, t2, 0, true, 10)
	// Without the cap kp(t2) would be 4 and have won at once; capped at
	// 2 it needs k=1 so that 2+1 > 2.
	mustAbortWin(t, m, t2, 0, true, t1)
	checkStats(t, m, t1, 1, 1)
}

// TestForceThresholdQ: at k >= Q a non-privileged transaction forces a
// win over a stronger non-privileged adversary, but never over a
// privileged one.
func TestForceThresholdQ(t *testing.T) {
	cfg := baseCfg()
	cfg.PrivThreshold = 1
	cfg.ForceThreshold = 3
	m := mustManager(t, cfg)

	// Non-privileged adversary: k reaching Q wins despite lower score.
	t1 := m.Begin()
	for o := 0; o < 5; o++ {
		mustGrant(t, m, t1, o, true) // kp(t1) = 5
	}
	t2 := m.Begin()
	mustWait(t, m, t2, 0, true, 10) // k=0
	mustWait(t, m, t2, 0, true, 20) // k=1
	mustWait(t, m, t2, 0, true, 40) // k=2
	mustAbortWin(t, m, t2, 0, true, t1)
	checkStats(t, m, t1, 3, 1) // ceil(5/2)

	// Privileged adversary: k >= Q does not help.
	t3 := m.Begin()
	mustGrant(t, m, t3, 5, true) // kp(t3) = 1
	t4 := m.Begin()
	mustGrant(t, m, t4, 6, true) // kp(t4) = 1
	mustWait(t, m, t4, 5, true, 10)
	mustAbortWin(t, m, t4, 5, true, t3) // t3: ab=1 -> privileged
	mustRestart(t, m, t3, 10)
	mustGrant(t, m, t3, 7, true) // kp(t3) = ceil(1/2)+1 = 2
	t5 := m.Begin()
	mustWait(t, m, t5, 7, true, 10)  // k=0
	mustWait(t, m, t5, 7, true, 20)  // k=1
	mustWait(t, m, t5, 7, true, 40)  // k=2
	mustWait(t, m, t5, 7, true, 80)  // k=3 >= Q, still loses to privilege
	mustWait(t, m, t5, 7, true, 160) // k=4
}

// TestWriteAgainstMultipleReaders: a writer must beat every reader;
// otherwise none of them is aborted.
func TestWriteAgainstMultipleReaders(t *testing.T) {
	cfg := baseCfg()
	m := mustManager(t, cfg)
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()
	t4 := m.Begin()
	mustGrant(t, m, t1, 0, false)   // reader, kp=1
	mustGrant(t, m, t2, 0, false)   // reader
	mustGrant(t, m, t2, 1, true)    // kp=2
	mustGrant(t, m, t2, 2, true)    // kp=3
	mustGrant(t, m, t3, 0, false)   // reader, kp=1
	mustGrant(t, m, t4, 3, true)    // kp=1
	mustGrant(t, m, t4, 4, true)    // kp=2
	mustWait(t, m, t4, 0, true, 10) // beats t1 (2>1), loses to t2 (2>3 no)
	checkStats(t, m, t1, 1, 0)      // untouched
	checkStats(t, m, t2, 3, 0)      // untouched
	checkStats(t, m, t3, 1, 0)      // untouched
	if holds, _ := m.Holds(t1); holds[0] != ModeRead {
		t.Fatalf("t1 lost its read hold after a failed enemy Open")
	}
	mustWait(t, m, t4, 0, true, 20) // k=1: 3>3 no
	mustAbortWin(t, m, t4, 0, true, t1, t2, t3)
	checkStats(t, m, t1, 1, 1) // ceil(1/2)
	checkStats(t, m, t2, 2, 1) // ceil(3/2)
	checkStats(t, m, t3, 1, 1)
	checkStats(t, m, t4, 3, 0)
}

// TestPrivilegeBeatsNonPrivilege: a privileged transaction wins against
// any non-privileged adversary regardless of scores and k.
func TestPrivilegeBeatsNonPrivilege(t *testing.T) {
	cfg := baseCfg()
	cfg.PrivThreshold = 1
	m := mustManager(t, cfg)
	t1 := m.Begin()
	mustGrant(t, m, t1, 0, true) // kp=1
	t3 := m.Begin()
	mustGrant(t, m, t3, 1, true) // kp=1
	mustWait(t, m, t3, 0, true, 10)
	mustAbortWin(t, m, t3, 0, true, t1) // t1: ab=1 -> privileged
	mustRestart(t, m, t1, 10)

	t2 := m.Begin()
	for o := 2; o < 7; o++ {
		mustGrant(t, m, t2, o, true) // kp(t2) = 5
	}
	// kp(t1)=1, k=0: privilege wins over kp=5 instantly.
	mustAbortWin(t, m, t1, 2, true, t2)
	checkStats(t, m, t2, 3, 1) // ceil(5/2)
}

// TestDoublePrivilegeByID: between two privileged transactions the
// smaller transaction id wins.
func TestDoublePrivilegeByID(t *testing.T) {
	cfg := baseCfg()
	cfg.PrivThreshold = 1
	m := mustManager(t, cfg)
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()
	mustGrant(t, m, t1, 0, true) // kp=1
	mustGrant(t, m, t2, 1, true) // kp=1
	mustGrant(t, m, t3, 2, true)
	mustGrant(t, m, t3, 3, true)        // kp(t3)=2
	mustAbortWin(t, m, t3, 0, true, t1) // t1: ab=1 -> privileged
	mustAbortWin(t, m, t3, 1, true, t2) // t2: ab=1 -> privileged
	mustRestart(t, m, t1, 10)
	mustRestart(t, m, t2, 10)

	mustGrant(t, m, t2, 4, true) // kp(t2) = ceil(1/2)+1 = 2
	// Both privileged: smaller id (t1) wins.
	mustAbortWin(t, m, t1, 4, true, t2)
	checkStats(t, m, t2, 1, 2) // ceil(2/2), second abort
	mustRestart(t, m, t2, 20)  // delay uses ab-1 = 1
	// Larger id (t2) loses and waits.
	mustWait(t, m, t2, 4, true, 10)
	mustWait(t, m, t2, 4, true, 20)
}

// TestPrivilegeStartsAtNextConflict: privilege takes effect from the
// first conflict after ab reaches L, not before.
func TestPrivilegeStartsAtNextConflict(t *testing.T) {
	cfg := baseCfg()
	cfg.PrivThreshold = 2
	m := mustManager(t, cfg)
	t1 := m.Begin()
	mustGrant(t, m, t1, 0, true) // kp=1
	t2 := m.Begin()
	for o := 1; o < 6; o++ {
		mustGrant(t, m, t2, o, true) // kp(t2) = 5
	}
	t3 := m.Begin()
	mustGrant(t, m, t3, 6, true) // kp=1
	mustWait(t, m, t3, 0, true, 10)
	mustAbortWin(t, m, t3, 0, true, t1) // t1: ab=1, below L=2
	mustRestart(t, m, t1, 10)

	// ab=1 < L: t1 is not privileged yet and loses to kp=5 on score.
	mustWait(t, m, t1, 1, true, 10)

	// Abort t1 once more so that ab reaches L.
	mustGrant(t, m, t1, 7, true) // kp(t1) = ceil(1/2)+1 = 2
	t4 := m.Begin()
	mustWait(t, m, t4, 7, true, 10) // k=0: 0>2 no
	mustWait(t, m, t4, 7, true, 20) // k=1: 1>2 no
	mustWait(t, m, t4, 7, true, 40) // k=2: 2>2 no
	mustAbortWin(t, m, t4, 7, true, t1)
	checkStats(t, m, t1, 1, 2) // ab reaches L=2
	mustRestart(t, m, t1, 20)  // delay uses ab-1 = 1

	// From the next conflict on, t1 is privileged and beats kp=5 at k=0.
	mustAbortWin(t, m, t1, 1, true, t2)
	checkStats(t, m, t2, 3, 1) // ceil(5/2)
}

// TestDelayCap: the exponential delay is capped at 2^E.
func TestDelayCap(t *testing.T) {
	cfg := baseCfg()
	cfg.Objects = 12
	cfg.ExpCap = 2
	m := mustManager(t, cfg)
	t1 := m.Begin()
	for o := 0; o < 10; o++ {
		mustGrant(t, m, t1, o, true) // kp(t1) = 10
	}
	t2 := m.Begin()
	// t2 (kp=0) keeps losing until k > 10; delays cap at 10*2^2 = 40.
	mustWait(t, m, t2, 0, true, 10) // k=0
	mustWait(t, m, t2, 0, true, 20) // k=1
	mustWait(t, m, t2, 0, true, 40) // k=2
	mustWait(t, m, t2, 0, true, 40) // k=3, capped
	mustWait(t, m, t2, 0, true, 40) // k=4, capped
}

// TestRestartDelayUsesAbMinusOne: Restart backs off by
// D * 2^min(max(ab-1, 0), E).
func TestRestartDelayUsesAbMinusOne(t *testing.T) {
	cfg := baseCfg()
	cfg.Objects = 64
	cfg.ExpCap = 3
	cfg.ForceThreshold = 1 // any fresh attacker wins its second try
	m := mustManager(t, cfg)

	// ab=0 (voluntary abort): delay D * 2^0.
	t1 := m.Begin()
	if err := m.Abort(t1); err != nil {
		t.Fatalf("Abort(%d): %v", t1, err)
	}
	mustRestart(t, m, t1, 10)

	// ab = 1..5: delays 10, 20, 40, 80, 80 (exponent ab-1 capped at E=3).
	want := []int64{10, 20, 40, 80, 80}
	for i, w := range want {
		ab := i + 1
		victim := m.Begin()
		for c := 0; c < ab; c++ {
			obj := 10 + i*8 + c // fresh object: previous attackers keep theirs
			mustGrant(t, m, victim, obj, true)
			attacker := m.Begin()
			mustWait(t, m, attacker, obj, true, 10) // k=0: 0>=1 false
			mustAbortWin(t, m, attacker, obj, true, victim)
			if c < ab-1 {
				if _, err := m.Restart(victim); err != nil {
					t.Fatalf("Restart(%d): %v", victim, err)
				}
			}
		}
		mustRestart(t, m, victim, w)
	}
}

// TestInvalidConfig: any out-of-range field rejects the whole
// configuration; boundary values are accepted.
func TestInvalidConfig(t *testing.T) {
	valid := Config{Objects: 8, PrivThreshold: 2, BaseDelay: 10, ExpCap: 3, ScoreCap: 100, ForceThreshold: 16}
	bad := []Config{}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Objects = 0 },
		func(c *Config) { c.Objects = 65 },
		func(c *Config) { c.PrivThreshold = 0 },
		func(c *Config) { c.PrivThreshold = 17 },
		func(c *Config) { c.BaseDelay = 0 },
		func(c *Config) { c.BaseDelay = 1001 },
		func(c *Config) { c.ExpCap = -1 },
		func(c *Config) { c.ExpCap = 21 },
		func(c *Config) { c.ScoreCap = 0 },
		func(c *Config) { c.ScoreCap = 1001 },
		func(c *Config) { c.ForceThreshold = 0 },
		func(c *Config) { c.ForceThreshold = 17 },
	} {
		c := valid
		mutate(&c)
		bad = append(bad, c)
	}
	for _, c := range bad {
		if _, err := NewManager(c); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("NewManager(%+v) = %v, want ErrInvalidConfig", c, err)
		}
	}
	for _, c := range []Config{
		{Objects: 1, PrivThreshold: 1, BaseDelay: 1, ExpCap: 0, ScoreCap: 1, ForceThreshold: 1},
		{Objects: 64, PrivThreshold: 16, BaseDelay: 1000, ExpCap: 20, ScoreCap: 1000, ForceThreshold: 16},
		valid,
	} {
		if _, err := NewManager(c); err != nil {
			t.Fatalf("NewManager(%+v) = %v, want nil", c, err)
		}
	}
}

// TestRejections: rejection reasons are reported in the order unknown
// transaction, bad state, bad object, and rejected calls change nothing.
func TestRejections(t *testing.T) {
	cfg := baseCfg()
	m := mustManager(t, cfg)
	t1 := m.Begin()
	mustGrant(t, m, t1, 0, true) // kp=1
	t2 := m.Begin()
	mustWait(t, m, t2, 0, true, 10) // att[0] = 1

	// Unknown transaction wins over every other reason.
	if _, err := m.Open(999, 999, true); !errors.Is(err, ErrNoSuchTxn) {
		t.Fatalf("Open(999, 999) = %v, want ErrNoSuchTxn", err)
	}
	if _, err := m.Open(0, 0, true); !errors.Is(err, ErrNoSuchTxn) {
		t.Fatalf("Open(0, 0) = %v, want ErrNoSuchTxn", err)
	}
	if err := m.Commit(999); !errors.Is(err, ErrNoSuchTxn) {
		t.Fatalf("Commit(999) = %v, want ErrNoSuchTxn", err)
	}
	if _, _, err := m.Stats(999); !errors.Is(err, ErrNoSuchTxn) {
		t.Fatalf("Stats(999) = %v, want ErrNoSuchTxn", err)
	}

	// Active transaction: Restart reports bad state.
	if _, err := m.Restart(t2); !errors.Is(err, ErrBadState) {
		t.Fatalf("Restart(active) = %v, want ErrBadState", err)
	}

	// Object out of range (only Open).
	if _, err := m.Open(t2, -1, true); !errors.Is(err, ErrNoSuchObject) {
		t.Fatalf("Open(t2, -1) = %v, want ErrNoSuchObject", err)
	}
	if _, err := m.Open(t2, cfg.Objects, true); !errors.Is(err, ErrNoSuchObject) {
		t.Fatalf("Open(t2, M) = %v, want ErrNoSuchObject", err)
	}

	// The rejected calls above changed nothing: t2's retry counter is
	// still 1, so the next wait costs D*2^1, and scores are untouched.
	mustWait(t, m, t2, 0, true, 20)
	checkStats(t, m, t2, 0, 0)

	// Committed transaction: every call reports bad state, and state is
	// checked before the object range.
	if err := m.Commit(t1); err != nil {
		t.Fatalf("Commit(%d): %v", t1, err)
	}
	if _, err := m.Open(t1, 999, true); !errors.Is(err, ErrBadState) {
		t.Fatalf("Open(committed, 999) = %v, want ErrBadState", err)
	}
	if err := m.Commit(t1); !errors.Is(err, ErrBadState) {
		t.Fatalf("Commit(committed) = %v, want ErrBadState", err)
	}
	if err := m.Abort(t1); !errors.Is(err, ErrBadState) {
		t.Fatalf("Abort(committed) = %v, want ErrBadState", err)
	}
	if _, err := m.Restart(t1); !errors.Is(err, ErrBadState) {
		t.Fatalf("Restart(committed) = %v, want ErrBadState", err)
	}

	// A transaction aborted by another cannot open, commit or abort until
	// it restarts.
	t3 := m.Begin()
	mustGrant(t, m, t3, 1, true) // kp=1
	t4 := m.Begin()
	mustWait(t, m, t4, 1, true, 10) // k=0: 0>1 no
	mustWait(t, m, t4, 1, true, 20) // k=1: 1>1 no
	mustAbortWin(t, m, t4, 1, true, t3)
	if _, err := m.Open(t3, 2, true); !errors.Is(err, ErrBadState) {
		t.Fatalf("Open(aborted) = %v, want ErrBadState", err)
	}
	if err := m.Commit(t3); !errors.Is(err, ErrBadState) {
		t.Fatalf("Commit(aborted) = %v, want ErrBadState", err)
	}
	if err := m.Abort(t3); !errors.Is(err, ErrBadState) {
		t.Fatalf("Abort(aborted) = %v, want ErrBadState", err)
	}
	mustRestart(t, m, t3, 10) // ab=1 -> delay D*2^0
	mustGrant(t, m, t3, 2, true)
}
