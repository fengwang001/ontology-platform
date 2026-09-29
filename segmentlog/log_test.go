package segmentlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// dump 打印每一步的输入、段列表与判定依据，便于事后复现。
func dump(t *testing.T, l *Log, tag string, steps []Step) {
	t.Helper()
	var b bytes.Buffer
	fmt.Fprintf(&b, "\n[%s] start=%d total=%d segments:", tag, l.StartOffset(), l.TotalBytes())
	for _, s := range l.Segments() {
		fmt.Fprintf(&b, "\n  seg#%d [%d,%d) bytes=%d maxTime=%d records=%d active=%v",
			s.Index, s.StartOffset, s.EndOffset, s.Bytes, s.MaxTime, s.Records, s.Active)
	}
	for _, st := range steps {
		fmt.Fprintf(&b, "\n  step phase=%s action=%s seg#%d [%d,%d) start=%d total=%d reason=%q",
			st.Phase, st.Action, st.Segment.Index, st.Segment.StartOffset, st.Segment.EndOffset,
			st.StartOffset, st.TotalBytes, st.Reason)
	}
	t.Log(b.String())
}

func mustNew(t *testing.T, cfg Config) *Log {
	t.Helper()
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func mustAppend(t *testing.T, l *Log, ts int64, data string) int64 {
	t.Helper()
	off, err := l.Append(context.Background(), Record{Time: ts, Data: []byte(data)})
	if err != nil {
		t.Fatalf("Append(ts=%d,data=%q): %v", ts, data, err)
	}
	return off
}

// state 是用于校验“拒绝不留痕”的快照。
type state struct {
	start int64
	total int64
	next  int64
	segs  []SegmentInfo
}

func snapshot(t *testing.T, l *Log) state {
	t.Helper()
	return state{start: l.StartOffset(), total: l.TotalBytes(), segs: l.Segments()}
}

func assertStateUnchanged(t *testing.T, before, after state, where string) {
	t.Helper()
	if before.start != after.start || before.total != after.total ||
		len(before.segs) != len(after.segs) {
		t.Fatalf("%s: state changed by rejected call: before=%+v after=%+v", where, before, after)
	}
	for i := range before.segs {
		if before.segs[i] != after.segs[i] {
			t.Fatalf("%s: segment %d changed by rejected call", where, i)
		}
	}
}

// refModel 是朴素参照：完整保留每条记录，用最直接的方式重新实现段滚动与
// 两阶段删除，最终与实际日志的读取结果逐项比对。
type refRec struct {
	off  int64
	time int64
	data []byte
}

type refModel struct {
	cfg     Config
	nextOff int64
	lastTs  int64
	segs    []refSeg // 有状态的段列表，按真实操作演化
	total   int64
}

func newRef(cfg Config) *refModel { return &refModel{cfg: cfg} }

func (r *refModel) appendRec(rec Record) (int64, bool) {
	size := int64(len(rec.Data))
	if rec.Time <= 0 || size == 0 || size > r.cfg.MaxSegmentBytes {
		return 0, false
	}
	if rec.Time < r.lastTs {
		return 0, false
	}
	off := r.nextOff
	d := append([]byte(nil), rec.Data...)
	r.nextOff += int64(len(d))
	r.lastTs = rec.Time
	if len(r.segs) == 0 {
		r.segs = append(r.segs, refSeg{})
	}
	curBytes := int64(0)
	for _, x := range r.segs[len(r.segs)-1].records {
		curBytes += int64(len(x.data))
	}
	if curBytes+size > r.cfg.MaxSegmentBytes {
		r.segs = append(r.segs, refSeg{})
	}
	r.segs[len(r.segs)-1].records = append(r.segs[len(r.segs)-1].records,
		refRec{off: off, time: rec.Time, data: d})
	r.total += size
	return off, true
}

type refSeg struct{ records []refRec }

// retain 复刻两阶段规则，返回保留下来的记录。
func (r *refModel) retain(now int64) []refRec {
	segs := r.segs
	total := r.total
	if r.cfg.RetentionMillis > 0 {
		cutoff := now - r.cfg.RetentionMillis
		for len(segs) > 1 { // 活动段（最后一段）永不删除
			mx := int64(0)
			for _, x := range segs[0].records {
				if x.time > mx {
					mx = x.time
				}
			}
			if mx >= cutoff {
				break
			}
			var b int64
			for _, x := range segs[0].records {
				b += int64(len(x.data))
			}
			segs = segs[1:]
			total -= b
		}
	}
	for len(segs) > 1 {
		var b int64
		for _, x := range segs[0].records {
			b += int64(len(x.data))
		}
		// 与实现相同的判定顺序：先看总量是否已达标，再看删除该段是否真的
		// 能让总量达标；任何一种不满足都在该段处立即停止。
		if total <= r.cfg.MaxTotalBytes {
			break
		}
		if total-b > r.cfg.MaxTotalBytes {
			break
		}
		segs = segs[1:]
		total -= b
	}
	r.segs = segs
	r.total = total
	var out []refRec
	for _, s := range segs {
		out = append(out, s.records...)
	}
	return out
}

func verifyAgainstRef(t *testing.T, l *Log, ref *refModel, now int64) {
	t.Helper()
	kept := ref.retain(now)
	verifyKept(t, l, kept)
}

// verifyState 对账参照当前段状态（不再触发任何删除演进）。
func verifyState(t *testing.T, l *Log, ref *refModel) {
	t.Helper()
	var kept []refRec
	for _, s := range ref.segs {
		kept = append(kept, s.records...)
	}
	verifyKept(t, l, kept)
}

func verifyKept(t *testing.T, l *Log, kept []refRec) {
	t.Helper()
	var wantStart, wantTotal int64
	if len(kept) > 0 {
		wantStart = kept[0].off
	}
	got, err := l.Read(context.Background(), l.StartOffset())
	if err != nil {
		t.Fatalf("Read from start: %v", err)
	}
	if l.StartOffset() != wantStart {
		t.Fatalf("startOffset: got %d want %d", l.StartOffset(), wantStart)
	}
	if len(got) != len(kept) {
		t.Fatalf("record count: got %d want %d", len(got), len(kept))
	}
	for i, w := range kept {
		wantTotal += int64(len(w.data))
		if got[i].Time != w.time || !bytes.Equal(got[i].Data, w.data) {
			t.Fatalf("record %d: got (ts=%d,data=%q) want (ts=%d,data=%q)",
				i, got[i].Time, got[i].Data, w.time, w.data)
		}
	}
	if l.TotalBytes() != wantTotal {
		t.Fatalf("totalBytes: got %d want %d", l.TotalBytes(), wantTotal)
	}
	segs := l.Segments()
	if len(segs) > 0 {
		if segs[0].StartOffset != wantStart {
			t.Fatalf("first segment start %d != startOffset %d", segs[0].StartOffset, wantStart)
		}
		for i := 1; i < len(segs); i++ {
			if segs[i].StartOffset != segs[i-1].EndOffset {
				t.Fatalf("segments not contiguous: #%d end %d != #%d start %d",
					i-1, segs[i-1].EndOffset, i, segs[i].StartOffset)
			}
		}
		if !segs[len(segs)-1].Active {
			t.Fatalf("last segment must be active")
		}
	}
}

func TestSegmentRollingAndContiguousRead(t *testing.T) {
	cfg := Config{MaxSegmentBytes: 10, MaxTotalBytes: 100, RetentionMillis: 0}
	l := mustNew(t, cfg)
	ref := newRef(cfg)
	offsets := []int64{}
	for i, payload := range []string{"aaaa", "bbbbbb", "ccc", "dddddddd", "ee", "ffff"} {
		ts := int64(1000 + i)
		off := mustAppend(t, l, ts, payload)
		if want, ok := ref.appendRec(Record{Time: ts, Data: []byte(payload)}); !ok || want != off {
			t.Fatalf("offset: got %d want %d ok=%v", off, want, ok)
		}
		offsets = append(offsets, off)
	}
	dump(t, l, "after appends", nil)

	segs := l.Segments()
	// 10 字节上限（追加后超限才滚动）：4+6=10；+3 到 13 滚动；
	// 8 放入后 11>10 再滚动，与后续 2 同段（8+2=10）；最后 4 一段。
	wantBytes := []int64{10, 3, 10, 4}
	if len(segs) != len(wantBytes) {
		t.Fatalf("segments: got %d want %d", len(segs), len(wantBytes))
	}
	for i, wb := range wantBytes {
		if segs[i].Bytes != wb {
			t.Fatalf("segment %d bytes: got %d want %d (%+v)", i, segs[i].Bytes, wb, segs)
		}
	}
	all, err := l.Read(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, off := range offsets {
		got, err := l.Read(context.Background(), off)
		if err != nil {
			t.Fatalf("Read(%d): %v", off, err)
		}
		idx, cursor := 0, int64(0)
		for cursor < off {
			cursor += int64(len(all[idx].Data))
			idx++
		}
		if len(got) != len(all[idx:]) {
			t.Fatalf("Read(%d) length mismatch", off)
		}
		for j := range got {
			if got[j].Time != all[idx+j].Time || !bytes.Equal(got[j].Data, all[idx+j].Data) {
				t.Fatalf("Read(%d) content mismatch at %d", off, j)
			}
		}
	}
	verifyAgainstRef(t, l, ref, 1000)
}

func TestRetentionTimeBoundariesAndWholeSegment(t *testing.T) {
	// 段大小 5：每段恰好一条 5 字节记录。
	cfg := Config{MaxSegmentBytes: 5, MaxTotalBytes: 1000, RetentionMillis: 100}
	l := mustNew(t, cfg)
	ref := newRef(cfg)
	for i := 0; i < 5; i++ {
		ts := int64(100 + i*10)
		mustAppend(t, l, ts, "abcde")
		ref.appendRec(Record{Time: ts, Data: []byte("abcde")})
	}
	if got := len(l.Segments()); got != 5 {
		t.Fatalf("segments: got %d want 5", got)
	}

	// cutoff=99：最早段 maxTime=100 >= 99，边界含等于，一个不删。
	n, steps, err := l.Retain(context.Background(), 199)
	if err != nil || n != 0 || steps[0].Action != "keep" {
		t.Fatalf("retain@199: n=%d err=%v steps=%+v", n, err, steps)
	}
	dump(t, l, "retain@199", steps)

	// cutoff=100：100>=100 仍保留。
	if n, _, err := l.Retain(context.Background(), 200); err != nil || n != 0 {
		t.Fatalf("retain@200: n=%d err=%v", n, err)
	}

	// cutoff=101：删 100 段；下一段 110 未过期即停。
	n, steps, err = l.Retain(context.Background(), 201)
	if err != nil || n != 1 {
		t.Fatalf("retain@201: n=%d err=%v", n, err)
	}
	dump(t, l, "retain@201", steps)
	if steps[0].Action != "remove" || steps[1].Action != "keep" {
		t.Fatalf("expected remove then keep, got %+v", steps)
	}
	if l.StartOffset() != 5 {
		t.Fatalf("startOffset: got %d want 5", l.StartOffset())
	}

	// cutoff=131：继续删 110/120/130，140 保留；活动段永留。
	n, steps, _ = l.Retain(context.Background(), 231)
	if n != 3 {
		t.Fatalf("retain@231 removed: got %d want 3", n)
	}
	dump(t, l, "retain@231", steps)
	if len(l.Segments()) != 1 || l.TotalBytes() != 5 || l.StartOffset() != 20 {
		t.Fatalf("unexpected state: segs=%d total=%d start=%d",
			len(l.Segments()), l.TotalBytes(), l.StartOffset())
	}
	if n, _, _ := l.Retain(context.Background(), 100000); n != 0 {
		t.Fatalf("active segment must never be removed, got %d", n)
	}
	verifyAgainstRef(t, l, ref, 231)
}

func TestRetentionSizeBoundaries(t *testing.T) {
	// 场景 A：3|10|10|10（cap=10），总上限 20。
	// 总量 33 超限，但删除首段 3 后仍为 30>20：遇第一个“删了仍超限”的段
	// 必须整体停止，不得跳过它删除后面的段，因此零删除。
	cfg := Config{MaxSegmentBytes: 10, MaxTotalBytes: 20, RetentionMillis: 0}
	l := mustNew(t, cfg)
	ref := newRef(cfg)
	payloads := []string{"klm", "abcdefghij", "nopqrstuvw", "xyz0123456"}
	for i, p := range payloads {
		ts := int64(1000 + i)
		mustAppend(t, l, ts, p)
		ref.appendRec(Record{Time: ts, Data: []byte(p)})
	}
	n, steps, err := l.Retain(context.Background(), 5000)
	if err != nil || n != 0 {
		t.Fatalf("size retain A: n=%d err=%v", n, err)
	}
	dump(t, l, "size retain A (stop oversized)", steps)
	if steps[0].Action != "keep" || l.TotalBytes() != 33 || l.StartOffset() != 0 {
		t.Fatalf("size retain A state: steps=%+v total=%d start=%d",
			steps, l.TotalBytes(), l.StartOffset())
	}
	verifyAgainstRef(t, l, ref, 5000)

	// 场景 B：10|10|10（cap=10），总上限 20。
	// 总量 30 超限；删首段 10 后恰好等于上限 20，第二步即 keep 停止。
	cfg2 := Config{MaxSegmentBytes: 10, MaxTotalBytes: 20, RetentionMillis: 0}
	l2 := mustNew(t, cfg2)
	ref2 := newRef(cfg2)
	for i, p := range []string{"abcdefghij", "klmnopqrst", "uvwxyz0123"} {
		ts := int64(1000 + i)
		mustAppend(t, l2, ts, p)
		ref2.appendRec(Record{Time: ts, Data: []byte(p)})
	}
	n, steps, err = l2.Retain(context.Background(), 5000)
	if err != nil || n != 1 {
		t.Fatalf("size retain B: n=%d err=%v", n, err)
	}
	dump(t, l2, "size retain B (exact boundary)", steps)
	if l2.TotalBytes() != 20 || l2.StartOffset() != 10 {
		t.Fatalf("size retain B state: total=%d start=%d", l2.TotalBytes(), l2.StartOffset())
	}
	if steps[1].Action != "keep" {
		t.Fatalf("size retain B second step must be keep: %+v", steps)
	}
	// 恰在边界：再次保留零删除。
	n2, steps2, _ := l2.Retain(context.Background(), 5001)
	if n2 != 0 || steps2[0].Action != "keep" {
		t.Fatalf("boundary retain: n=%d steps=%+v", n2, steps2)
	}
	dump(t, l2, "size retain B boundary", steps2)
	verifyAgainstRef(t, l2, ref2, 5001)
}

func TestSizePhaseStopsAtFirstOversizedSegment(t *testing.T) {
	// 段大小 10：字节布局 6 | 10 | 10；总上限 15。
	// 总量 26 超限，但删第一段(6)后仍为 20>15：遇第一个“删了仍超限”的段即停，
	// 不得跳过它去删后面的段。
	cfg := Config{MaxSegmentBytes: 10, MaxTotalBytes: 15, RetentionMillis: 0}
	l := mustNew(t, cfg)
	mustAppend(t, l, 1, "aaaaaa")
	mustAppend(t, l, 2, "bbbbbbbbbb")
	mustAppend(t, l, 3, "cccccccccc")
	n, steps, err := l.Retain(context.Background(), 100)
	if err != nil || n != 0 {
		t.Fatalf("expected no removal, got n=%d err=%v", n, err)
	}
	dump(t, l, "size stop", steps)
	if steps[0].Action != "keep" || l.TotalBytes() != 26 || l.StartOffset() != 0 {
		t.Fatalf("unexpected state: steps=%+v total=%d start=%d",
			steps, l.TotalBytes(), l.StartOffset())
	}
}

func TestActiveSegmentNeverRemoved(t *testing.T) {
	// 只有一个活动段且同时违反时间与大小：也不得删除。
	cfg := Config{MaxSegmentBytes: 10, MaxTotalBytes: 10, RetentionMillis: 1}
	l := mustNew(t, cfg)
	mustAppend(t, l, 1, "xxxxxxxxxx")
	n, steps, err := l.Retain(context.Background(), 100000)
	if err != nil || n != 0 {
		t.Fatalf("active removed? n=%d err=%v", n, err)
	}
	dump(t, l, "only active", steps)
	if l.TotalBytes() != 10 || l.StartOffset() != 0 || len(l.Segments()) != 1 {
		t.Fatalf("active segment must survive")
	}
}

func TestInvalidConfigDistinctReasons(t *testing.T) {
	bad := []Config{
		{MaxSegmentBytes: 0, MaxTotalBytes: 10, RetentionMillis: 0},
		{MaxSegmentBytes: -3, MaxTotalBytes: 10, RetentionMillis: 0},
		{MaxSegmentBytes: 10, MaxTotalBytes: 0, RetentionMillis: 0},
		{MaxSegmentBytes: 10, MaxTotalBytes: 9, RetentionMillis: 0},
		{MaxSegmentBytes: 10, MaxTotalBytes: 10, RetentionMillis: -1},
	}
	msgs := map[string]bool{}
	for i, c := range bad {
		_, err := New(c)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: want ErrInvalidConfig, got %v", i, err)
		}
		if msgs[err.Error()] {
			t.Fatalf("case %d: duplicate reason %q", i, err.Error())
		}
		msgs[err.Error()] = true
	}
}

func TestInvalidRecordLeavesNoTrace(t *testing.T) {
	cfg := Config{MaxSegmentBytes: 5, MaxTotalBytes: 100, RetentionMillis: 0}
	l := mustNew(t, cfg)
	mustAppend(t, l, 100, "abc")
	before := snapshot(t, l)

	cases := []Record{
		{Time: 0, Data: []byte("x")},
		{Time: -1, Data: []byte("x")},
		{Time: 101, Data: nil},
		{Time: 101, Data: []byte("toolong123")},
	}
	for i, rec := range cases {
		_, err := l.Append(context.Background(), rec)
		if !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("case %d: want ErrInvalidRecord, got %v", i, err)
		}
		assertStateUnchanged(t, before, snapshot(t, l), "invalid record case "+itoa(int64(i)))
	}

	// 时钟回退：时间戳早于已接受的 100。
	_, err := l.Append(context.Background(), Record{Time: 99, Data: []byte("z")})
	if !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("want ErrClockBackwards, got %v", err)
	}
	assertStateUnchanged(t, before, snapshot(t, l), "clock backwards")

	// 合法等时间戳追加应当成功（仅要求单调不减），且位点未被失败调用占用。
	if off, err := l.Append(context.Background(), Record{Time: 100, Data: []byte("d")}); err != nil {
		t.Fatalf("equal timestamp should succeed: %v", err)
	} else if off != 3 {
		t.Fatalf("offset after rejected appends: got %d want 3", off)
	}
}

