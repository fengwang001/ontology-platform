package temporaljoin

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// newTestJoiner 构造一个使用文本日志捕获的 Joiner，便于断言判定依据被打印。
func newTestJoiner(t *testing.T, capacity int) (*Joiner, *bytes.Buffer) {
	t.Helper()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	j, err := NewJoiner(Options{BufferCapacity: capacity, Logger: logger})
	if err != nil {
		t.Fatalf("NewJoiner: %v", err)
	}
	return j, &logBuf
}

func rejectReason(t *testing.T, err error) RejectReason {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %T: %v", err, err)
	}
	return re.Reason
}

func drain(t *testing.T, j *Joiner, wm int64) []Result {
	t.Helper()
	rs, err := j.AdvanceWatermark(wm)
	if err != nil {
		t.Fatalf("AdvanceWatermark(%d): %v", wm, err)
	}
	return rs
}

// TestEffectiveAtEqualsEventTime: 生效起点恰好等于事件时间，命中该版本（左闭）。
func TestEffectiveAtEqualsEventTime(t *testing.T) {
	j, _ := newTestJoiner(t, 10)
	if err := j.PutVersion("k", 10, []byte("v10")); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PutEvent("k", 10, nil); err != nil {
		t.Fatal(err)
	}
	rs := drain(t, j, 10)
	if len(rs) != 1 || !rs[0].Hit || string(rs[0].Value) != "v10" ||
		rs[0].VersionEffectiveAt != 10 || rs[0].Basis != BasisHit {
		t.Fatalf("unexpected result: %+v", rs)
	}
}

// TestTombstone: 墓碑区间无值；墓碑之后的新版本重新生效；墓碑前为无版本。
func TestTombstone(t *testing.T) {
	j, _ := newTestJoiner(t, 10)
	must(t, j.PutVersion("k", 5, []byte("A")))
	must(t, j.PutTombstone("k", 8))
	must(t, j.PutVersion("k", 12, []byte("B")))

	cases := []struct {
		at       int64
		hit      bool
		want     string
		basis    JoinBasis
		verEffAt int64
	}{
		{4, false, "", BasisNoVersion, 0},
		{7, true, "A", BasisHit, 5},       // [5,8) 命中 A
		{8, false, "", BasisTombstone, 8}, // [8,12) 墓碑
		{11, false, "", BasisTombstone, 8},
		{12, true, "B", BasisHit, 12},  // 边界命中 B
		{100, true, "B", BasisHit, 12}, // 最后一个版本到无穷
	}
	for _, c := range cases {
		if _, err := j.PutEvent("k", c.at, nil); err != nil {
			t.Fatalf("event %d: %v", c.at, err)
		}
	}
	rs := drain(t, j, 100)
	if len(rs) != len(cases) {
		t.Fatalf("want %d results, got %d", len(cases), len(rs))
	}
	for i, c := range cases {
		got := rs[i]
		if got.Basis != c.basis || got.Hit != c.hit || got.VersionEffectiveAt != c.verEffAt {
			t.Fatalf("at=%d: got %+v, want basis=%s hit=%v verAt=%d", c.at, got, c.basis, c.hit, c.verEffAt)
		}
		if c.hit && string(got.Value) != c.want {
			t.Fatalf("at=%d: got value %q, want %q", c.at, got.Value, c.want)
		}
	}
}

// TestSameEffectiveAtOverwrite: 同一起点后写覆盖先写（值覆盖值、值覆盖墓碑、墓碑覆盖值）。
func TestSameEffectiveAtOverwrite(t *testing.T) {
	j, _ := newTestJoiner(t, 10)
	must(t, j.PutVersion("k", 5, []byte("A")))
	must(t, j.PutVersion("k", 5, []byte("B"))) // 值覆盖值
	if vs := j.Versions("k"); len(vs) != 1 || string(vs[0].Value) != "B" {
		t.Fatalf("overwrite value: %+v", vs)
	}

	must(t, j.PutTombstone("k", 5)) // 墓碑覆盖值
	if vs := j.Versions("k"); len(vs) != 1 || !vs[0].Tombstone {
		t.Fatalf("overwrite with tombstone: %+v", vs)
	}
	must(t, j.PutVersion("k", 5, []byte("C"))) // 值覆盖墓碑
	if vs := j.Versions("k"); len(vs) != 1 || vs[0].Tombstone || string(vs[0].Value) != "C" {
		t.Fatalf("overwrite tombstone: %+v", vs)
	}

	if _, err := j.PutEvent("k", 5, nil); err != nil {
		t.Fatal(err)
	}
	rs := drain(t, j, 5)
	if len(rs) != 1 || !rs[0].Hit || string(rs[0].Value) != "C" {
		t.Fatalf("event should see final overwrite, got %+v", rs)
	}
}

