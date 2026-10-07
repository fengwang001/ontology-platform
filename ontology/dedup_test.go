package ontology

import (
	"fmt"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// TestDedupBoundaryExhaustive 穷举两条事件在两个标识维度上的全部
// 组合，验证“重复投递”与“独立等价调用”的判定边界。
func TestDedupBoundaryExhaustive(t *testing.T) {
	const (
		eventA = "evt-1"
		eventB = "evt-2"
		actA   = "act-1"
		actB   = "act-2"
	)
	payload := map[string]any{"amount": 42}

	cases := []struct {
		name string
		evt  ChangeEvent
		want DedupVerdict
	}{
		{"完全重复投递（同 EventID 同 ActionExecutionID）",
			ChangeEvent{EventID: eventA, ActionExecutionID: actA, Payload: payload}, VerdictDuplicate},
		{"同 ActionExecutionID 不同 EventID（标识矛盾）",
			ChangeEvent{EventID: eventB, ActionExecutionID: actA, Payload: payload}, VerdictUndecidable},
		{"同 EventID 不同 ActionExecutionID（标识矛盾）",
			ChangeEvent{EventID: eventA, ActionExecutionID: actB, Payload: payload}, VerdictUndecidable},
		{"独立等价调用（参数效果等价但标识全新）",
			ChangeEvent{EventID: eventB, ActionExecutionID: actB, Payload: payload}, VerdictNew},
		{"缺 EventID",
			ChangeEvent{EventID: "", ActionExecutionID: "act-9"}, VerdictUndecidable},
		{"缺 ActionExecutionID",
			ChangeEvent{EventID: "evt-9", ActionExecutionID: ""}, VerdictUndecidable},
		{"双标识皆缺",
			ChangeEvent{EventID: "", ActionExecutionID: ""}, VerdictUndecidable},
	}

	d := NewDeduper(fixedClock())
	first := ChangeEvent{EventID: eventA, ActionExecutionID: actA, Payload: payload}
	if v, _ := d.Classify(first); v != VerdictNew {
		t.Fatalf("首次事件应判定为 New，得到 %v", v)
	}
	for _, tc := range cases {
		if got, _ := d.Classify(tc.evt); got != tc.want {
			t.Errorf("%s: 期望 %v，得到 %v", tc.name, tc.want, got)
		}
	}
}

// TestDedupDecisionLog 验证每次判定的输入、所依据的事件标识与结论
// 都被记录，可供事后核查。
func TestDedupDecisionLog(t *testing.T) {
	d := NewDeduper(fixedClock())
	evt := ChangeEvent{EventID: "evt-1", ActionExecutionID: "act-1"}
	dup := ChangeEvent{EventID: "evt-1", ActionExecutionID: "act-1"}
	conflict := ChangeEvent{EventID: "evt-2", ActionExecutionID: "act-1"}

	d.Classify(evt)
	d.Classify(dup)
	d.Classify(conflict)

	log := d.Decisions()
	if len(log) != 3 {
		t.Fatalf("决策日志应有 3 条，得到 %d", len(log))
	}
	for i, rec := range log {
		if rec.Seq != int64(i+1) {
			t.Errorf("第 %d 条日志序号应为 %d，得到 %d", i, i+1, rec.Seq)
		}
		if rec.Time.IsZero() {
			t.Errorf("第 %d 条日志缺少时间戳", i)
		}
		if rec.Reason == "" {
			t.Errorf("第 %d 条日志缺少判定理由", i)
		}
	}
	dupRec := log[1]
	if dupRec.Verdict != VerdictDuplicate || dupRec.ComparedAgainst != "evt-1" {
		t.Errorf("重复判定日志应记录对照标识 evt-1，得到 %+v", dupRec)
	}
	conflictRec := log[2]
	if conflictRec.Verdict != VerdictUndecidable || conflictRec.ComparedAgainst != "evt-1" {
		t.Errorf("矛盾判定日志应记录对照标识 evt-1，得到 %+v", conflictRec)
	}
}

// TestDedupConstantCost 独立验证去重判定开销不随历史总量线性增长：
// 处理 n 条事件后哈希探测总数不超过 2n（每次判定 O(1)）。
func TestDedupConstantCost(t *testing.T) {
	d := NewDeduper(nil)
	const n = 20000
	for i := 0; i < n; i++ {
		evt := ChangeEvent{
			EventID:           fmt.Sprintf("evt-%d", i),
			ActionExecutionID: fmt.Sprintf("act-%d", i),
		}
		if v, _ := d.Classify(evt); v != VerdictNew {
			t.Fatalf("第 %d 条事件应判定为 New", i)
		}
	}
	// 再投递 n 条完全重复事件。
	for i := 0; i < n; i++ {
		evt := ChangeEvent{
			EventID:           fmt.Sprintf("evt-%d", i),
			ActionExecutionID: fmt.Sprintf("act-%d", i),
		}
		if v, _ := d.Classify(evt); v != VerdictDuplicate {
			t.Fatalf("第 %d 条重复事件应判定为 Duplicate", i)
		}
	}
	probes := d.Probes()
	if probes > 2*2*n {
		t.Fatalf("探测次数 %d 超过 2×事件数 %d，判定开销非常数", probes, 2*n)
	}
	// 更强的断言：重复判定每条恰好 1 次探测。
	if probes != n*2+n {
		t.Fatalf("探测次数应为 %d（新事件 2 次 + 重复 1 次），得到 %d", n*2+n, probes)
	}
}
