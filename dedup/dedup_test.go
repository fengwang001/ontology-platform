package dedup

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var base = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

func at(offset time.Duration) time.Time { return base.Add(offset) }

func newTestDeduper(t *testing.T, ttl time.Duration, maxEntries int) *Deduper {
	t.Helper()
	d, err := New(Config{TTL: ttl, MaxEntries: maxEntries, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	return d
}

func TestNewInvalidParameters(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"zero ttl", Config{TTL: 0, MaxEntries: 1}},
		{"negative ttl", Config{TTL: -time.Second, MaxEntries: 1}},
		{"zero max entries", Config{TTL: time.Minute, MaxEntries: 0}},
		{"negative max entries", Config{TTL: time.Minute, MaxEntries: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := New(tc.cfg)
			if d != nil {
				t.Fatalf("expected nil Deduper, got %+v", d)
			}
			var rej *RejectError
			if !errors.As(err, &rej) || rej.Reason != ReasonInvalidParameter {
				t.Fatalf("expected RejectError{%s}, got %v", ReasonInvalidParameter, err)
			}
			if !errors.Is(err, ErrInvalidParameter) {
				t.Fatalf("errors.Is(err, ErrInvalidParameter) = false")
			}
		})
	}
}

func TestEmptyIDRejected(t *testing.T) {
	d := newTestDeduper(t, time.Minute, 5)
	before := d.Snapshot()

	res, err := d.Process(Event{ID: "", Time: at(0)})
	if !errors.Is(err, ErrEmptyID) {
		t.Fatalf("expected ErrEmptyID, got %v", err)
	}
	if res.Accepted || res.Duplicate {
		t.Fatalf("rejected event must not be accepted/duplicate: %+v", res)
	}
	assertStateUnchanged(t, before, d.Snapshot())
}

func TestZeroTimeRejected(t *testing.T) {
	d := newTestDeduper(t, time.Minute, 5)
	before := d.Snapshot()

	res, err := d.Process(Event{ID: "x", Time: time.Time{}})
	if !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("expected ErrInvalidTime, got %v", err)
	}
	if res.Accepted || res.Duplicate {
		t.Fatalf("rejected event must not be accepted/duplicate: %+v", res)
	}
	assertStateUnchanged(t, before, d.Snapshot())
}

func TestMemoryLimitRejectedAndNoStateChange(t *testing.T) {
	const ttl = time.Hour
	d := newTestDeduper(t, ttl, 2)

	if r, err := d.Process(Event{ID: "a", Time: at(0)}); err != nil || !r.Accepted {
		t.Fatalf("accept a: res=%+v err=%v", r, err)
	}
	if r, err := d.Process(Event{ID: "b", Time: at(0)}); err != nil || !r.Accepted {
		t.Fatalf("accept b: res=%+v err=%v", r, err)
	}
	before := d.Snapshot()

	// 第三条新记忆会超限：拒绝，状态（含水位线）完全不变。
	res, err := d.Process(Event{ID: "c", Time: at(30 * time.Minute)})
	if !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("expected ErrMemoryLimit, got %v", err)
	}
	if res.Accepted || res.Duplicate {
		t.Fatalf("over-limit event must be neither accepted nor duplicate: %+v", res)
	}
	assertStateUnchanged(t, before, d.Snapshot())

	// 满状态下重复事件仍正常判重丢弃（不产生新记忆，不违反上限）。
	r, err := d.Process(Event{ID: "a", Time: at(45 * time.Minute)})
	if err != nil || !r.Duplicate {
		t.Fatalf("duplicate a while full: res=%+v err=%v", r, err)
	}
	dupState := d.Snapshot()
	if dupState.DuplicateCount != before.DuplicateCount+1 {
		t.Fatalf("duplicate count = %d, want %d", dupState.DuplicateCount, before.DuplicateCount+1)
	}
	if len(dupState.Memories) != 2 {
		t.Fatalf("memories = %d, want 2", len(dupState.Memories))
	}

	// 水位线推进到恰好过期边界：a、b 同时被清除，腾出空间后 c 被接受。
	r, err = d.Process(Event{ID: "c", Time: at(ttl)})
	if err != nil || !r.Accepted {
		t.Fatalf("accept c after expiration: res=%+v err=%v", r, err)
	}
	if !reflect.DeepEqual(r.ExpiredIDs, []string{"a", "b"}) {
		t.Fatalf("ExpiredIDs = %v, want [a b]", r.ExpiredIDs)
	}
	after := d.Snapshot()
	if len(after.Memories) != 1 {
		t.Fatalf("memories after expiration = %d, want 1", len(after.Memories))
	}
	if _, ok := after.Memories["c"]; !ok {
		t.Fatalf("memory c missing: %+v", after.Memories)
	}
}

