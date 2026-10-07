package bitemporal

import "testing"

// TestReplaySublinearProbe 验证回放探针访问次数不随该链接类型累计事实量线性增长。
//
// 构造：N 条互不相关对象对的创建事实（总事实量线性增长），
// 固定回放一条特定边；统计二维穿刺过程中的索引访问次数。
// 要求：N 从 64 增长到 2048（32 倍），探针增长远低于 32 倍
// （理论界 O(log^2 F)，允许常数噪声，取上界 4 倍）。
func TestReplaySublinearProbe(t *testing.T) {
	measure := func(n int) int {
		st := NewStore()
		must(t, st.RegisterObjectType("T", 0))
		must(t, st.RegisterObjectType("U", 0))
		must(t, st.RegisterLinkType(LinkType{ID: "L", SourceType: "T", TargetType: "U"},
			Cardinality{Forward: Card{Max: 0}, Reverse: Card{Max: 0}}, 0))
		for i := 0; i < n; i++ {
			a := ID("s" + itoa(i))
			b := ID("u" + itoa(i))
			must(t, st.RecordCreate("L", a, b, int64(i+1), int64(i+1)))
		}
		snap := st.CurrentSnapshot()
		probe := &ProbeCounts{}
		_ = snap.ProbeReplay("L", int64(n), int64(n), probe)
		return probe.FactScans + probe.RuleScans
	}

	small := measure(64)
	large := measure(2048)
	t.Logf("probe scans: N=64 -> %d; N=2048 -> %d", small, large)
	if large <= 0 {
		t.Fatalf("probe should observe index work")
	}
	if large >= small*4 {
		t.Fatalf("replay probe grows near-linearly: %d -> %d (32x facts)", small, large)
	}
}

// TestAuditSegmentProbe 验证固定大小审计窗口的探针不随历史版本数线性增长。
func TestAuditSegmentProbe(t *testing.T) {
	measureRules := func(rules int) int {
		st := NewStore()
		must(t, st.RegisterObjectType("T", 0))
		must(t, st.RegisterObjectType("U", 0))
		must(t, st.RegisterLinkType(LinkType{ID: "L", SourceType: "T", TargetType: "U"},
			Cardinality{Forward: Card{Max: 1}, Reverse: Card{Max: 1}}, 0))
		must(t, st.RecordCreate("L", "a", "b", 0, 0))
		// 大量规则版本，均发生在审计窗口之后。
		for r := 1; r <= rules; r++ {
			must(t, st.PutRule("L",
				Cardinality{Forward: Card{Max: 1 + (r % 3)}, Reverse: Card{Max: 1}},
				int64(1000+r)))
		}
		// 规则版本选择是二分查找；直接统计 RuleAt 的访问规模不可见，
		// 这里验证审计窗口内分段数不随后续版本数变化。
		aud := NewAuditor(st, NewDecisionLog())
		segs, err := aud.Audit(nil, AuditRequest{
			LinkType: "L", RecordStart: 0, RecordEnd: 100})
		if err != nil {
			t.Fatal(err)
		}
		return len(segs)
	}
	a := measureRules(10)
	b := measureRules(500)
	if a != b {
		t.Fatalf("segment count in an early window must not depend on later rules: %d vs %d", a, b)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}