// TestOutOfOrderVersionArrival: 版本晚于事件到达、版本乱序到达，只要早于水位线关闭即正确。
func TestOutOfOrderVersionArrival(t *testing.T) {
	j, _ := newTestJoiner(t, 10)
	// 事件先到，版本后到。
	if _, err := j.PutEvent("k", 10, []byte("e1")); err != nil {
		t.Fatal(err)
	}
	// 高起点版本先到、低起点版本后到（乱序写入）。
	must(t, j.PutVersion("k", 15, []byte("late-range")))
	must(t, j.PutVersion("k", 10, []byte("v10")))
	rs := drain(t, j, 14)
	if len(rs) != 1 || !rs[0].Hit || string(rs[0].Value) != "v10" {
		t.Fatalf("event at 10 should join v10, got %+v", rs)
	}
}

// TestLateVersionRejected: 版本变更不晚于水位线即迟到，被拒绝且不改变版本表。
func TestLateVersionRejected(t *testing.T) {
	j, _ := newTestJoiner(t, 10)
	must(t, j.PutVersion("k", 10, []byte("v10")))
	drain(t, j, 10)

	// 早于水位线。
	err := j.PutVersion("k", 9, []byte("v9"))
	if reason := rejectReason(t, err); reason != ReasonLateVersion {
		t.Fatalf("want late_version_change, got %v", err)
	}
	// 恰好等于水位线同样迟到。
	err = j.PutTombstone("k", 10)
	if reason := rejectReason(t, err); reason != ReasonLateVersion {
		t.Fatalf("equal wm want late_version_change, got %v", err)
	}
	if vs := j.Versions("k"); len(vs) != 1 || string(vs[0].Value) != "v10" {
		t.Fatalf("rejected late version changed table: %+v", vs)
	}
	if j.Watermark() != 10 || j.Pending() != 0 {
		t.Fatalf("reject changed wm/buffer: wm=%d pending=%d", j.Watermark(), j.Pending())
	}
	// 严格晚于水位线仍然接受（区间 [11, inf)）。
	must(t, j.PutVersion("k", 11, []byte("v11")))
}

// TestRejectEmptyKey: 空键事件与空键版本变更均被拒绝。
func TestRejectEmptyKey(t *testing.T) {
	j, _ := newTestJoiner(t, 10)
	if _, err := j.PutEvent("", 5, nil); rejectReason(t, err) != ReasonEmptyKey {
		t.Fatalf("empty event key: %v", err)
	}
	if err := j.PutVersion("", 5, []byte("v")); rejectReason(t, err) != ReasonEmptyKey {
		t.Fatalf("empty version key: %v", err)
	}
	if err := j.PutTombstone("", 5); rejectReason(t, err) != ReasonEmptyKey {
		t.Fatalf("empty tombstone key: %v", err)
	}
	if j.Pending() != 0 || len(j.Versions("")) != 0 {
		t.Fatal("empty-key operations must not mutate state")
	}
}

// TestLateEventRejected: 事件时间不晚于水位线即迟到。
func TestLateEventRejected(t *testing.T) {
	j, _ := newTestJoiner(t, 10)
	must(t, j.PutVersion("k", 1, []byte("v")))
	mustEvent(t, j, "k", 5, nil)
	drain(t, j, 5)

	if _, err := j.PutEvent("k", 5, nil); rejectReason(t, err) != ReasonLateEvent {
		t.Fatalf("event at wm: %v", err)
	}
	if _, err := j.PutEvent("k", 4, nil); rejectReason(t, err) != ReasonLateEvent {
		t.Fatalf("event before wm: %v", err)
	}
	if j.Pending() != 0 {
		t.Fatal("late event must not be buffered")
	}
}

// TestWatermarkRegression: 水位线只能单调非降，回退被拒绝且无副作用。
func TestWatermarkRegression(t *testing.T) {
	j, _ := newTestJoiner(t, 10)
	drain(t, j, 10)
	if _, err := j.AdvanceWatermark(9); rejectReason(t, err) != ReasonWatermarkRegression {
		t.Fatalf("regression: %v", err)
	}
	// 回退被拒绝后，正常推进仍可输出此前缓冲的事件。
	must(t, j.PutVersion("k", 11, []byte("v11")))
	mustEvent(t, j, "k", 11, nil)
	if _, err := j.AdvanceWatermark(-1 << 60); rejectReason(t, err) != ReasonWatermarkRegression {
		t.Fatalf("negative regression: %v", err)
	}
	rs := drain(t, j, 11)
	if len(rs) != 1 || !rs[0].Hit {
		t.Fatalf("event lost after rejected regression: %+v", rs)
	}
	// 推进到相同水位线不是回退，返回空输出且无错误。
	if rs, err := j.AdvanceWatermark(11); err != nil || rs != nil {
		t.Fatalf("equal watermark should be no-op, got %v %v", rs, err)
	}
}

