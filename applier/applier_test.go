package applier

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// failPayload 总是应用失败；其余 payload 总是成功。
const failPayload = "fail"

func newTestApplier(t *testing.T, cfg Config) *Applier {
	t.Helper()
	return New(cfg, func(ev Event) error {
		if ev.Payload == failPayload {
			return errors.New("boom")
		}
		return nil
	})
}

func ev(key string, seq int64, payload string) Event {
	return Event{Key: key, Seq: seq, Payload: payload}
}

func seqs(events []Event) []int64 {
	got := make([]int64, len(events))
	for i, e := range events {
		got[i] = e.Seq
	}
	return got
}

// 一个键阻塞不得影响其他键继续提交并应用。
func TestBlockedKeyDoesNotAffectOthers(t *testing.T) {
	a := newTestApplier(t, Config{MaxAttempts: 2, MaxBuffered: 10})

	out, err := a.Submit(ev("A", 1, failPayload))
	if err != nil || out.Status != StatusRetrying || out.Attempts != 1 {
		t.Fatalf("A first submit: (%+v,%v), want retrying/1", out, err)
	}

	// A 阻塞期间，B 的事件必须立即成功；A 的后续事件只能缓冲。
	out, err = a.Submit(ev("B", 1, "ok"))
	if err != nil || out.Status != StatusApplied {
		t.Fatalf("B while A blocked: (%+v,%v), want applied", out, err)
	}
	out, err = a.Submit(ev("A", 2, "ok"))
	if err != nil || out.Status != StatusBuffered {
		t.Fatalf("A buffered while blocked: (%+v,%v), want buffered", out, err)
	}

	blocked := a.Blocked()
	if len(blocked) != 1 || blocked[0] != "A" {
		t.Fatalf("blocked = %v, want [A]", blocked)
	}
	if got := seqs(a.Applied("B")); len(got) != 1 || got[0] != 1 {
		t.Fatalf("B applied = %v, want [1]", got)
	}
	if got := a.Pending(); got != 2 {
		t.Fatalf("pending = %d, want 2 (A head + A buffer)", got)
	}
}

// 重试到上限进入死信：解除阻塞并按 FIFO 排空缓冲。
func TestRetryToDeadLetterDrainsFIFO(t *testing.T) {
	a := newTestApplier(t, Config{MaxAttempts: 3, MaxBuffered: 10})

	if out, err := a.Submit(ev("K", 1, failPayload)); err != nil || out.Status != StatusRetrying || out.Attempts != 1 {
		t.Fatalf("first attempt = (%+v,%v)", out, err)
	}
	// seq2 也失败、seq3 成功：排空必须严格 FIFO。
	if _, err := a.Submit(ev("K", 2, failPayload)); err != nil {
		t.Fatalf("buffer seq2: %v", err)
	}
	if _, err := a.Submit(ev("K", 3, "ok")); err != nil {
		t.Fatalf("buffer seq3: %v", err)
	}

	outs := a.Advance()
	if len(outs) != 1 || outs[0].Status != StatusRetrying || outs[0].Attempts != 2 {
		t.Fatalf("advance 1 = %+v, want one retrying/2", outs)
	}
	outs = a.Advance()
	// seq1 第三次失败 -> dead；drain 立即尝试 seq2（首次失败，阻塞）；seq3 留在缓冲。
	if len(outs) != 2 {
		t.Fatalf("advance 2 len = %d, want 2: %+v", len(outs), outs)
	}
	if outs[0].Status != StatusDead || outs[0].Attempts != 3 || outs[0].Seq != 1 {
		t.Fatalf("outs[0] = %+v, want dead seq1 attempts3", outs[0])
	}
	if outs[1].Status != StatusRetrying || outs[1].Attempts != 1 || outs[1].Seq != 2 {
		t.Fatalf("outs[1] = %+v, want retrying seq2 attempts1", outs[1])
	}

	dead := a.DeadLetters()
	if len(dead) != 1 || dead[0].Event.Seq != 1 || dead[0].Attempts != 3 {
		t.Fatalf("dead = %+v, want seq1/3", dead)
	}

	// seq2 再试两次后进入死信，随后 seq3 首次即成功，缓冲排空、键解除阻塞。
	outs = a.Advance()
	if len(outs) != 1 || outs[0].Status != StatusRetrying || outs[0].Attempts != 2 {
		t.Fatalf("advance 3 = %+v", outs)
	}
	outs = a.Advance()
	if len(outs) != 2 {
		t.Fatalf("advance 4 len = %d, %+v", len(outs), outs)
	}
	if outs[0].Seq != 2 || outs[0].Status != StatusDead || outs[0].Attempts != 3 {
		t.Fatalf("outs[0] = %+v, want seq2 dead/3", outs[0])
	}
	if outs[1].Seq != 3 || outs[1].Status != StatusApplied || outs[1].Attempts != 1 {
		t.Fatalf("outs[1] = %+v, want seq3 applied/1", outs[1])
	}

	if len(a.Blocked()) != 0 {
		t.Fatalf("blocked = %v, want empty after drain", a.Blocked())
	}
	if got := seqs(a.Applied("K")); len(got) != 1 || got[0] != 3 {
		t.Fatalf("applied = %v, want [3]", got)
	}
	dead = a.DeadLetters()
	if len(dead) != 2 || dead[0].Event.Seq != 1 || dead[1].Event.Seq != 2 {
		t.Fatalf("dead seqs = %+v, want [1 2]", dead)
	}
	if a.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", a.Pending())
	}
}

