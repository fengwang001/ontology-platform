package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
)

// scriptedApplier 按 "key/seq" 预置前若干次应用失败的次数。
type scriptedApplier struct {
	mu       sync.Mutex
	failLeft map[string]int
	calls    []Event
}

func newScriptedApplier() *scriptedApplier {
	return &scriptedApplier{failLeft: map[string]int{}}
}

func (a *scriptedApplier) fail(ev Event, times int) {
	a.failLeft[ev.Key+"/"+fmt.Sprint(ev.Seq)] = times
}

func (a *scriptedApplier) Apply(ev Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, ev)
	id := ev.Key + "/" + fmt.Sprint(ev.Seq)
	if a.failLeft[id] > 0 {
		a.failLeft[id]--
		return errors.New("injected failure for " + id)
	}
	return nil
}

func (a *scriptedApplier) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func rejectReason(t *testing.T, err error) RejectReason {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %v", err)
	}
	return re.Reason
}

// 一个键阻塞时不影响其他键；重试成功后解除阻塞并排空缓冲。
func TestBlockedKeyDoesNotAffectOthers(t *testing.T) {
	ap := newScriptedApplier()
	eng := NewEngine(ap, Config{MaxAttempts: 3, BufferLimit: 100})

	ap.fail(Event{Key: "A", Seq: 1}, 2)
	if err := eng.Submit(Event{Key: "A", Seq: 1, Value: "a1"}); err != nil {
		t.Fatalf("submit A1: %v", err)
	}
	if err := eng.Submit(Event{Key: "B", Seq: 1, Value: "b1"}); err != nil {
		t.Fatalf("submit B1: %v", err)
	}
	if got := eng.Applied(); len(got) != 1 || got[0].Key != "B" {
		t.Fatalf("while A blocked, B must apply immediately, got %+v", got)
	}

	if err := eng.Submit(Event{Key: "A", Seq: 2, Value: "a2"}); err != nil {
		t.Fatalf("submit A2: %v", err)
	}
	if err := eng.Submit(Event{Key: "B", Seq: 2, Value: "b2"}); err != nil {
		t.Fatalf("submit B2: %v", err)
	}
	if got := eng.Applied(); len(got) != 2 || got[1].Key != "B" {
		t.Fatalf("B must keep applying while A is blocked, got %+v", got)
	}

	eng.Advance()
	if got := eng.DeadLetters(); len(got) != 0 {
		t.Fatalf("A1 should not be dead-lettered before max attempts, got %+v", got)
	}
	eng.Advance()

	got := eng.Applied()
	want := []Event{
		{Key: "B", Seq: 1, Value: "b1"},
		{Key: "B", Seq: 2, Value: "b2"},
		{Key: "A", Seq: 1, Value: "a1"},
		{Key: "A", Seq: 2, Value: "a2"},
	}
	if len(got) != len(want) {
		t.Fatalf("applied = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("applied[%d] = %+v, want %+v (full %+v)", i, got[i], want[i], got)
		}
	}
	if dl := eng.DeadLetters(); len(dl) != 0 {
		t.Fatalf("unexpected dead letters: %+v", dl)
	}
}

// 重试到上限仍失败：进入死信、解除阻塞、按 FIFO 排空缓冲。
func TestDeadLetterAfterMaxAttemptsAndDrain(t *testing.T) {
	ap := newScriptedApplier()
	eng := NewEngine(ap, Config{MaxAttempts: 2, BufferLimit: 100})

	bad := Event{Key: "K", Seq: 1, Value: "bad"}
	ap.fail(bad, 99)
	ap.fail(Event{Key: "K", Seq: 2}, 1)

	if err := eng.Submit(bad); err != nil {
		t.Fatalf("submit bad: %v", err)
	}
	for _, ev := range []Event{
		{Key: "K", Seq: 2, Value: "k2"},
		{Key: "K", Seq: 3, Value: "k3"},
		{Key: "K", Seq: 4, Value: "k4"},
	} {
		if err := eng.Submit(ev); err != nil {
			t.Fatalf("submit %+v: %v", ev, err)
		}
	}

	eng.Advance()
	if dl := eng.DeadLetters(); len(dl) != 1 || dl[0] != bad {
		t.Fatalf("dead letters = %+v, want [%+v]", dl, bad)
	}
	if got := eng.Applied(); len(got) != 0 {
		t.Fatalf("nothing should apply yet, got %+v", got)
	}

	eng.Advance()
	got := eng.Applied()
	want := []Event{
		{Key: "K", Seq: 2, Value: "k2"},
		{Key: "K", Seq: 3, Value: "k3"},
		{Key: "K", Seq: 4, Value: "k4"},
	}
	if len(got) != len(want) {
		t.Fatalf("applied = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("applied[%d] = %+v, want %+v (full %+v)", i, got[i], want[i], got)
		}
	}
	if dl := eng.DeadLetters(); len(dl) != 1 {
		t.Fatalf("dead letters = %+v", dl)
	}
}