// TestBufferOverflow: 缓冲满后新事件被拒绝，且拒绝不挤出已有事件。
func TestBufferOverflow(t *testing.T) {
	j, _ := newTestJoiner(t, 2)
	mustEvent(t, j, "a", 1, nil)
	mustEvent(t, j, "b", 2, nil)
	_, err := j.PutEvent("c", 3, nil)
	if rejectReason(t, err) != ReasonBufferOverflow {
		t.Fatalf("overflow: %v", err)
	}
	if j.Pending() != 2 {
		t.Fatalf("rejected overflow changed buffer size: %d", j.Pending())
	}
	// 排空一个后容量恢复，且先前两个事件都还在。
	rs := drain(t, j, 1)
	if len(rs) != 1 || rs[0].Key != "a" {
		t.Fatalf("drained wrong event: %+v", rs)
	}
	if _, err := j.PutEvent("c", 3, nil); err != nil {
		t.Fatalf("buffer should have room after drain: %v", err)
	}
	rs = drain(t, j, 3)
	if len(rs) != 2 || rs[0].Key != "b" || rs[1].Key != "c" {
		t.Fatalf("ordering after overflow recovery: %+v", rs)
	}
}

// TestInvalidConfig: 缓冲容量非法。
func TestInvalidConfig(t *testing.T) {
	if _, err := NewJoiner(Options{BufferCapacity: 0}); rejectReason(t, err) != ReasonInvalidConfig {
		t.Fatalf("capacity 0: %v", err)
	}
	if _, err := NewJoiner(Options{BufferCapacity: -1}); rejectReason(t, err) != ReasonInvalidConfig {
		t.Fatalf("capacity -1: %v", err)
	}
}

// TestExactlyOnceAndNaiveConsistency: 每个被接受事件恰好输出一次，结果与朴素查询一致。
func TestExactlyOnceAndNaiveConsistency(t *testing.T) {
	j, _ := newTestJoiner(t, 1000)
	// 预置版本表：多个键、值/墓碑交替。
	spec := map[string][]Version{
		"a": {{EffectiveAt: 1, Value: []byte("a1")}, {EffectiveAt: 10, Tombstone: true}, {EffectiveAt: 20, Value: []byte("a20")}},
		"b": {{EffectiveAt: 5, Value: []byte("b5")}},
		"c": {{EffectiveAt: 3, Tombstone: true}},
	}
	for k, vs := range spec {
		for _, v := range vs {
			var err error
			if v.Tombstone {
				err = j.PutTombstone(k, v.EffectiveAt)
			} else {
				err = j.PutVersion(k, v.EffectiveAt, v.Value)
			}
			must(t, err)
		}
	}

	// 构造一批事件，时间在 [1..50)。
	var events []Event
	keys := []string{"a", "b", "c", "d"} // d 从未有过版本
	for t0 := int64(1); t0 < 50; t0++ {
		for _, k := range keys {
			events = append(events, Event{Key: k, EventTime: t0, Payload: []byte(fmt.Sprintf("%s@%d", k, t0))})
		}
	}

	var wg sync.WaitGroup
	for _, e := range events {
		wg.Add(1)
		go func(e Event) {
			defer wg.Done()
			_, err := j.PutEvent(e.Key, e.EventTime, e.Payload)
			if err != nil {
				t.Errorf("accept %v: %v", e, err)
			}
		}(e)
	}
	wg.Wait()
	if j.Pending() != len(events) {
		t.Fatalf("pending=%d want %d", j.Pending(), len(events))
	}
	rs := drain(t, j, 49)
	if len(rs) != len(events) {
		t.Fatalf("emitted %d, accepted %d (must be exactly once)", len(rs), len(events))
	}

	// 序号唯一。
	seen := map[int64]bool{}
	for _, r := range rs {
		if seen[r.Seq] {
			t.Fatalf("seq %d emitted twice", r.Seq)
		}
		seen[r.Seq] = true
		// 与朴素查询一致。
		wantVal, wantOK := naiveLookup(spec[r.Key], r.EventTime)
		if r.Hit != wantOK || (wantOK && string(r.Value) != wantVal) {
			t.Fatalf("%s@%d: got hit=%v val=%q, naive ok=%v val=%q, basis=%s",
				r.Key, r.EventTime, r.Hit, r.Value, wantOK, wantVal, r.Basis)
		}
		// 输出按 (eventTime, seq) 升序。
	}
	for i := 1; i < len(rs); i++ {
		if rs[i-1].EventTime > rs[i].EventTime ||
			(rs[i-1].EventTime == rs[i].EventTime && rs[i-1].Seq > rs[i].Seq) {
			t.Fatalf("output not sorted at %d: %+v then %+v", i, rs[i-1], rs[i])
		}
	}
}

