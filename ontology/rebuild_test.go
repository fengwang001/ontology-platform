package ontology

import (
	"bytes"
	"testing"
)

func newLoggedPlatform() (*Platform, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return NewPlatform(NewJSONLogger(buf)), buf
}

// 边界归属：基准点恰好落在某次写入之后——该写入归基线，下一写入归增量，不重不漏。
func TestBoundaryBeforeAfterWrite(t *testing.T) {
	p, _ := newLoggedPlatform()
	p.DeclareIndex("T", "age")
	p.Put("T", "o1", "age", "10")
	before := p.CurrentSeq() // o1 的写入已生效

	h, err := p.StartRebuild("T", "age")
	if err != nil {
		t.Fatal(err)
	}
	// 基准点之后到达的写入必须归入增量
	w2 := p.Put("T", "o2", "age", "20")
	if w2.Seq <= before {
		t.Fatalf("增量写入序号 %d 应严格大于基准点 %d", w2.Seq, before)
	}

	rec, err := h.Complete()
	if err != nil {
		t.Fatal(err)
	}
	if rec.BaselineSeq != before || rec.CompleteSeq != w2.Seq {
		t.Fatalf("审计边界错误 B=%d C=%d want %d,%d", rec.BaselineSeq, rec.CompleteSeq, before, w2.Seq)
	}
	var sawBase, sawInc bool
	for _, e := range rec.Entries {
		if e.Object == "o1" && e.Baseline && e.SourceSeq <= rec.BaselineSeq {
			sawBase = true
		}
		if e.Object == "o2" && !e.Baseline && e.SourceSeq > rec.BaselineSeq {
			sawInc = true
		}
	}
	if !sawBase || !sawInc {
		t.Fatalf("基线/增量归属错误 base=%v inc=%v entries=%+v", sawBase, sawInc, rec.Entries)
	}
	if !rec.ValidDigest() {
		t.Fatal("审计指纹无效")
	}
	if rep := p.Verify("T", "age"); !rep.Consistent {
		t.Fatalf("复核应一致: %+v", rep.Mismatches)
	}
	r, qerr := p.Query("T", "age", "20")
	if qerr != nil || len(r.Objects) != 1 || r.Objects[0] != "o2" {
		t.Fatalf("增量应已按序应用: res=%v err=%v", r, qerr)
	}
}

// 同一对象在重建期间多次写入，增量必须按对象侧生效顺序（seq 升序）应用。
func TestIncrementAppliedInOrder(t *testing.T) {
	p, _ := newLoggedPlatform()
	p.DeclareIndex("T", "city")
	p.Put("T", "o1", "city", "BJ")
	h, _ := p.StartRebuild("T", "city")
	p.Put("T", "o1", "city", "SH")
	p.Put("T", "o1", "city", "SZ")
	p.DeleteAttr("T", "o1", "city")
	p.Put("T", "o1", "city", "GZ") // 最终值
	h.Complete()
	r, qerr := p.Query("T", "city", "GZ")
	if qerr != nil || len(r.Objects) != 1 {
		t.Fatalf("最终值应为 GZ: %v %v", r, qerr)
	}
	for _, v := range []Value{"BJ", "SH", "SZ"} {
		if rr, _ := p.Query("T", "city", v); len(rr.Objects) != 0 {
			t.Fatalf("旧值 %s 不应残留", v)
		}
	}
	if rep := p.Verify("T", "city"); !rep.Consistent {
		t.Fatalf("复核应一致: %+v", rep.Mismatches)
	}
	if d := p.DiffAgainstNaive("T", "city"); len(d) != 0 {
		t.Fatalf("与朴素模型不一致: %v", d)
	}
}

func TestRebuildFailureUnavailable(t *testing.T) {
	p, _ := newLoggedPlatform()
	p.DeclareIndex("T", "age")
	p.Put("T", "o1", "age", "1")

	// 未声明索引：查询=不存在（优先级1）
	if _, err := p.Query("X", "age", "1"); err.Kind != KindIndexNotDeclared {
		t.Fatalf("期望未声明，得到 %v", err)
	}

	h, _ := p.StartRebuild("T", "age")
	p.Put("T", "o2", "age", "2")
	// 重建中：查询=不可用（优先级2），与“不存在”明确区分
	if _, err := p.Query("T", "age", "1"); err.Kind != KindRebuildingUnavailable {
		t.Fatalf("重建中应不可用，得到 %v", err)
	}
	h.Fail()
	if _, err := p.Query("T", "age", "1"); err.Kind != KindRebuildingUnavailable {
		t.Fatalf("失败后整体应不可用，部分条目不得泄露，得到 %v", err)
	}

	// 重新发起一次成功重建后恢复
	h2, rerr := p.StartRebuild("T", "age")
	if rerr != nil {
		t.Fatal(rerr)
	}
	rec, _ := h2.Complete()
	if rec.CompleteSeq < 2 {
		t.Fatal("新重建必须包含失败窗口期间的全部写入")
	}
	if d := p.DiffAgainstNaive("T", "age"); len(d) != 0 {
		t.Fatalf("恢复后应与朴素模型一致: %v", d)
	}
}

func TestRejectedRequestNoStateChange(t *testing.T) {
	p, _ := newLoggedPlatform()
	p.DeclareIndex("T", "age")
	p.Put("T", "o1", "age", "1")
	seq := p.CurrentSeq()

	if _, err := p.StartRebuild("X", "age"); err.Kind != KindIndexNotDeclared {
		t.Fatalf("未声明拒绝类别错误: %v", err)
	}
	h, _ := p.StartRebuild("T", "age")
	if _, err := p.StartRebuild("T", "age"); err.Kind != KindRejectedRebuild {
		t.Fatalf("重复重建应被拒绝: %v", err)
	}
	// 拒绝不改变对象状态：全局序号与对象值保持不变
	if p.CurrentSeq() != seq {
		t.Fatal("被拒绝的重建不应分配序号/改变对象状态")
	}
	h.Complete()
	// 只读复核幂等：同一状态两次结论完全相同
	r1 := p.Verify("T", "age")
	r2 := p.Verify("T", "age")
	if !reportsEqual(r1, r2) {
		t.Fatalf("只读复核不幂等: %+v != %+v", r1.Mismatches, r2.Mismatches)
	}
	if d := p.DiffAgainstNaive("T", "age"); len(d) != 0 {
		t.Fatalf("拒绝的重建不得改变索引: %v", d)
	}
}

func TestDirtyNoticeOnQuery(t *testing.T) {
	p, _ := newLoggedPlatform()
	p.DeclareIndex("T", "age")
	p.Put("T", "o1", "age", "1")
	h, _ := p.StartRebuild("T", "age")
	h.Complete()
	p.InjectCorruption("T", "age", "o1", "9")
	rep := p.Verify("T", "age")
	if rep.Consistent {
		t.Fatal("注入错误后复核必须发现")
	}
	p.MarkFlaggedFromReport("T", "age", rep)
	res, err := p.Query("T", "age", "9")
	if err != nil || res.DirtyNotice == "" {
		t.Fatalf("索引可用但须声明未修复不一致: res=%v err=%v", res, err)
	}
}
