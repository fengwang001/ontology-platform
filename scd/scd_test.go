package scd

import (
	"fmt"
	"strings"
	"testing"
)

// stepLogger 在测试日志中打印每一步输入、历史区间与判定依据。
type stepLogger struct {
	t    *testing.T
	step int
}

func newStepLogger(t *testing.T) *stepLogger {
	t.Helper()
	l := &stepLogger{t: t}
	l.banner("初始状态：无任何变更点与历史区间")
	return l
}

func (l *stepLogger) banner(msg string) {
	l.t.Helper()
	l.t.Logf("── 步骤 %d ── %s", l.step, msg)
}

// commit 打印提交输入与结果。
func (l *stepLogger) commit(h *History, events []Event, why string) error {
	l.t.Helper()
	l.step++
	l.banner(why)
	for i, ev := range events {
		l.t.Logf("  输入 event#%d: key=%q at=%d op=%s value=%q",
			i, ev.Key, ev.At, opName(ev.Op), ev.Value)
	}
	err := h.Commit(events)
	if err != nil {
		l.t.Logf("  判定: 整批拒绝 → %v", err)
		return err
	}
	l.t.Logf("  判定: 提交成功（事件合法且未超变更点上限）")
	return nil
}

// dump 打印每个键的变更点、历史区间与点查询抽样。
func (l *stepLogger) dump(h *History, probeKeys []string, probes []int64) {
	l.t.Helper()
	for _, key := range probeKeys {
		h.keysMu.RLock()
		st, ok := h.keys[key]
		h.keysMu.RUnlock()
		if !ok {
			l.t.Logf("  历史 key=%q: <不存在>", key)
			continue
		}
		st.mu.RLock()
		pts := make([]ChangePoint, 0, len(st.order))
		for _, at := range st.order {
			pts = append(pts, st.points[at])
		}
		ivs := cloneIntervals(st.intervals)
		st.mu.RUnlock()

		var pb strings.Builder
		for i, p := range pts {
			if i > 0 {
				pb.WriteString(", ")
			}
			fmt.Fprintf(&pb, "@%d %s/%q#%d", p.At, opName(p.Op), p.Value, p.Seq)
		}
		l.t.Logf("  变更点 key=%q (n=%d): %s", key, len(pts), pb.String())
		if len(ivs) == 0 {
			l.t.Logf("  历史区间 key=%q: <无区间>", key)
		}
		for _, iv := range ivs {
			l.t.Logf("  历史区间 key=%q: [%d, %d) = %q", key, iv.Start, iv.End, iv.Value)
		}
		for _, at := range probes {
			v, ok := h.ValueAt(key, at)
			if ok {
				l.t.Logf("  点查询 key=%q at=%d → %q（依据：at 落在包含它的左闭右开区间内）", key, at, v)
			} else {
				l.t.Logf("  点查询 key=%q at=%d → 无值（依据：无任何 [start,end) 区间包含该点）", key, at)
			}
		}
	}
	if err := h.SelfCheck(); err != nil {
		l.t.Fatalf("  自检失败: %v", err)
	}
	l.t.Logf("  自检: 通过（区间升序、不重叠、任意点至多命中一行、且与批量重算一致）")
}

func opName(op Op) string {
	switch op {
	case OpUpdate:
		return "UPDATE"
	case OpDelete:
		return "DELETE"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", op)
	}
}

func iv(key string, start, end int64, value string) Interval {
	return Interval{Key: key, Start: start, End: end, Value: value}
}

// assertIntervals 校验增量历史与纯批量重算结果，同时核对期望区间。
func assertIntervals(t *testing.T, h *History, key string, want []Interval) {
	t.Helper()
	got := h.Intervals(key)
	if !intervalsEqual(got, want) {
		t.Fatalf("key=%q 增量区间 = %v, 期望 %v", key, got, want)
	}
}