// 各类非法输入：空键、序号不递增、缓冲超限；拒绝不得改变任何状态。
func TestInvalidInputsRejectedWithoutStateChange(t *testing.T) {
	t.Run("empty key", func(t *testing.T) {
		a := newTestApplier(t, Config{MaxAttempts: 1, MaxBuffered: 2})
		if _, err := a.Submit(ev("", 1, "ok")); !errors.Is(err, ErrEmptyKey) {
			t.Fatalf("err = %v, want ErrEmptyKey", err)
		}
		if a.Pending() != 0 || len(a.DeadLetters()) != 0 || len(a.Blocked()) != 0 {
			t.Fatalf("state changed after empty-key rejection")
		}
	})

	t.Run("sequence not strictly increasing", func(t *testing.T) {
		a := newTestApplier(t, Config{MaxAttempts: 1, MaxBuffered: 5})
		if _, err := a.Submit(ev("K", 5, "ok")); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []int64{5, 4, 0, -1} {
			if _, err := a.Submit(ev("K", bad, "ok")); !errors.Is(err, ErrSequenceNotIncreasing) {
				t.Fatalf("seq=%d err = %v, want ErrSequenceNotIncreasing", bad, err)
			}
		}
		// 拒绝不改变状态：6 仍可正常提交，已应用列表只有 [5 6]。
		if _, err := a.Submit(ev("K", 6, "ok")); err != nil {
			t.Fatalf("seq 6 after rejections: %v", err)
		}
		if got := seqs(a.Applied("K")); len(got) != 2 || got[0] != 5 || got[1] != 6 {
			t.Fatalf("applied = %v, want [5 6]", got)
		}
		if a.Pending() != 0 || len(a.DeadLetters()) != 0 {
			t.Fatalf("rejected seq changed pending/dead state")
		}
	})

	t.Run("buffer limit", func(t *testing.T) {
		a := newTestApplier(t, Config{MaxAttempts: 3, MaxBuffered: 3})
		if _, err := a.Submit(ev("K", 1, failPayload)); err != nil { // 队首，pending=1
			t.Fatal(err)
		}
		if _, err := a.Submit(ev("K", 2, "ok")); err != nil { // 缓冲，pending=2
			t.Fatal(err)
		}
		if _, err := a.Submit(ev("K", 3, "ok")); err != nil { // 缓冲，pending=3
			t.Fatal(err)
		}
		if _, err := a.Submit(ev("K", 4, "ok")); !errors.Is(err, ErrBufferLimitExceeded) {
			t.Fatalf("err = %v, want ErrBufferLimitExceeded", err)
		}
		if a.Pending() != 3 {
			t.Fatalf("pending = %d, want 3 unchanged", a.Pending())
		}
		if got := seqs(a.Applied("K")); len(got) != 0 {
			t.Fatalf("applied = %v, want none", got)
		}
		if len(a.DeadLetters()) != 0 {
			t.Fatalf("dead changed after limit rejection: %+v", a.DeadLetters())
		}
	})

	t.Run("reasons are distinguishable", func(t *testing.T) {
		if errors.Is(ErrEmptyKey, ErrSequenceNotIncreasing) ||
			errors.Is(ErrEmptyKey, ErrBufferLimitExceeded) ||
			errors.Is(ErrSequenceNotIncreasing, ErrBufferLimitExceeded) {
			t.Fatal("sentinel rejection errors must be distinct")
		}
	})
}