func TestExpirationBoundary(t *testing.T) {
	const ttl = 10 * time.Minute
	d := newTestDeduper(t, ttl, 10)

	if _, err := d.Process(Event{ID: "a", Time: at(0)}); err != nil {
		t.Fatal(err)
	}

	// 差 1ns 到边界：记忆仍存活，判为重复。
	r, err := d.Process(Event{ID: "a", Time: at(ttl - time.Nanosecond)})
	if err != nil || !r.Duplicate {
		t.Fatalf("just before boundary: res=%+v err=%v", r, err)
	}
	if len(r.ExpiredIDs) != 0 {
		t.Fatalf("ExpiredIDs before boundary = %v, want none", r.ExpiredIDs)
	}

	// 用另一个标识把水位线推进到恰好 t0+TTL：a 必须在边界上立即清除。
	r, err = d.Process(Event{ID: "b", Time: at(ttl)})
	if err != nil || !r.Accepted {
		t.Fatalf("at boundary: res=%+v err=%v", r, err)
	}
	if !reflect.DeepEqual(r.ExpiredIDs, []string{"a"}) {
		t.Fatalf("ExpiredIDs at boundary = %v, want [a]", r.ExpiredIDs)
	}
	if _, ok := d.Snapshot().Memories["a"]; ok {
		t.Fatal("memory a must be evicted exactly on the boundary")
	}

	// 清除后同一标识再次出现应作为新事件接受。
	r, err = d.Process(Event{ID: "a", Time: at(ttl + time.Nanosecond)})
	if err != nil || !r.Accepted {
		t.Fatalf("a re-accepted after expiry: res=%+v err=%v", r, err)
	}
}

