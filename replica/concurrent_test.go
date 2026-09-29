package replica

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInvariantUnderMixedLoad(t *testing.T) {
	tol := 2 * time.Millisecond
	s, _, t0 := newTestSet(t, tol)
	for _, id := range []string{"a", "b", "c"} {
		mustAdd(t, s, id)
		mustFetch(t, s, t0, id, 0)
	}
	var prevHWM int64
	for i := 1; i <= 40; i++ {
		now := t0.Add(time.Duration(i) * time.Millisecond)
		mustAppend(t, s, now, 1)
		if i%2 == 0 {
			mustFetch(t, s, now, "a", int64(i))
		}
		if i%3 == 0 {
			mustFetch(t, s, now, "b", int64(i))
		}
		if i%7 == 0 {
			if _, err := s.Sweep(now); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
		}
		st := s.Status()
		if st.HighWatermark < prevHWM {
			t.Fatalf("step %d: hwm went %d -> %d", i, prevHWM, st.HighWatermark)
		}
		prevHWM = st.HighWatermark
		foundLeader := false
		for _, id := range st.Synced {
			if id == "leader" {
				foundLeader = true
			}
			if st.Ends[id] < st.HighWatermark {
				t.Fatalf("step %d: hwm %d exceeds synced %s end %d", i, st.HighWatermark, id, st.Ends[id])
			}
		}
		if !foundLeader {
			t.Fatalf("step %d: leader missing from synced %v", i, st.Synced)
		}
	}
}

func TestConcurrentCalls(t *testing.T) {
	tol := 5 * time.Millisecond
	var buf bytes.Buffer
	t0 := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	s, err := NewSyncSet("leader", tol, t0, WithLogWriter(&buf))
	if err != nil {
		t.Fatal(err)
	}
	const followers = 4
	for i := 0; i < followers; i++ {
		id := string(rune('a' + i))
		mustAdd(t, s, id)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 领导者串行推进位点（时钟必须单调，因此只有一个写时钟执行体）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			now := t0.Add(time.Duration(i) * time.Millisecond)
			if _, _, err := s.Append(now, 1); err != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// 每个跟随者并发拉取：只回报不超过当前领导者位点的单调值；
	// 停止信号后退出，由主执行体在收敛阶段把它们追平。
	for i := 0; i < followers; i++ {
		id := string(rune('a' + i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			var have int64
			for {
				select {
				case <-stop:
					return
				default:
				}
				leaderEnd := s.Status().Ends["leader"]
				if have < leaderEnd {
					have++
				}
				// 使用已观察时钟之后的时间，避免与领导者竞争触发回退拒绝。
				now := s.Status().LastClock.Add(time.Nanosecond)
				_ = s.Fetch(now, id, have)
			}
		}()
	}

	// 周期检查与查询并发执行。
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			now := s.Status().LastClock.Add(time.Microsecond)
			_, _ = s.Sweep(now)
		}
	}()
	go func() {
		defer wg.Done()
		for k := 0; k < 500; k++ {
			st := s.Status()
			for _, id := range st.Synced {
				if st.Ends[id] < st.HighWatermark {
					t.Errorf("hwm %d > end of synced %q %d", st.HighWatermark, id, st.Ends[id])
					return
				}
			}
		}
	}()

	time.Sleep(60 * time.Millisecond)
	close(stop)
	wg.Wait()

	// 收敛阶段：领导者已停写，把每个跟随者拉到领导者位点，
	// 被移出者应在此过程中重新加入，最终 hwm == leader end。
	st := s.Status()
	leaderEnd := st.Ends["leader"]
	now := st.LastClock
	for i := 0; i < followers; i++ {
		id := string(rune('a' + i))
		for s.Status().Ends[id] < leaderEnd {
			now = now.Add(time.Millisecond)
			target := s.Status().Ends[id] + 1
			if err := s.Fetch(now, id, target); err != nil {
				t.Fatalf("drain fetch %q: %v", id, err)
			}
		}
	}
	st = s.Status()
	if st.HighWatermark != leaderEnd {
		t.Fatalf("final hwm %d != leader end %d; synced=%v", st.HighWatermark, leaderEnd, st.Synced)
	}
	for _, id := range st.Synced {
		if st.Ends[id] != leaderEnd {
			t.Fatalf("synced %q end %d != %d", id, st.Ends[id], leaderEnd)
		}
	}
}

func TestLogsContainInputsSyncedAndDecision(t *testing.T) {
	var buf bytes.Buffer
	t0 := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	s, err := NewSyncSet("leader", time.Second, t0, WithLogWriter(&buf))
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, "f")
	mustAppend(t, s, t0.Add(time.Second), 2)
	mustFetch(t, s, t0.Add(2*time.Second), "f", 2)
	if _, err := s.Sweep(t0.Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.Fetch(t0.Add(4*time.Second), "ghost", 0); err != ErrUnknownReplica {
		t.Fatalf("want ErrUnknownReplica, got %v", err)
	}
	log := buf.String()
	for _, want := range []string{
		`new leader="leader"`,
		"append", "n=2",
		`id="f" end=2`,
		"sweep", "synced=[leader",
		"decision=",
		"reject", "unknown replica",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}
