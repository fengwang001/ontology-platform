package segmentlog

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func mustNew(t *testing.T, cfg Config, now time.Time) *Log {
	t.Helper()
	l, err := New(cfg, now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func mustAppend(t *testing.T, l *Log, ts time.Time, payload string) int64 {
	t.Helper()
	off, err := l.Append(Record{Time: ts, Data: []byte(payload), Bytes: int64(len(payload))})
	if err != nil {
		t.Fatalf("Append(%q at %s): %v", payload, formatTime(ts), err)
	}
	return off
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func payloads(recs [][]byte) string {
	parts := make([]string, len(recs))
	for i, r := range recs {
		parts[i] = string(r)
	}
	return strings.Join(parts, "|")
}

func TestInvalidConfig(t *testing.T) {
	cases := []Config{
		{MaxSegmentBytes: 0},
		{MaxSegmentBytes: -1},
		{MaxSegmentBytes: 10, MaxAge: -time.Second},
		{MaxSegmentBytes: 10, MaxTotalBytes: -5},
	}
	for i, cfg := range cases {
		if _, err := New(cfg, t0); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: want ErrInvalidConfig, got %v", i, err)
		}
	}
}

func TestAppendRolloverBoundaries(t *testing.T) {
	// 段容量 10：恰好填满不滚动，下一条必定滚动；任何单条 <=10 均可接受。
	l := mustNew(t, Config{MaxSegmentBytes: 10}, t0)

	mustAppend(t, l, t0, "aaaaa") // 段0: 5
	mustAppend(t, l, t0, "bbbbb") // 段0: 10，恰好填满
	if segs := l.Segments(); len(segs) != 1 || !segs[0].Active {
		t.Fatalf("exact fill must stay in active segment, got %+v", segs)
	}

	mustAppend(t, l, t0, "c") // 10+1>10，滚动出段1
	segs := l.Segments()
	if len(segs) != 2 {
		t.Fatalf("want 2 segments, got %d", len(segs))
	}
	if segs[0].Active || !segs[1].Active {
		t.Fatalf("only newest segment is active: %+v", segs)
	}
	if segs[0].StartOff != 0 || segs[0].EndOff != 10 || segs[1].StartOff != 10 || segs[1].EndOff != 11 {
		t.Fatalf("segments not contiguous: %+v", segs)
	}
	if l.TotalBytes() != 11 || l.StartOffset() != 0 {
		t.Fatalf("total=%d start=%d", l.TotalBytes(), l.StartOffset())
	}

	// 等于段容量的单条记录必须能被接受（进入新活动段）。
	mustAppend(t, l, t0, "dddddddddd")
	if segs := l.Segments(); len(segs) != 3 || !segs[2].Active {
		t.Fatalf("max-size record opens fresh segment: %+v", segs)
	}
}

func TestInvalidRecordRejectedWithoutTrace(t *testing.T) {
	l := mustNew(t, Config{MaxSegmentBytes: 4}, t0)
	mustAppend(t, l, t0, "ab")

	snapshot := func() string {
		return fmt.Sprintf("start=%d total=%d segs=%+v", l.StartOffset(), l.TotalBytes(), l.Segments())
	}
	before := snapshot()

	huge := strings.Repeat("x", 5)
	cases := []Record{
		{Time: t0, Data: nil, Bytes: 0},                           // 空数据
		{Time: time.Time{}, Data: []byte("x"), Bytes: 1},          // 无时间戳
		{Time: t0, Data: []byte("xy"), Bytes: 1},                  // 字节数与负载不一致
		{Time: t0, Data: []byte("xy"), Bytes: 0},                  // 非正字节数
		{Time: t0, Data: []byte(huge), Bytes: int64(len(huge))},   // 超段容量
		{Time: t0.Add(-time.Second), Data: []byte("z"), Bytes: 1}, // 时钟回退
	}
	for i, rec := range cases {
		_, err := l.Append(rec)
		if err == nil {
			t.Fatalf("case %d: expected rejection", i)
		}
		if !errors.Is(err, ErrInvalidRecord) && !errors.Is(err, ErrClockBackwards) {
			t.Fatalf("case %d: unexpected error class: %v", i, err)
		}
		if i == len(cases)-1 && !errors.Is(err, ErrClockBackwards) {
			t.Fatalf("last case must be clock backwards: %v", err)
		}
		if got := snapshot(); got != before {
			t.Fatalf("case %d changed state:\nbefore=%s\nafter =%s", i, before, got)
		}
	}

	// Retain 时钟回退同样不留痕。
	if _, err := l.Retain(t0.Add(-time.Second)); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("retain backwards: %v", err)
	}
	if got := snapshot(); got != before {
		t.Fatalf("backwards retain changed state:\nbefore=%s\nafter =%s", before, got)
	}
}

func TestReadRangeErrorsAndReproducibility(t *testing.T) {
	l := mustNew(t, Config{MaxSegmentBytes: 10}, t0)
	mustAppend(t, l, t0, "aaa")
	mustAppend(t, l, t0, "bbb")

	if _, err := l.Read(0, 0); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("zero budget: %v", err)
	}
	if _, err := l.Read(-1, 10); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("negative start: %v", err)
	}
	if _, err := l.Read(1, 10); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("unaligned start must be rejected: %v", err)
	}
	if _, err := l.Read(7, 10); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("start past end must be rejected: %v", err)
	}

	got, err := l.Read(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if payloads(got) != "aaa|bbb" {
		t.Fatalf("read = %q", payloads(got))
	}

	// 预算边界：只放得下第一条。
	got, err = l.Read(0, 3)
	if err != nil || payloads(got) != "aaa" {
		t.Fatalf("budget boundary: %q %v", payloads(got), err)
	}

	// 从记录边界读第二条。
	got, err = l.Read(3, 3)
	if err != nil || payloads(got) != "bbb" {
		t.Fatalf("boundary read: %q %v", payloads(got), err)
	}

	// 末尾位点空读取合法。
	got, err = l.Read(6, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("end read: %q %v", payloads(got), err)
	}

	// 可复现：外部改写传入与取回的切片都不影响日志内容。
	src := []byte("ccc")
	off, _ := l.Append(Record{Time: t0, Data: src, Bytes: 3})
	src[0] = 'X'
	got, _ = l.Read(off, 3)
	if string(got[0]) != "ccc" {
		t.Fatalf("input mutation leaked into log: %q", got[0])
	}
	got[0][0] = 'Y'
	got2, _ := l.Read(off, 3)
	if string(got2[0]) != "ccc" {
		t.Fatalf("returned slice must be a copy: %q", got2[0])
	}
}

