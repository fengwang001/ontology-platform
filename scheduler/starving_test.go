package scheduler

import "testing"

// buildStarvingScenario drives the scheduler into the state:
//
//	expired = [X] with expiredTs = t0, cur = B (interactive at expiry,
//	ts_left = 500-k), plus `fillers` extra non-sleeping queued tasks,
//	so nr = 2 + fillers at B's expiry.
//
// B is a nice=-5 task (ts=500) that ran k ticks, slept 1000 ticks (so
// s=1000 at wake, still >= 700 when it expires 500-k ticks later), and
// was queued behind X (nice=-20, prio 105) so it could not preempt. X
// expires while B waits in the active array, which lands X in the
// expired array and makes B the current task with a known ts_left; B's
// expiry then happens exactly 500-k ticks after t0.
func buildStarvingScenario(t *testing.T, k, fillers int) (*Scheduler, int) {
	t.Helper()
	s := New(8)
	if fillers > 0 {
		mustOK(t, s.Spawn(3, 0)) // W: sits in the active array, never runs
	}
	mustOK(t, s.Spawn(2, -5))  // B: prio 120, ts 500
	tickN(s, k)                // B runs k ticks: ts_left = 500-k
	mustOK(t, s.Sleep())       // B sleeps at now=k
	mustOK(t, s.Spawn(1, -20)) // X: prio 105, ts 800
	tickN(s, 1000)             // B sleeps 1000 ticks
	mustOK(t, s.Wake(2))       // B: s=1000, prio 110, queued behind X
	// Tick until B becomes the current task (X just expired into the
	// expired array, so expiredTs is the t0 we need).
	for {
		if cur, _ := s.Current(); cur == 2 {
			break
		}
		s.Tick()
	}
	t0 := s.ExpiredTs()
	if t0 == 0 {
		t.Fatalf("scenario broken: expired array empty when B dispatched")
	}
	info, _ := s.State(2)
	if info.TsLeft != 500-k || info.S != 1000 {
		t.Fatalf("scenario broken: B state %+v, want ts_left=%d s=1000", info, 500-k)
	}
	return s, t0
}

// TestStarvingBoundary checks that starving is true exactly when
// now-expiredTs == 100*nr and false one tick earlier, for nr=2 and nr=3,
// and that a starving interactive task is sent to the expired array.
func TestStarvingBoundary(t *testing.T) {
	cases := []struct {
		name    string
		k       int // B's pre-sleep run ticks; B expires 500-k ticks after t0
		fillers int // extra non-sleeping tasks; nr = 2+fillers
	}{
		{"nr2_equal_true", 300, 0},   // 200 >= 100*2
		{"nr2_minus1_false", 301, 0}, // 199 < 200
		{"nr3_equal_true", 200, 1},   // 300 >= 100*3
		{"nr3_minus1_false", 201, 1}, // 299 < 300
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, t0 := buildStarvingScenario(t, c.k, c.fillers)
			nr := 2 + c.fillers
			runLeft := 500 - c.k
			wantStarving := runLeft >= StarveUnit*nr
			expiryTick := t0 + runLeft
			t.Logf("输入: k=%d fillers=%d t0=%d runLeft=%d nr=%d 判定依据: starving 当且仅当 now-expiredTs=%d >= 100*nr=%d => %v",
				c.k, c.fillers, t0, runLeft, nr, runLeft, StarveUnit*nr, wantStarving)
			tickN(s, runLeft) // B expires on the last tick
			if s.Now() != expiryTick {
				t.Fatalf("now=%d want %d", s.Now(), expiryTick)
			}
			info, _ := s.State(2)
			if !Interactive(info.S) {
				t.Fatalf("scenario broken: B not interactive at expiry, s=%d", info.S)
			}
			if wantStarving {
				// Interactive but starving: B went to the expired array,
				// so the CPU moved on to another task.
				if cur, _ := s.Current(); cur == 2 {
					t.Fatalf("starving: B should have left the CPU (entered expired)")
				}
				t.Logf("输出: cur=%d B=%+v 判定: starving 为真, 交互任务进入过期队列", mustCurID(s), info)
			} else {
				// Not starving: interactive B returned to the active
				// array and, having the best prio, keeps running.
				mustCurrent(t, s, 2)
				if got := s.ExpiredTs(); got != t0 {
					t.Fatalf("expiredTs=%d want %d (unchanged)", got, t0)
				}
				t.Logf("输出: cur=2 B=%+v 判定: starving 为假, 交互任务回到活动队列继续运行", info)
			}
		})
	}
}

func mustCurID(s *Scheduler) int {
	id, _ := s.Current()
	return id
}

// TestStarvingQueuePlacement verifies the exact queue placement in the
// nr=2 true case: B lands in the expired array behind X, and the swap
// then moves both to the active array with X dispatched first.
func TestStarvingQueuePlacement(t *testing.T) {
	s, t0 := buildStarvingScenario(t, 300, 0)
	tickN(s, 200) // B expires at t0+200, starving (200 >= 100*2)
	// B (interactive, prio 113) was appended to expired behind X (105);
	// the active array was empty, so the arrays swapped: X runs, B waits
	// in what is now the active array.
	mustCurrent(t, s, 1)
	if got := s.ExpiredTs(); got != 0 {
		t.Fatalf("expiredTs=%d want 0 after swap", got)
	}
	mustState(t, s, 2, TaskInfo{State: Queued, Prio: 112, TsLeft: 500, S: 800})
	mustQueues(t, s, []PriorityQueue{pq(112, 2)}, nil)
	_ = t0
}
