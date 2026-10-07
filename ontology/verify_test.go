package ontology

import "testing"

func reportsEqual(a, b *VerificationReport) bool {
	if a.Consistent != b.Consistent || len(a.Mismatches) != len(b.Mismatches) ||
		a.CellReads != b.CellReads || a.HistoryReads != b.HistoryReads {
		return false
	}
	for i := range a.Mismatches {
		if a.Mismatches[i] != b.Mismatches[i] {
			return false
		}
	}
	return true
}

// 复核独立于重建执行日志：人为篡改索引值后，仅凭审计+对象当前状态即定位到具体条目。
func TestIndependentVerifyFindsInjectedMismatch(t *testing.T) {
	p, _ := newLoggedPlatform()
	p.DeclareIndex("T", "age")
	p.Put("T", "o1", "age", "1")
	p.Put("T", "o2", "age", "2")
	h, _ := p.StartRebuild("T", "age")
	p.Put("T", "o3", "age", "3")
	h.Complete()

	p.InjectCorruption("T", "age", "o2", "777")
	rep := p.Verify("T", "age")
	if rep.Consistent {
		t.Fatal("必须发现注入的条目级不一致")
	}
	found := false
	for _, m := range rep.Mismatches {
		if m.Object == "o2" && m.Value == "777" && m.Kind == "wrong_value" {
			found = true
		}
	}
	if !found {
		t.Fatalf("必须精确报告哪个条目与哪个对象不符，实际: %+v", rep.Mismatches)
	}
}

// 审计指纹篡改可被独立发现。
func TestAuditDigestTampering(t *testing.T) {
	p, _ := newLoggedPlatform()
	p.DeclareIndex("T", "age")
	p.Put("T", "o1", "age", "1")
	h, _ := p.StartRebuild("T", "age")
	rec, _ := h.Complete()

	_ = rec
	p.TamperAuditDigest("T", "age") // 破坏指纹
	rep := p.Verify("T", "age")
	ok := false
	for _, m := range rep.Mismatches {
		if m.Kind == "digest_bad" {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("审计指纹被篡改必须被发现: %+v", rep.Mismatches)
	}
}

// 规模无关性：对象历史写入从 N 增长到 100N，复核读取的历史记录数恒为 0，
// 每条目检查的当前属性单元数不随历史次数增长。
func TestVerifyScaleIndependent(t *testing.T) {
	p, _ := newLoggedPlatform()
	p.DeclareIndex("T", "age")
	build := func(historyPerObj int) *VerificationReport {
		p2, _ := newLoggedPlatform()
		p2.DeclareIndex("T", "age")
		for i := 0; i < historyPerObj; i++ {
			p2.Put("T", "o1", "age", Value("v"+itoa(i)))
		}
		p2.Put("T", "o2", "age", "final")
		h, _ := p2.StartRebuild("T", "age")
		h.Complete()
		return p2.Verify("T", "age")
	}
	r1 := build(10)
	r2 := build(1000)
	if r1.HistoryReads != 0 || r2.HistoryReads != 0 {
		t.Fatalf("复核不得读取历史写入: %d %d", r1.HistoryReads, r2.HistoryReads)
	}
	// 每条目只看当前单元：2 个对象无论历史 10 次还是 1000 次，单元读取相同。
	if r1.CellReads != r2.CellReads {
		t.Fatalf("单元读取不应随历史增长: %d vs %d", r1.CellReads, r2.CellReads)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