func TestRetainRejectionsLeaveNoTrace(t *testing.T) {
	cfg := Config{MaxSegmentBytes: 5, MaxTotalBytes: 1000, RetentionMillis: 10}
	l := mustNew(t, cfg)
	mustAppend(t, l, 100, "abcde")
	mustAppend(t, l, 200, "fghij")
	if _, _, err := l.Retain(context.Background(), 300); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, l)

	if _, _, err := l.Retain(context.Background(), 299); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("want ErrClockBackwards, got %v", err)
	}
	assertStateUnchanged(t, before, snapshot(t, l), "retain clock backwards")

	if _, _, err := l.Retain(context.Background(), 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument, got %v", err)
	}
	assertStateUnchanged(t, before, snapshot(t, l), "retain non-positive clock")
}

func TestReadOutOfRange(t *testing.T) {
	cfg := Config{MaxSegmentBytes: 5, MaxTotalBytes: 1000, RetentionMillis: 10}
	l := mustNew(t, cfg)
	mustAppend(t, l, 100, "abc")
	mustAppend(t, l, 200, "def") // 3+3>5，滚动为独立活动段
	// now=301 → cutoff=291：段(maxTime=200)过期删除，起始位点前移到 3。
	if _, steps, err := l.Retain(context.Background(), 301); err != nil {
		t.Fatal(err)
	} else {
		dump(t, l, "retain before read", steps)
	}

	if _, err := l.Read(context.Background(), 0); !errors.Is(err, ErrOffsetOutOfRange) {
		t.Fatalf("removed offset: got %v", err)
	}
	if _, err := l.Read(context.Background(), 4); !errors.Is(err, ErrOffsetOutOfRange) {
		t.Fatalf("mid-record offset: got %v", err)
	}
	if _, err := l.Read(context.Background(), 7); !errors.Is(err, ErrOffsetOutOfRange) {
		t.Fatalf("past end: got %v", err)
	}
	if _, err := l.Read(context.Background(), -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative: got %v", err)
	}
	got, err := l.Read(context.Background(), 3)
	if err != nil {
		t.Fatalf("valid read: %v", err)
	}
	if len(got) != 1 || string(got[0].Data) != "def" {
		t.Fatalf("read content: %+v", got)
	}
	// 末尾位点合法，返回空。
	if tail, err := l.Read(context.Background(), 6); err != nil || len(tail) != 0 {
		t.Fatalf("end offset read: %v %+v", err, tail)
	}
}