// 各类非法输入必须被拒绝且不改变任何状态。
func TestRejectionsDoNotMutateState(t *testing.T) {
	ap := newScriptedApplier()
	eng := NewEngine(ap, Config{MaxAttempts: 2, BufferLimit: 2})

	err := eng.Submit(Event{Key: "", Seq: 1})
	if reason := rejectReason(t, err); reason != RejectEmptyKey {
		t.Fatalf("reason = %s, want %s", reason, RejectEmptyKey)
	}

	ap.fail(Event{Key: "K", Seq: 1}, 99)
	if err := eng.Submit(Event{Key: "K", Seq: 1, Value: "k1"}); err != nil {
		t.Fatalf("submit K1: %v", err)
	}
	if err := eng.Submit(Event{Key: "K", Seq: 2, Value: "k2"}); err != nil {
		t.Fatalf("submit K2: %v", err)
	}

	callsBefore := ap.callCount()
	err = eng.Submit(Event{Key: "K", Seq: 3, Value: "k3"})
	if reason := rejectReason(t, err); reason != RejectBufferFull {
		t.Fatalf("reason = %s, want %s", reason, RejectBufferFull)
	}
	if ap.callCount() != callsBefore {
		t.Fatalf("rejected submit must not invoke applier")
	}
	err = eng.Submit(Event{Key: "Z", Seq: 1, Value: "z1"})
	if reason := rejectReason(t, err); reason != RejectBufferFull {
		t.Fatalf("reason = %s, want %s", reason, RejectBufferFull)
	}

	err = eng.Submit(Event{Key: "K", Seq: 2, Value: "dup"})
	if reason := rejectReason(t, err); reason != RejectSeqNotIncreasing {
		t.Fatalf("reason = %s, want %s", reason, RejectSeqNotIncreasing)
	}
	err = eng.Submit(Event{Key: "K", Seq: 1, Value: "back"})
	if reason := rejectReason(t, err); reason != RejectSeqNotIncreasing {
		t.Fatalf("reason = %s, want %s", reason, RejectSeqNotIncreasing)
	}

	if got := eng.Applied(); len(got) != 0 {
		t.Fatalf("applied mutated by rejected submits: %+v", got)
	}
	if got := eng.DeadLetters(); len(got) != 0 {
		t.Fatalf("dead letters mutated: %+v", got)
	}

	eng2 := NewEngine(newScriptedApplier(), Config{MaxAttempts: 1, BufferLimit: 100})
	if err := eng2.Submit(Event{Key: "K", Seq: 5}); err != nil {
		t.Fatalf("submit seq5: %v", err)
	}
	err = eng2.Submit(Event{Key: "K", Seq: 5})
	if reason := rejectReason(t, err); reason != RejectSeqNotIncreasing {
		t.Fatalf("reason = %s, want %s", reason, RejectSeqNotIncreasing)
	}
}

// 同一输入序列重复计算得到完全相同的输出；日志含输入、定案结果与判定依据。
func TestDeterministicReplayAndLogging(t *testing.T) {
	script := []Event{
		{Key: "A", Seq: 1, Value: "a1"},
		{Key: "B", Seq: 1, Value: "b1"},
		{Key: "A", Seq: 2, Value: "a2"},
		{Key: "B", Seq: 2, Value: "b2"},
	}
	run := func() (string, string, string) {
		ap := newScriptedApplier()
		ap.fail(Event{Key: "A", Seq: 1}, 99)
		var buf bytes.Buffer
		eng := NewEngine(ap, Config{
			MaxAttempts: 2,
			BufferLimit: 100,
			Logger:      log.New(io.Writer(&buf), "", 0),
		})
		for _, ev := range script {
			if err := eng.Submit(ev); err != nil {
				t.Fatalf("submit %+v: %v", ev, err)
			}
		}
		eng.Advance()
		return fmt.Sprint(eng.Applied()), fmt.Sprint(eng.DeadLetters()), buf.String()
	}

	applied1, dead1, logs1 := run()
	applied2, dead2, logs2 := run()
	if applied1 != applied2 || dead1 != dead2 {
		t.Fatalf("replay mismatch: %s/%s vs %s/%s", applied1, dead1, applied2, dead2)
	}
	if logs1 != logs2 {
		t.Fatalf("replay logs differ")
	}
	for _, want := range []string{"submit", "decision=applied", "decision=blocked", "decision=dead_letter", "basis="} {
		if !strings.Contains(logs1, want) {
			t.Fatalf("logs missing %q:\n%s", want, logs1)
		}
	}
}

// 并发调用下每个键的定案次序与逐键串行参照一致（竞态由 -race 覆盖）。
func TestConcurrentSubmitsPerKeyOrderMatchesSerial(t *testing.T) {
	const perKey = 50
	keys := []string{"A", "B", "C", "D"}

	ap := newScriptedApplier()
	eng := NewEngine(ap, Config{MaxAttempts: 3, BufferLimit: 100000})
	var wg sync.WaitGroup
	for _, key := range keys {
		ap.fail(Event{Key: key, Seq: 7}, 2)
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			// 同键严格按序提交；不同键之间并发，引擎内部串行化定案。
			for i := 1; i <= perKey; i++ {
				ev := Event{Key: key, Seq: int64(i), Value: fmt.Sprintf("%s%d", key, i)}
				if err := eng.Submit(ev); err != nil {
					t.Errorf("concurrent submit %+v: %v", ev, err)
				}
			}
		}(key)
	}

	stop := make(chan struct{})
	advDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				eng.Advance()
				close(advDone)
				return
			case <-time.After(time.Millisecond):
				eng.Advance()
			}
		}
	}()
	wg.Wait()
	close(stop)
	<-advDone
	eng.Advance()

	applied := byKey(eng.Applied())
	dead := byKey(eng.DeadLetters())
	for _, key := range keys {
		evs := applied[key]
		if len(evs) != perKey {
			t.Fatalf("key %s applied %d events, want %d (dead=%+v)", key, len(evs), perKey, dead[key])
		}
		for i, ev := range evs {
			if ev.Seq != int64(i+1) {
				t.Fatalf("key %s finalization order not FIFO at %d: %+v (full %+v)", key, i, ev, evs)
			}
		}
		if len(dead[key]) != 0 {
			t.Fatalf("key %s should recover, got dead letters %+v", key, dead[key])
		}
	}
}

func byKey(evs []Event) map[string][]Event {
	out := map[string][]Event{}
	for _, ev := range evs {
		out[ev.Key] = append(out[ev.Key], ev)
	}
	return out
}