func TestDuplicateDoesNotRefreshMemory(t *testing.T) {
	const ttl = 10 * time.Minute
	d := newTestDeduper(t, ttl, 10)

	t0 := at(0)
	if _, err := d.Process(Event{ID: "a", Time: t0}); err != nil {
		t.Fatal(err)
	}
	// 5 分钟后重复到达；若错误地“刷新”记忆，过期点会变成 t0+15min。
	if r, err := d.Process(Event{ID: "a", Time: at(5 * time.Minute)}); err != nil || !r.Duplicate {
		t.Fatalf("duplicate a: res=%+v err=%v", r, err)
	}
	if mt := d.Snapshot().Memories["a"]; !mt.Equal(t0) {
		t.Fatalf("memory time refreshed to %v, want %v", mt, t0)
	}

	// 由另一事件把水位线推到 t0+10min：a 必须按原始时间过期。
	r, err := d.Process(Event{ID: "b", Time: at(ttl)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.ExpiredIDs, []string{"a"}) {
		t.Fatalf("a should expire at original t0+TTL, ExpiredIDs=%v", r.ExpiredIDs)
	}
}

func TestLateEventsAreNotDropped(t *testing.T) {
	const ttl = 2 * time.Hour
	d := newTestDeduper(t, ttl, 10)

	// 先把水位线推到 t0+1h。
	if _, err := d.Process(Event{ID: "a", Time: at(0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Process(Event{ID: "b", Time: at(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	// 迟到 30 分钟的重复标识：记忆仍存活 -> 判重，而非因迟到直接丢弃。
	r, err := d.Process(Event{ID: "a", Time: at(30 * time.Minute)})
	if err != nil || !r.Duplicate {
		t.Fatalf("late duplicate: res=%+v err=%v", r, err)
	}

	// 迟到 30 分钟的新标识：正常接受并保留记忆。
	r, err = d.Process(Event{ID: "c", Time: at(30 * time.Minute)})
	if err != nil || !r.Accepted {
		t.Fatalf("late new event: res=%+v err=%v", r, err)
	}
	if mt, ok := d.Snapshot().Memories["c"]; !ok || !mt.Equal(at(30*time.Minute)) {
		t.Fatalf("late event memory = %v (ok=%v)", mt, ok)
	}
}

func TestVeryLateNewEventAcceptedWithoutRetention(t *testing.T) {
	const ttl = time.Hour
	d := newTestDeduper(t, ttl, 1) // 上限为 1，放大占用问题

	if _, err := d.Process(Event{ID: "a", Time: at(0)}); err != nil {
		t.Fatal(err)
	}
	// 水位线推进到 t0+3h，a 过期清除，槽位为空。
	if _, err := d.Process(Event{ID: "b", Time: at(3 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if len(d.Snapshot().Memories) != 1 || d.Snapshot().Memories["b"].IsZero() {
		t.Fatalf("state after b = %+v", d.Snapshot())
	}

	// 迟到 3 小时的新标识 c：t0+TTL <= 水位线，出生即过期。
	// 仍应作为新事件输出，但不保留记忆、不占槽位，也不得触发超限拒绝。
	r, err := d.Process(Event{ID: "c", Time: at(0)})
	if err != nil || !r.Accepted {
		t.Fatalf("very late new event: res=%+v err=%v", r, err)
	}
	st := d.Snapshot()
	if _, ok := st.Memories["c"]; ok {
		t.Fatal("already-expired memory must not be retained")
	}
	if len(st.Memories) != 1 {
		t.Fatalf("memories = %d, want 1", len(st.Memories))
	}
	if st.AcceptedCount != 3 {
		t.Fatalf("accepted count = %d, want 3", st.AcceptedCount)
	}
}

func TestWatermarkMonotonic(t *testing.T) {
	d := newTestDeduper(t, time.Hour, 10)
	order := []time.Duration{5 * time.Second, time.Hour, 30 * time.Minute, 2 * time.Hour, time.Second}
	wantMax := at(0)
	for _, off := range order {
		e := Event{ID: "id-" + off.String(), Time: at(off)}
		r, err := d.Process(e)
		if err != nil {
			t.Fatal(err)
		}
		if off > 0 && at(off).After(wantMax) {
			wantMax = at(off)
		}
		if !r.Watermark.Equal(wantMax) {
			t.Fatalf("watermark = %v, want %v after offset %v", r.Watermark, wantMax, off)
		}
	}
}

func TestDeterministicReplay(t *testing.T) {
	// 同一输入序列反复计算，结果序列与最终状态必须完全一致。
	sequence := []Event{
		{ID: "a", Time: at(0)},
		{ID: "b", Time: at(time.Minute)},
		{ID: "a", Time: at(2 * time.Minute)}, // 重复
		{ID: "", Time: at(3 * time.Minute)},  // 非法：空标识
		{ID: "c", Time: time.Time{}},         // 非法：零时间
		{ID: "c", Time: at(time.Hour)},       // 新事件，同时使 a/b 过期
		{ID: "a", Time: at(time.Hour)},       // a 已过期 -> 重新接受
		{ID: "b", Time: at(61 * time.Minute)},
		{ID: "b", Time: at(62 * time.Minute)}, // 重复不刷新
	}

	replay := func() ([]Result, State) {
		d := newTestDeduper(t, time.Hour, 5)
		results := make([]Result, 0, len(sequence))
		for _, e := range sequence {
			r, _ := d.Process(e)
			results = append(results, r)
		}
		return results, d.Snapshot()
	}

	r1, s1 := replay()
	r2, s2 := replay()
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("result sequences differ:\n%+v\n%+v", r1, r2)
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("final states differ:\n%+v\n%+v", s1, s2)
	}
}

func TestConcurrentSnapshotConsistency(t *testing.T) {
	const goroutines = 8
	const perG = 50
	d := newTestDeduper(t, 100*time.Hour, goroutines*perG)

	var readerWg, writerWg sync.WaitGroup
	stop := make(chan struct{})

	// 并发热读 Snapshot：在 -race 下验证字段级一致、无数据竞争。
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s := d.Snapshot()
				// 快照内字段必须自洽：记忆条数不超过接受数。
				if len(s.Memories) > int(s.AcceptedCount) {
					t.Errorf("inconsistent snapshot: %d memories vs %d accepted", len(s.Memories), s.AcceptedCount)
					return
				}
			}
		}
	}()

	for g := 0; g < goroutines; g++ {
		writerWg.Add(1)
		go func(g int) {
			defer writerWg.Done()
			for i := 0; i < perG; i++ {
				id := "g" + string(rune('a'+g)) + "-" + string(rune('0'+i/10)) + string(rune('0'+i%10))
				_, err := d.Process(Event{ID: id, Time: at(time.Duration(g*perG+i) * time.Second)})
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
			}
		}(g)
	}

	writerWg.Wait()
	close(stop)
	readerWg.Wait()

	final := d.Snapshot()
	if final.AcceptedCount != goroutines*perG {
		t.Fatalf("accepted = %d, want %d", final.AcceptedCount, goroutines*perG)
	}
	if len(final.Memories) != goroutines*perG {
		t.Fatalf("memories = %d, want %d", len(final.Memories), goroutines*perG)
	}
}

func TestLoggingContents(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	d, err := New(Config{TTL: time.Hour, MaxEntries: 5, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := d.Process(Event{ID: "a", Time: at(0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Process(Event{ID: "a", Time: at(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Process(Event{ID: "", Time: at(time.Minute)}); err == nil {
		t.Fatal("empty id must return an error")
	}

	logs := buf.String()
	for _, want := range []string{
		`event received`,
		`level=INFO msg="dedup: event received"`,
		`event accepted as new`,
		`basis="no live memory for id"`,
		`duplicate discarded`,
		`basis="id already remembered"`,
		`first_seen=`,
		`reason=empty_id`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log output missing %q\nfull logs:\n%s", want, logs)
		}
	}
}

func assertStateUnchanged(t *testing.T, before, after State) {
	t.Helper()
	if !before.Watermark.Equal(after.Watermark) {
		t.Fatalf("watermark changed: %v -> %v", before.Watermark, after.Watermark)
	}
	if !reflect.DeepEqual(before.Memories, after.Memories) {
		t.Fatalf("memories changed: %+v -> %+v", before.Memories, after.Memories)
	}
	if before.DuplicateCount != after.DuplicateCount {
		t.Fatalf("duplicate count changed: %d -> %d", before.DuplicateCount, after.DuplicateCount)
	}
	if before.AcceptedCount != after.AcceptedCount {
		t.Fatalf("accepted count changed: %d -> %d", before.AcceptedCount, after.AcceptedCount)
	}
}