// 同一输入序列串行计算两次，输出必须完全相同。
func TestDeterministicReplay(t *testing.T) {
	run := func() ([]int64, []DeadEntry, []string, int) {
		a := newTestApplier(t, Config{MaxAttempts: 2, MaxBuffered: 20})
		steps := []Event{
			ev("A", 1, failPayload),
			ev("B", 1, "ok"),
			ev("A", 2, failPayload),
			ev("B", 2, failPayload),
			ev("A", 3, "ok"),
		}
		for _, e := range steps {
			if _, err := a.Submit(e); err != nil {
				t.Fatalf("submit %+v: %v", e, err)
			}
			a.Advance()
		}
		a.Advance()
		a.Advance()
		return seqs(a.Applied("A")), a.DeadLetters(), a.Blocked(), a.Pending()
	}

	firstApplied, firstDead, firstBlocked, firstPending := run()
	secondApplied, secondDead, secondBlocked, secondPending := run()
	if fmt.Sprint(firstApplied) != fmt.Sprint(secondApplied) {
		t.Fatalf("applied mismatch: %v vs %v", firstApplied, secondApplied)
	}
	if fmt.Sprint(firstDead) != fmt.Sprint(secondDead) {
		t.Fatalf("dead mismatch: %v vs %v", firstDead, secondDead)
	}
	if fmt.Sprint(firstBlocked) != fmt.Sprint(secondBlocked) {
		t.Fatalf("blocked mismatch: %v vs %v", firstBlocked, secondBlocked)
	}
	if firstPending != secondPending {
		t.Fatalf("pending mismatch: %d vs %d", firstPending, secondPending)
	}
}

// 并发下每个键的定案次序与逐键串行参照一致（每个 goroutine 独占一个键）。
func TestConcurrentPerKeyOrderMatchesSerial(t *testing.T) {
	const goroutines = 8
	const perG = 50
	cfg := Config{MaxAttempts: 1, MaxBuffered: goroutines * perG}

	serial := New(cfg, func(Event) error { return nil })
	for g := 0; g < goroutines; g++ {
		key := fmt.Sprintf("K%d", g)
		for i := 0; i < perG; i++ {
			if _, err := serial.Submit(ev(key, int64(i), "ok")); err != nil {
				t.Fatalf("serial submit: %v", err)
			}
		}
	}

	parallel := New(cfg, func(Event) error { return nil })
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := fmt.Sprintf("K%d", g)
			for i := 0; i < perG; i++ {
				if _, err := parallel.Submit(ev(key, int64(i), "ok")); err != nil {
					t.Errorf("parallel submit key=%s seq=%d: %v", key, i, err)
				}
			}
		}(g)
	}
	wg.Wait()

	for g := 0; g < goroutines; g++ {
		key := fmt.Sprintf("K%d", g)
		want := seqs(serial.Applied(key))
		got := seqs(parallel.Applied(key))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("key %s applied order = %v, want %v", key, got, want)
		}
	}
	if parallel.Pending() != 0 || len(parallel.DeadLetters()) != 0 {
		t.Fatalf("parallel leftover pending=%d dead=%d", parallel.Pending(), len(parallel.DeadLetters()))
	}
}