// TestConcurrentWithWatermark: 并发写入事件 + 单线程推进水位线，
// 被接受事件总数 == 输出总数，且每条输出与朴素查询一致。
func TestConcurrentWithWatermark(t *testing.T) {
	j, _ := newTestJoiner(t, 10000)
	spec := map[string][]Version{
		"k1": {{EffectiveAt: 0, Value: []byte("base")}, {EffectiveAt: 30, Value: []byte("mid")}, {EffectiveAt: 70, Tombstone: true}},
		"k2": {{EffectiveAt: 10, Tombstone: true}, {EffectiveAt: 40, Value: []byte("k2-40")}},
	}
	for k, vs := range spec {
		for _, v := range vs {
			if v.Tombstone {
				must(t, j.PutTombstone(k, v.EffectiveAt))
			} else {
				must(t, j.PutVersion(k, v.EffectiveAt, v.Value))
			}
		}
	}

	const producers = 8
	const perProducer = 100
	var accepted int64
	var acceptMu sync.Mutex
	var wg sync.WaitGroup
	rng := rand.New(rand.NewSource(42))
	var rngMu sync.Mutex

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				rngMu.Lock()
				key := []string{"k1", "k2", "k3"}[rng.Intn(3)]
				at := int64(rng.Intn(100))
				rngMu.Unlock()
				_, err := j.PutEvent(key, at, nil)
				if err == nil {
					acceptMu.Lock()
					accepted++
					acceptMu.Unlock()
				}
			}
		}()
	}

	// 单个推进者：保证水位线调用本身单调。
	var emitted []Result
	var emitMu sync.Mutex
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		wm := int64(0)
		for {
			select {
			case <-stop:
				rs, err := j.AdvanceWatermark(99)
				if err != nil {
					t.Errorf("final advance: %v", err)
				}
				emitMu.Lock()
				emitted = append(emitted, rs...)
				emitMu.Unlock()
				return
			default:
				if wm < 99 {
					wm++
					rs, err := j.AdvanceWatermark(wm)
					if err != nil {
						t.Errorf("advance %d: %v", wm, err)
						return
					}
					emitMu.Lock()
					emitted = append(emitted, rs...)
					emitMu.Unlock()
				} else {
					runtime.Gosched()
				}
			}
		}
	}()
	wg.Wait() // 等待生产者结束
	close(stop)
	<-done

	emitMu.Lock()
	defer emitMu.Unlock()
	if int64(len(emitted)) != accepted {
		t.Fatalf("emitted=%d accepted=%d", len(emitted), accepted)
	}
	seqs := map[int64]int{}
	for _, r := range emitted {
		seqs[r.Seq]++
		wantVal, wantOK := naiveLookup(spec[r.Key], r.EventTime)
		if r.Hit != wantOK || (wantOK && string(r.Value) != wantVal) {
			t.Fatalf("%s@%d mismatch: %+v vs naive ok=%v %q", r.Key, r.EventTime, r, wantOK, wantVal)
		}
	}
	for s, n := range seqs {
		if n != 1 {
			t.Fatalf("seq %d emitted %d times", s, n)
		}
	}
}