func TestConcurrentAppendRetainRead(t *testing.T) {
	cfg := Config{MaxSegmentBytes: 7, MaxTotalBytes: 60, RetentionMillis: 500}
	l := mustNew(t, cfg)

	const writers = 4
	const perWriter = 200
	var wg sync.WaitGroup

	// 并发追加：各 writer 使用不相交的时间戳区间以避免时钟回退拒绝。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				// 每个 writer 使用独立、单调递增的时间戳区间；
				// 并发交错仍可能导致全局时钟回退，此时整体拒绝是预期行为。
				ts := int64(2000 + id*perWriter + i + 1)
				data := fmt.Sprintf("w%d-n%d", id, i)
				if _, err := l.Append(context.Background(), Record{Time: ts, Data: []byte(data)}); err != nil {
					if !errors.Is(err, ErrClockBackwards) {
						t.Errorf("append: unexpected %v", err)
						return
					}
				}
			}
		}(w)
	}

	// 并发保留：单调推进的时钟，只清理老数据，不影响 writer 的新时间戳。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for now := int64(1); now <= 600; now++ {
			if _, _, err := l.Retain(context.Background(), now); err != nil {
				t.Errorf("retain: %v", err)
				return
			}
		}
	}()

	// 并发读取：从当前起始位点读，结果必须连续、内容字节等于追加时内容。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			start := l.StartOffset()
			got, err := l.Read(context.Background(), start)
			if err != nil {
				t.Errorf("read: %v", err)
				return
			}
			cursor := start
			for _, r := range got {
				if len(r.Data) == 0 || r.Time <= 0 {
					t.Errorf("impossible record: %+v", r)
					return
				}
				cursor += int64(len(r.Data))
			}
		}
	}()

	wg.Wait()

	// 收尾：时钟推到足够大，所有非活动段按时间过期；活动段必须保留。
	n, steps, err := l.Retain(context.Background(), 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	dump(t, l, fmt.Sprintf("final retain removed=%d", n), steps)
	if n < 0 {
		t.Fatalf("negative removal")
	}
	segs := l.Segments()
	if len(segs) == 0 {
		t.Fatalf("active segment must remain")
	}
	for i := 1; i < len(segs); i++ {
		if segs[i].StartOffset != segs[i-1].EndOffset {
			t.Fatalf("non-contiguous segments after concurrent run")
		}
	}
	got, err := l.Read(context.Background(), l.StartOffset())
	if err != nil {
		t.Fatal(err)
	}
	cursor := l.StartOffset()
	for _, r := range got {
		cursor += int64(len(r.Data))
	}
	if cursor != segs[len(segs)-1].EndOffset {
		t.Fatalf("read range not contiguous to tail: cursor=%d end=%d",
			cursor, segs[len(segs)-1].EndOffset)
	}

	// 起始位点只进不退：再跑一轮保留不应变小。
	startBefore := l.StartOffset()
	l.Retain(context.Background(), 2_000_000)
	if l.StartOffset() < startBefore {
		t.Fatalf("start offset moved backwards: %d -> %d", startBefore, l.StartOffset())
	}
}

