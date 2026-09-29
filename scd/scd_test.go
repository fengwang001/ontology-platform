package scd

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func fmtEvent(e Event) string {
	if e.Deleted {
		return fmt.Sprintf("(key=%q t=%d DELETE)", e.Key, e.EffectiveTime)
	}
	return fmt.Sprintf("(key=%q t=%d value=%q)", e.Key, e.EffectiveTime, e.Value)
}

func fmtEvents(es []Event) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = fmtEvent(e)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func fmtTime(t int64) string {
	if t == OpenEnd {
		return "+Inf"
	}
	return fmt.Sprintf("%d", t)
}

func fmtInterval(iv Interval) string {
	return fmt.Sprintf("[%s,%s)=%q", fmtTime(iv.Start), fmtTime(iv.End), iv.Value)
}

func fmtIntervals(iv []Interval) string {
	if len(iv) == 0 {
		return "(empty)"
	}
	parts := make([]string, len(iv))
	for i, v := range iv {
		parts[i] = fmtInterval(v)
	}
	return strings.Join(parts, "; ")
}

func commitStep(t *testing.T, s *Store, desc string, events []Event) []Rejection {
	t.Helper()
	t.Logf("STEP %-40s 输入=%s", desc, fmtEvents(events))
	rej := s.Commit(events)
	if rej == nil {
		t.Logf("    判定: 提交接受（全部输入合法且未超限）")
	} else {
		for _, r := range rej {
			t.Logf("    判定: 整批拒绝 原因=%s %s", r.Reason, r.Message)
		}
	}
	return rej
}

func logHistory(t *testing.T, s *Store, key string) []Interval {
	t.Helper()
	h := s.History(key)
	t.Logf("    历史 key=%q: %s", key, fmtIntervals(h))
	return h
}

func expectIntervals(t *testing.T, got []Interval, want []Interval, why string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("区间不一致\n  判定依据: %s\n  期望: %s\n  实际: %s",
			why, fmtIntervals(want), fmtIntervals(got))
	}
	t.Logf("    校验: 区间与预期一致（%s）", why)
}

func expectValue(t *testing.T, s *Store, key string, at int64, wantVal string, wantOK bool) {
	t.Helper()
	v, ok := s.ValueAt(key, at)
	if v != wantVal || ok != wantOK {
		t.Fatalf("ValueAt(key=%q,t=%d)=(%q,%v), 期望=(%q,%v)", key, at, v, ok, wantVal, wantOK)
	}
	t.Logf("    校验: ValueAt(t=%d)=(%q,%v)，判定依据: 左闭右开且 t 恰等于变更点时命中该点行",
		at, v, ok)
}

func iv(key string, start, end int64, value string) Interval {
	return Interval{Key: key, Start: start, End: end, Value: value}
}