// TestDeterministicReplay: 同一输入序列（含被拒绝操作）反复计算，输出逐字节一致。
func TestDeterministicReplay(t *testing.T) {
	reasonOf := func(err error) string {
		if err == nil {
			return "OK"
		}
		var re *RejectError
		if errors.As(err, &re) {
			return string(re.Reason)
		}
		return err.Error()
	}
	script := func(j *Joiner) []string {
		var transcript []string
		record := func(rs []Result, err error) {
			if err != nil {
				transcript = append(transcript, "ERR:"+reasonOf(err))
				return
			}
			for _, r := range rs {
				transcript = append(transcript, fmt.Sprintf("%d:%s@%d:%s:%t:%q@%d",
					r.Seq, r.Key, r.EventTime, r.Basis, r.Hit, string(r.Value), r.VersionEffectiveAt))
			}
		}
		record(nil, j.PutVersion("k", 10, []byte("v10")))
		_, err := j.PutEvent("k", 10, []byte("e10"))
		record(nil, err)
		record(nil, j.PutTombstone("k", 20))
		_, err = j.PutEvent("", 5, nil) // 空键
		record(nil, err)
		record(j.AdvanceWatermark(15))                    // 输出 e10 -> hit v10
		record(nil, j.PutVersion("k", 5, []byte("late"))) // 迟到版本
		record(j.AdvanceWatermark(14))                    // 水位线回退
		_, err = j.PutEvent("k", 12, nil)                 // 迟到事件
		record(nil, err)
		record(nil, j.PutVersion("k", 16, []byte("v16")))
		_, err = j.PutEvent("k", 18, nil)
		record(nil, err)
		record(j.AdvanceWatermark(25))       // 输出 e18 -> hit v16
		record(j.AdvanceWatermark(25))       // 相同水位线：空操作
		record(nil, j.PutTombstone("k", 25)) // 25 <= wm25：迟到
		return transcript
	}

	var first []string
	for run := 0; run < 3; run++ {
		j, _ := newTestJoiner(t, 10)
		got := script(j)
		if first == nil {
			first = got
			continue
		}
		if strings.Join(got, "|") != strings.Join(first, "|") {
			t.Fatalf("run %d differs:\n%v\n%v", run, first, got)
		}
	}
	// 固化期望序列，防止语义悄悄改变（成功的写操作不产生记录行）。
	want := []string{
		"ERR:empty_key",
		"1:k@10:hit:true:\"v10\"@10",
		"ERR:late_version_change",
		"ERR:watermark_regression",
		"ERR:late_event",
		"2:k@18:hit:true:\"v16\"@16",
		"ERR:late_version_change",
	}
	if strings.Join(first, "|") != strings.Join(want, "|") {
		t.Fatalf("transcript mismatch:\n got %v\nwant %v", first, want)
	}
}

// TestLogsContainInputsBasisAndResults: 日志需打印输入、连接结果与判定依据。
func TestLogsContainInputsBasisAndResults(t *testing.T) {
	j, logBuf := newTestJoiner(t, 10)
	must(t, j.PutVersion("k", 5, []byte("A")))
	mustEvent(t, j, "k", 5, []byte("payload"))
	drain(t, j, 5)
	_ = j.PutVersion("k", 5, []byte("late")) // 触发拒绝日志

	log := logBuf.String()
	for _, want := range []string{
		"version accepted", "event accepted", "event joined", "operation rejected",
		`key=k`, "event_time=5", "basis=hit", `value`, "reason=late_version_change",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\n%s", want, log)
		}
	}
}

// TestLookupNaiveSemantics: Lookup 与 Versions 快照语义。
func TestLookupNaiveSemantics(t *testing.T) {
	j, _ := newTestJoiner(t, 10)
	must(t, j.PutVersion("k", 5, []byte("A")))
	must(t, j.PutTombstone("k", 9))
	if v, ok := j.Lookup("k", 4); ok || v != nil {
		t.Fatal("before first version: miss")
	}
	if v, ok := j.Lookup("k", 5); !ok || string(v) != "A" {
		t.Fatalf("at 5: %q %v", v, ok)
	}
	if _, ok := j.Lookup("k", 9); ok {
		t.Fatal("tombstone range: miss")
	}
	if _, ok := j.Lookup("other", 100); ok {
		t.Fatal("unknown key: miss")
	}
	vs := j.Versions("k")
	if len(vs) != 2 || vs[0].EffectiveAt != 5 || !vs[1].Tombstone {
		t.Fatalf("snapshot: %+v", vs)
	}
	// 修改返回的快照不得影响内部状态。
	vs[0].Value[0] = 'X'
	if v, _ := j.Lookup("k", 5); string(v) != "A" {
		t.Fatalf("internal state aliases snapshot: %q", v)
	}
}

// naiveLookup 是测试用的朴素参照实现：在有序版本上找 EffectiveAt <= at 的最大版本。
func naiveLookup(vs []Version, at int64) (string, bool) {
	idx := sort.Search(len(vs), func(i int) bool { return vs[i].EffectiveAt > at }) - 1
	if idx < 0 || vs[idx].Tombstone {
		return "", false
	}
	return string(vs[idx].Value), true
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustEvent(t *testing.T, j *Joiner, key string, at int64, payload []byte) int64 {
	t.Helper()
	seq, err := j.PutEvent(key, at, payload)
	if err != nil {
		t.Fatal(err)
	}
	return seq
}