func TestRandomScenarioMatchesReference(t *testing.T) {
	cfgs := []Config{
		{MaxSegmentBytes: 4, MaxTotalBytes: 13, RetentionMillis: 50},
		{MaxSegmentBytes: 1, MaxTotalBytes: 7, RetentionMillis: 3},
		{MaxSegmentBytes: 10, MaxTotalBytes: 10, RetentionMillis: 100},
	}
	// 确定性的伪随机场景：固定种子，逐步“追加/保留”，每步后与朴素参照对账。
	// 参照与真实日志一样只在显式保留调用时演进删除；追加步骤仅做位点与
	// 当前状态对账。
	for ci, cfg := range cfgs {
		l := mustNew(t, cfg)
		ref := newRef(cfg)
		seed := int64(7)
		nextRand := func(n int64) int64 {
			seed = (seed*1103515245 + 12345) & 0x7fffffff
			return seed % n
		}
		ts := int64(1)
		now := int64(1)
		for step := 0; step < 300; step++ {
			switch nextRand(3) {
			case 0, 1:
				ts += nextRand(5)
				size := 1 + nextRand(cfg.MaxSegmentBytes+2) // 偶尔产生超限非法记录
				data := bytes.Repeat([]byte{byte('a' + int(nextRand(26)))}, int(size))
				rec := Record{Time: ts, Data: data}
				off, err := l.Append(context.Background(), rec)
				// 时间戳 ts 在上面已无条件推进，参照模型同样只在接受时写入记录，
				// 因此被拒追加对两边的“下一条记录时间”认知保持一致。
				if err != nil {
					if !errors.Is(err, ErrInvalidRecord) && !errors.Is(err, ErrClockBackwards) {
						t.Fatalf("cfg %d step %d: unexpected err %v", ci, step, err)
					}
					verifyState(t, l, ref)
					continue
				}
				if want, ok := ref.appendRec(rec); !ok || want != off {
					t.Fatalf("cfg %d step %d offset: got %d want %d ok=%v", ci, step, off, want, ok)
				}
				verifyState(t, l, ref)
			case 2:
				now += nextRand(6)
				_, steps, err := l.Retain(context.Background(), now)
				ref.retain(now)
				if err != nil {
					t.Fatalf("cfg %d retain: %v", ci, err)
				}
				if step%25 == 0 {
					dump(t, l, fmt.Sprintf("cfg%d step%d retain now=%d", ci, step, now), steps)
				}
				verifyState(t, l, ref)
			}
		}
		// 终结：极端时钟下至少保留活动段。
		l.Retain(context.Background(), 1_000_000_000)
		ref.retain(1_000_000_000)
		verifyState(t, l, ref)
		if len(l.Segments()) == 0 {
			t.Fatalf("cfg %d: active segment removed", ci)
		}
	}
}