// TestSequentialOpenAndClose 覆盖顺序到达的新开区间与闭合区间。
func TestSequentialOpenAndClose(t *testing.T) {
	s := NewStore(10)
	s.SetLogger(nil)
	const k = "emp-1"

	commitStep(t, s, "新开：首个更新点产生无界区间", []Event{{Key: k, EffectiveTime: 10, Value: "A"}})
	expectIntervals(t, logHistory(t, s, k),
		[]Interval{iv(k, 10, OpenEnd, "A")},
		"单个更新点到下一个变更点（无）产生 [10,+Inf) 无界区间")

	commitStep(t, s, "闭合：后续点闭合前行并新开", []Event{{Key: k, EffectiveTime: 20, Value: "B"}})
	expectIntervals(t, logHistory(t, s, k),
		[]Interval{iv(k, 10, 20, "A"), iv(k, 20, OpenEnd, "B")},
		"点 20 把 [10,+Inf) 闭合为 [10,20)，并新开 [20,+Inf)")

	commitStep(t, s, "删除点不产生区间", []Event{{Key: k, EffectiveTime: 30, Deleted: true}})
	expectIntervals(t, logHistory(t, s, k),
		[]Interval{iv(k, 10, 20, "A"), iv(k, 20, 30, "B")},
		"删除点只作为上一行右端，自身不产生区间；删除空隙不命中任何行")

	expectValue(t, s, k, 5, "", false)
	expectValue(t, s, k, 10, "A", true)
	expectValue(t, s, k, 19, "A", true)
	expectValue(t, s, k, 20, "B", true)
	expectValue(t, s, k, 29, "B", true)
	expectValue(t, s, k, 30, "", false)
	expectValue(t, s, k, 100, "", false)

	if err := s.CheckInvariants(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Logf("    自检通过: 区间升序、不重叠、任意时间点至多命中一行")
}

// TestOutOfOrderSplit 覆盖乱序到达事件对已有行的拆分。
func TestOutOfOrderSplit(t *testing.T) {
	s := NewStore(10)
	s.SetLogger(nil)
	const k = "emp-2"

	commitStep(t, s, "先建立 [10,30) 与 [30,+Inf)", []Event{
		{Key: k, EffectiveTime: 10, Value: "A"},
		{Key: k, EffectiveTime: 30, Value: "C"},
	})
	expectIntervals(t, logHistory(t, s, k),
		[]Interval{iv(k, 10, 30, "A"), iv(k, 30, OpenEnd, "C")},
		"历史只取决于变更点集合，与其到达顺序无关")

	commitStep(t, s, "乱序：t=20 落在 [10,30) 内部，拆分该行",
		[]Event{{Key: k, EffectiveTime: 20, Value: "B"}})
	expectIntervals(t, logHistory(t, s, k),
		[]Interval{iv(k, 10, 20, "A"), iv(k, 20, 30, "B"), iv(k, 30, OpenEnd, "C")},
		"生效时间落在某行内部时拆分该行为两行")

	commitStep(t, s, "乱序：t=5 早于最早点，新开首行",
		[]Event{{Key: k, EffectiveTime: 5, Value: "Z"}})
	expectIntervals(t, logHistory(t, s, k),
		[]Interval{
			iv(k, 5, 10, "Z"),
			iv(k, 10, 20, "A"),
			iv(k, 20, 30, "B"),
			iv(k, 30, OpenEnd, "C"),
		},
		"插入比所有点更早的事件只影响首行，其余区间首尾不变（增量）")

	expectValue(t, s, k, 4, "", false)
	expectValue(t, s, k, 5, "Z", true)
	expectValue(t, s, k, 9, "Z", true)
	expectValue(t, s, k, 20, "B", true)
	expectValue(t, s, k, 30, "C", true)

	all := []Event{
		{Key: k, EffectiveTime: 10, Value: "A"},
		{Key: k, EffectiveTime: 30, Value: "C"},
		{Key: k, EffectiveTime: 20, Value: "B"},
		{Key: k, EffectiveTime: 5, Value: "Z"},
	}
	got := logHistory(t, s, k)
	expectIntervals(t, got, Recompute(k, all),
		"增量结果必须与全量事件批量重算完全一致")

	if err := s.CheckInvariants(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestReplaceSameInstant 覆盖同一生效时间只保留后到者，以及相邻同值不合并。
func TestReplaceSameInstant(t *testing.T) {
	s := NewStore(10)
	s.SetLogger(nil)
	const k = "emp-3"

	commitStep(t, s, "初始点", []Event{{Key: k, EffectiveTime: 10, Value: "A"}})
	commitStep(t, s, "替换：同一时刻后到者替换该点",
		[]Event{{Key: k, EffectiveTime: 10, Value: "A2"}})
	expectIntervals(t, logHistory(t, s, k),
		[]Interval{iv(k, 10, OpenEnd, "A2")},
		"恰等于已有变更点则替换该点，不新增变更点、不复制区间")

	commitStep(t, s, "替换为删除点：删除点不产生区间",
		[]Event{{Key: k, EffectiveTime: 10, Deleted: true}})
	expectIntervals(t, logHistory(t, s, k), nil,
		"唯一点被替换为删除点后历史为空")

	commitStep(t, s, "同批同刻折叠为后到者",
		[]Event{
			{Key: k, EffectiveTime: 10, Value: "first"},
			{Key: k, EffectiveTime: 10, Value: "second"},
		})
	expectIntervals(t, logHistory(t, s, k),
		[]Interval{iv(k, 10, OpenEnd, "second")},
		"同一批内同键同时刻只保留后出现者")

	commitStep(t, s, "相邻两行值相同也不合并",
		[]Event{{Key: k, EffectiveTime: 20, Value: "second"}})
	expectIntervals(t, logHistory(t, s, k),
		[]Interval{iv(k, 10, 20, "second"), iv(k, 20, OpenEnd, "second")},
		"规则明确不做相邻同值合并")

	if err := s.CheckInvariants(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestInvalidInputRejectedAtomically 覆盖非法输入的可区分原因与失败不留痕。
func TestInvalidInputRejectedAtomically(t *testing.T) {
	s := NewStore(3)
	s.SetLogger(nil)
	const k = "emp-4"
	commitStep(t, s, "预置合法状态", []Event{{Key: k, EffectiveTime: 10, Value: "A"}})
	before := s.History(k)

	bad := []Event{
		{Key: "", EffectiveTime: 1, Value: "no-key"},
		{Key: k, EffectiveTime: 2, Value: ""},
		{Key: k, EffectiveTime: math.MaxInt64, Value: "too-late"},
		{Key: "other", EffectiveTime: 5, Value: "valid-but-must-be-rejected"},
	}
	rej := commitStep(t, s, "混合非法批次必须整批拒绝", bad)
	if rej == nil {
		t.Fatal("期望整批拒绝，实际被接受")
	}
	seen := map[RejectReason]bool{}
	for _, r := range rej {
		if seen[r.Reason] {
			t.Fatalf("拒绝原因出现重复: %s", r.Reason)
		}
		seen[r.Reason] = true
	}
	for _, want := range []RejectReason{ReasonEmptyKey, ReasonEmptyValue, ReasonEffectiveTimeOutOfRange} {
		if !seen[want] {
			t.Fatalf("缺少可区分的拒绝原因 %s，实际原因集合=%v", want, seen)
		}
	}
	t.Logf("    判定依据: 拒绝原因互不相同且可区分；非法点 %d 超过 MaxEffectiveTime=%d",
		math.MaxInt64, MaxEffectiveTime)
	expectIntervals(t, s.History(k), before, "整批拒绝后历史必须与提交前完全一致（失败不留痕）")
	if v, ok := s.ValueAt("other", 5); ok || v != "" {
		t.Fatal("被拒批次中的合法事件不得落库")
	}

	if rej := commitStep(t, s, "空批次", nil); len(rej) != 1 || rej[0].Reason != ReasonEmptyBatch {
		t.Fatalf("空批次应给出 EMPTY_BATCH，实际=%v", rej)
	}

	commitStep(t, s, "补满变更点至上限 3",
		[]Event{
			{Key: k, EffectiveTime: 20, Value: "B"},
			{Key: k, EffectiveTime: 30, Value: "C"},
		})
	rej = commitStep(t, s, "超限批次必须整批拒绝",
		[]Event{
			{Key: k, EffectiveTime: 40, Value: "D"},
			{Key: "new-key", EffectiveTime: 1, Value: "X"},
		})
	if len(rej) != 1 || rej[0].Reason != ReasonTooManyChangePoints {
		t.Fatalf("期望唯一的超限原因，实际=%v", rej)
	}
	if got := s.History("new-key"); got != nil {
		t.Fatalf("超限拒绝不得影响批次中其他键，实际历史=%s", fmtIntervals(got))
	}
	expectIntervals(t, s.History(k),
		[]Interval{iv(k, 10, 20, "A"), iv(k, 20, 30, "B"), iv(k, 30, OpenEnd, "C")},
		"超限拒绝后 k 的变更点与历史不变")

	if err := s.CheckInvariants(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}
