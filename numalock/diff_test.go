package numalock

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

// genConfig builds a random valid configuration.
func genConfig(rng *rand.Rand) (int, int, []int, int) {
	m := 1 + rng.Intn(8)
	t := 1 + rng.Intn(32)
	b := 1 + rng.Intn(16)
	nodeOf := make([]int, t)
	for i := range nodeOf {
		nodeOf[i] = rng.Intn(m)
	}
	return m, t, nodeOf, b
}

func opNames() []string {
	return []string{"RLock", "RUnlock", "WLock", "WUnlock", "Downgrade", "Upgrade", "Cancel"}
}

// TestRandomDifferential runs 2000 random call sequences against both the
// real lock and the naive transcription, comparing full state, OK flag and
// granted sets after every call. Inputs, outputs and decision bases are
// logged.
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	var failLog bytes.Buffer
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq) + 1))
		m, tc, nodeOf, b := genConfig(rng)
		lock, err := New(m, tc, nodeOf, b)
		if err != nil {
			t.Fatalf("seq %d 合法配置被拒: %v", seq, err)
		}
		model := newNaive(m, tc, nodeOf, b)

		fmt.Fprintf(&failLog, "===== 序列 %d: M=%d T=%d B=%d nodeOf=%v =====\n",
			seq, m, tc, b, nodeOf)

		steps := 20 + rng.Intn(60)
		for i := 0; i < steps; i++ {
			c := call{op: opNames()[rng.Intn(7)], tid: rng.Intn(tc)}
			got := applyReal(lock, c)
			want := applyNaive(model, c)

			fmt.Fprintf(&failLog, "#%02d %-9s -> 实际{ok=%v granted=%v} 朴素{ok=%v granted=%v} | %s\n",
				i, c, got.OK, got.Granted, want.ok, want.granted, got.Reason)

			if got.OK != want.ok {
				t.Fatalf("seq %d 步 %d %s: OK %v != 朴素 %v\n%s",
					seq, i, c, got.OK, want.ok, failLog.String())
			}
			if got.OK || want.ok {
				if !eqInts(sortedCopy(got.Granted), sortedCopy(want.granted)) {
					t.Fatalf("seq %d 步 %d %s: granted %v != 朴素 %v\n%s",
						seq, i, c, got.Granted, want.granted, failLog.String())
				}
			}
			if err := compareSnapshots(lock.Snapshot(), model.snapshot()); err != nil {
				t.Fatalf("seq %d 步 %d %s 状态分歧: %v\n%s",
					seq, i, c, err, failLog.String())
			}
			if err := checkInvariants(lock, lock.Snapshot()); err != nil {
				t.Fatalf("seq %d 步 %d %s 不变量破坏: %v\n%s",
					seq, i, c, err, failLog.String())
			}
		}

		// drain: eventually return to idle using only legal calls.
		for drainSteps := 0; drainSteps < 4*tc+8; drainSteps++ {
			s := lock.Snapshot()
			if s.Phase == PhaseIdle {
				break
			}
			var c call
			switch {
			case s.W != -1:
				if len(s.Qr) > 0 || rng.Intn(3) == 0 {
					c = call{"WUnlock", s.W}
				} else {
					c = call{"Downgrade", s.W}
				}
			case len(s.R) > 0:
				if len(s.R) == 1 && rng.Intn(4) == 0 {
					c = call{"Upgrade", s.R[0]}
				} else {
					c = call{"RUnlock", s.R[0]}
				}
			default:
				// phase should not be read/write without holders; cancel waiters
				c = call{"Cancel", anyWaiter(s)}
			}
			if c.tid < 0 {
				break
			}
			got := applyReal(lock, c)
			want := applyNaive(model, c)
			if got.OK != want.ok ||
				!eqInts(sortedCopy(got.Granted), sortedCopy(want.granted)) {
				t.Fatalf("seq %d drain %s: %+v != %+v", seq, c, got, want)
			}
			if err := compareSnapshots(lock.Snapshot(), model.snapshot()); err != nil {
				t.Fatalf("seq %d drain %s 分歧: %v", seq, c, err)
			}
		}
		if s := lock.Snapshot(); s.Phase != PhaseIdle {
			t.Fatalf("seq %d 未能排空到空闲: phase=%s\n%s", seq, s.Phase, failLog.String())
		}
	}
	t.Logf("完成 %d 组随机序列与朴素模拟逐项对照", sequences)
}

func anyWaiter(s Snapshot) int {
	for _, x := range s.Qr {
		return x
	}
	for _, q := range s.Qw {
		for _, x := range q {
			return x
		}
	}
	return -1
}

// TestReplayDeterminism replays identical random scripts on two locks and
// requires byte-identical granted sets and queues.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	m, tc, nodeOf, b := genConfig(rng)
	calls := make([]call, 300)
	for i := range calls {
		calls[i] = call{op: opNames()[rng.Intn(7)], tid: rng.Intn(tc)}
	}
	replay := func() []Snapshot {
		l, _ := New(m, tc, nodeOf, b)
		snaps := make([]Snapshot, 0, len(calls)+1)
		for _, c := range calls {
			applyReal(l, c)
			snaps = append(snaps, l.Snapshot())
		}
		return snaps
	}
	a := replay()
	bb := replay()
	for i := range a {
		if err := compareSnapshots(a[i], bb[i]); err != nil {
			t.Fatalf("第 %d 次调用后两次重放不一致: %v", i, err)
		}
	}
}

// TestConcurrentRace hammers the lock from many goroutines under -race.
// Callers coordinate through the returned Results so holds are always
// released by their owning goroutine.
func TestConcurrentRace(t *testing.T) {
	m, tc, b := 8, 24, 4
	nodeOf := make([]int, tc)
	for i := range nodeOf {
		nodeOf[i] = i % m
	}
	l, err := New(m, tc, nodeOf, b)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for tid := 0; tid < tc; tid++ {
		wg.Add(1)
		go func(tid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(tid)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				switch rng.Intn(6) {
				case 0:
					if r := l.RLock(tid); r.OK && len(r.Granted) > 0 {
						l.RUnlock(tid)
					}
				case 1:
					if r := l.WLock(tid); r.OK && len(r.Granted) > 0 {
						if rng.Intn(2) == 0 {
							l.WUnlock(tid)
						} else {
							l.Downgrade(tid)
							l.RUnlock(tid)
						}
					} else {
						l.Cancel(tid)
					}
				case 2:
					l.Cancel(tid)
				default:
					// read-only state inspection must be safe
					_ = l.Snapshot()
				}
			}
		}(tid)
	}
	// run briefly, then ask everyone to stop and clean up via a drainer.
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Deterministic cleanup: cancel every waiter, then release holders.
	for {
		s := l.Snapshot()
		if s.Phase == PhaseIdle {
			break
		}
		if tid := anyWaiter(s); tid >= 0 {
			l.Cancel(tid)
			continue
		}
		if s.W != -1 {
			l.WUnlock(s.W)
			continue
		}
		if len(s.R) > 0 {
			l.RUnlock(s.R[0])
			continue
		}
	}
	final := l.Snapshot()
	if err := checkInvariants(l, final); err != nil {
		t.Fatalf("并发结束后不变量破坏: %v", err)
	}
	if final.Phase != PhaseIdle {
		t.Fatalf("并发结束后未空闲: %s", final.Phase)
	}
}

var _ = strings.TrimSpace