func TestTimeRetentionBoundariesAndActive(t *testing.T) {
	l := mustNew(t, Config{MaxSegmentBytes: 6, MaxAge: 10 * time.Second}, t0)
	var logs bytes.Buffer
	l.SetDebugOutput(&logs)

	// 段0：0s/1s（LastTime=1s，6B）；段1：2s/3s（LastTime=3s，6B）；段2（活动）：4s（3B）。
	mustAppend(t, l, t0, "s0a")
	mustAppend(t, l, t0.Add(1*time.Second), "s0b")
	mustAppend(t, l, t0.Add(2*time.Second), "s1a")
	mustAppend(t, l, t0.Add(3*time.Second), "s1b")
	mustAppend(t, l, t0.Add(4*time.Second), "s2a")

	// now=13s → cutoff=3s：段0 的 LastTime=1s < 3s 删除；
	// 段1 的 LastTime=3s 恰好等于 cutoff，按规则保留并停止，段2 根本不被判定。
	rep, err := l.Retain(t0.Add(13 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.TimePhase) != 2 {
		t.Fatalf("time phase decisions = %d, want 2 (stop at first keep)", len(rep.TimePhase))
	}
	if !rep.TimePhase[0].Delete || rep.TimePhase[1].Delete {
		t.Fatalf("unexpected decisions: %+v", rep.TimePhase)
	}
	segs := l.Segments()
	if len(segs) != 2 || segs[0].ID != 1 || !segs[1].Active {
		t.Fatalf("segments after time retention: %+v", segs)
	}
	if l.StartOffset() != 6 || l.TotalBytes() != 9 {
		t.Fatalf("start=%d total=%d", l.StartOffset(), l.TotalBytes())
	}
	if rep.StartOffsetBefore != 0 || rep.StartOffsetAfter != 6 || rep.TotalBefore != 15 || rep.TotalAfter != 9 {
		t.Fatalf("report counters: %+v", rep)
	}
	// 已删除位点不可再读。
	if _, err := l.Read(0, 3); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("deleted range must be unreadable: %v", err)
	}
	// 剩余区间连续可读。
	got, err := l.Read(6, 100)
	if err != nil || payloads(got) != "s1a|s1b|s2a" {
		t.Fatalf("remaining records: %q %v", payloads(got), err)
	}

	// now=100s → cutoff=90s：段1 过期删除；段2 是活动段，永不删除。
	rep, err = l.Retain(t0.Add(100 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.TimePhase) != 2 || !rep.TimePhase[0].Delete || rep.TimePhase[1].Delete {
		t.Fatalf("active segment must stop deletion: %+v", rep.TimePhase)
	}
	segs = l.Segments()
	if len(segs) != 1 || !segs[0].Active {
		t.Fatalf("only active segment remains: %+v", segs)
	}
	if l.StartOffset() != 12 || l.TotalBytes() != 3 {
		t.Fatalf("start=%d total=%d", l.StartOffset(), l.TotalBytes())
	}

	// 只剩活动段时，即使远超保留窗口也不删除。
	rep, err = l.Retain(t0.Add(1000 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Deleted) != 0 || l.TotalBytes() != 3 {
		t.Fatalf("active segment deleted: %+v", rep)
	}

	// 判定日志必须包含输入、段列表与判定依据。
	out := logs.String()
	for _, want := range []string{"RETAIN begin", "RETAIN time seg=0 DELETE", "RETAIN time seg=1 KEEP", "STOP", "segments=["} {
		if !strings.Contains(out, want) {
			t.Fatalf("decision log missing %q:\n%s", want, out)
		}
	}
}

func TestSizeRetentionBoundaries(t *testing.T) {
	l := mustNew(t, Config{MaxSegmentBytes: 4, MaxTotalBytes: 8}, t0)
	var logs bytes.Buffer
	l.SetDebugOutput(&logs)

	// 四个段各 4B（每段一条），活动段为最后一个。
	mustAppend(t, l, t0, "a111")
	mustAppend(t, l, t0, "b222")
	mustAppend(t, l, t0, "c333")
	mustAppend(t, l, t0, "d444")
	if l.TotalBytes() != 16 {
		t.Fatalf("total=%d", l.TotalBytes())
	}

	// 16 > 8：删段0（12>8）、删段1（8<=8 停），恰好等于上限即停止。
	rep, err := l.Retain(t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.SizePhase) != 3 {
		t.Fatalf("size decisions = %d, want 3", len(rep.SizePhase))
	}
	if !rep.SizePhase[0].Delete || !rep.SizePhase[1].Delete || rep.SizePhase[2].Delete {
		t.Fatalf("unexpected size decisions: %+v", rep.SizePhase)
	}
	if l.TotalBytes() != 8 || l.StartOffset() != 8 {
		t.Fatalf("start=%d total=%d", l.StartOffset(), l.TotalBytes())
	}
	got, err := l.Read(8, 100)
	if err != nil || payloads(got) != "c333|d444" {
		t.Fatalf("remaining: %q %v", payloads(got), err)
	}

	// 上限小于单个非活动段：删到只剩活动段为止，活动段永不删除。
	l2 := mustNew(t, Config{MaxSegmentBytes: 4, MaxTotalBytes: 1}, t0)
	mustAppend(t, l2, t0, "aaaa")
	mustAppend(t, l2, t0, "bbbb")
	rep, err = l2.Retain(t0)
	if err != nil {
		t.Fatal(err)
	}
	segs := l2.Segments()
	if len(segs) != 1 || !segs[0].Active || l2.TotalBytes() != 4 {
		t.Fatalf("active segment must survive size retention: %+v total=%d", segs, l2.TotalBytes())
	}
	if len(rep.Deleted) != 1 {
		t.Fatalf("deleted=%+v", rep.Deleted)
	}

	out := logs.String()
	if !strings.Contains(out, "RETAIN size seg=0 DELETE") || !strings.Contains(out, "STOP") {
		t.Fatalf("size decision log incomplete:\n%s", out)
	}
}

func TestTwoPhaseOrder(t *testing.T) {
	// 时间阶段先删最旧两段，大小阶段再基于新总量继续删。
	l := mustNew(t, Config{MaxSegmentBytes: 2, MaxAge: 5 * time.Second, MaxTotalBytes: 4}, t0)
	mustAppend(t, l, t0, "aa")                    // 段0，LastTime=0s
	mustAppend(t, l, t0, "bb")                    // 段1，LastTime=0s
	mustAppend(t, l, t0, "cc")                    // 段2，LastTime=0s
	mustAppend(t, l, t0, "dd")                    // 段3，LastTime=0s
	mustAppend(t, l, t0.Add(6*time.Second), "ee") // 段4（活动）

	// now=6s → cutoff=1s：段0..3 的 LastTime=0s < 1s 全删；活动段保留。
	// 时间阶段后 total=2 <= 4，大小阶段无需再删。
	rep, err := l.Retain(t0.Add(6 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Deleted) != 4 || l.TotalBytes() != 2 || l.StartOffset() != 8 {
		t.Fatalf("deleted=%d start=%d total=%d", len(rep.Deleted), l.StartOffset(), l.TotalBytes())
	}
	if len(rep.SizePhase) != 1 || rep.SizePhase[0].Delete {
		t.Fatalf("size phase must stop immediately: %+v", rep.SizePhase)
	}
}

// TestContiguousReadable 校验删除整段后剩余记录仍构成连续可读区间：
// 从起始位点一次性读出全部记录，与逐条追加的内容完全一致。
func TestContiguousReadable(t *testing.T) {
	l := mustNew(t, Config{MaxSegmentBytes: 5, MaxAge: 3 * time.Second, MaxTotalBytes: 10}, t0)
	want := []string{}
	step := 0
	appendN := func(n int, ts time.Time) {
		for i := 0; i < n; i++ {
			p := fmt.Sprintf("r%03d", step)
			mustAppend(t, l, ts, p)
			want = append(want, p)
			step++
		}
	}
	appendN(4, t0)                     // 段0: r000,r001,r002,r003 (4B*4=16B → 滚动)
	appendN(3, t0.Add(1*time.Second))  // 段1
	appendN(2, t0.Add(2*time.Second))  // 段2
	appendN(5, t0.Add(10*time.Second)) // 段3（活动）

	if _, err := l.Retain(t0.Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}

	got, err := l.Read(l.StartOffset(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	// 起始位点之前的内容应已被整段删除。
	deleted := int(l.StartOffset() / 4)
	if deleted <= 0 {
		t.Fatalf("expected some deletion, start=%d", l.StartOffset())
	}
	if len(got) != len(want)-deleted {
		t.Fatalf("got %d records, want %d", len(got), len(want)-deleted)
	}
	for i, rec := range got {
		if string(rec) != want[deleted+i] {
			t.Fatalf("record %d = %q, want %q", i, rec, want[deleted+i])
		}
	}
	// 段首尾相接校验。
	segs := l.Segments()
	for i := 1; i < len(segs); i++ {
		if segs[i].StartOff != segs[i-1].EndOff {
			t.Fatalf("gap between segments: %+v", segs)
		}
	}
	if segs[0].StartOff != l.StartOffset() {
		t.Fatalf("first segment start %d != log start %d", segs[0].StartOff, l.StartOffset())
	}
}

// TestRandomAgainstNaive 用随机操作序列对比正式实现与朴素参照模型，
// 每一步打印输入、段列表与判定依据，失败时可直接定位发散点。
func TestRandomAgainstNaive(t *testing.T) {
	const (
		seeds = 8
		steps = 300
	)
	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := Config{
				MaxSegmentBytes: int64(4 + rng.Intn(20)),
				MaxAge:          time.Duration(1+rng.Intn(30)) * time.Second,
				MaxTotalBytes:   int64(8 + rng.Intn(60)),
			}
			l := mustNew(t, cfg, t0)
			var logs bytes.Buffer
			l.SetDebugOutput(&logs)
			ref := newNaive(cfg, t0)

			now := t0
			for step := 0; step < steps; step++ {
				// 时钟只前进：0~3 秒随机步长。
				now = now.Add(time.Duration(rng.Intn(4)) * time.Second)
				op := rng.Intn(10)
				switch {
				case op < 6: // 追加
					size := 1 + rng.Intn(int(cfg.MaxSegmentBytes))
					payload := bytes.Repeat([]byte{byte('a' + rng.Intn(26))}, size)
					off, err := l.Append(Record{Time: now, Data: payload, Bytes: int64(size)})
					if err != nil {
						t.Fatalf("step %d append: %v\n%s", step, err, logs.String())
					}
					if want := ref.append(now, payload, int64(size)); want != off {
						t.Fatalf("step %d offset=%d, naive=%d\n%s", step, off, want, logs.String())
					}
				case op < 9: // 保留
					if _, err := l.Retain(now); err != nil {
						t.Fatalf("step %d retain: %v\n%s", step, err, logs.String())
					}
					ref.retain(now)
				default: // 读取（从起始位点读全部）
					got, err := l.Read(l.StartOffset(), 1<<30)
					if err != nil {
						t.Fatalf("step %d read: %v\n%s", step, err, logs.String())
					}
					want := ref.read(ref.start, 1<<30)
					if len(got) != len(want) {
						t.Fatalf("step %d read %d records, naive=%d\n%s", step, len(got), len(want), logs.String())
					}
					for i := range got {
						if !bytes.Equal(got[i], want[i]) {
							t.Fatalf("step %d record %d mismatch\n%s", step, i, logs.String())
						}
					}
				}

				// 每步对比可观测状态。
				if l.StartOffset() != ref.start || l.TotalBytes() != ref.total {
					t.Fatalf("step %d state diverged: start=%d/%d total=%d/%d\n%s",
						step, l.StartOffset(), ref.start, l.TotalBytes(), ref.total, logs.String())
				}
				segs := l.Segments()
				if len(segs) != len(ref.segs) {
					t.Fatalf("step %d segment count %d != naive %d\n%s", step, len(segs), len(ref.segs), logs.String())
				}
				for i, seg := range segs {
					rs := ref.segs[i]
					if seg.StartOff != rs.start || seg.EndOff != rs.end || seg.Bytes != rs.bytes ||
						seg.RecordNum != len(rs.records) || seg.Active != rs.active {
						t.Fatalf("step %d segment %d diverged: %+v vs %+v\n%s", step, i, seg, rs, logs.String())
					}
				}
			}
			t.Logf("final: start=%d total=%d segments=%d\n%s", l.StartOffset(), l.TotalBytes(), len(l.Segments()), logs.String())
		})
	}
}
