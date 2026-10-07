package audit

import "testing"

// TestCorrectionOverridesOriginal 覆盖：订正记录占用新序号、指向
// 原记录、重放覆盖原记录影响；原记录原样保留；订正不能指向订正、
// 不能指向不存在序号（均为参数非法）。
func TestCorrectionOverridesOriginal(t *testing.T) {
	tl := &testLogger{}
	defer tl.flush(t)

	_, log, exec, replayer, _ := newSystem(2)
	const typ = "Person"
	mustRegister(t, exec, typ, "p1", "init")
	mustRegister(t, exec, typ, "p2", "init")

	r1, err := exec.Execute(Action{ActionID: "A1", TypeName: typ, Writes: []Write{{"p1", "bad"}, {"p2", "c2"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := exec.Execute(Action{ActionID: "A2", TypeName: typ, Writes: []Write{{"p2", "c3"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tl.logf("输入动作: %s | %s", r1, r2)

	_, err = exec.Correct(typ, "C-missing", 99, map[string]string{"p1": "good", "p2": "c2"})
	tl.logf("订正 seq=99 | 实际输出: %v | 依据: 参数非法（序号不存在）", err)
	if !isIllegal(err) {
		t.Fatalf("want illegal, got %v", err)
	}

	_, err = exec.Correct(typ, "C-wrongset", r1.Seq, map[string]string{"p1": "good"})
	tl.logf("订正 seq=%d 但实例集合不全 | 实际输出: %v | 依据: 参数非法", r1.Seq, err)
	if !isIllegal(err) {
		t.Fatalf("want illegal, got %v", err)
	}

	// p1 自原动作后未被再写：订正应覆盖其值；
	// p2 在原动作后又被 A2 写成 c3：订正不得倒装这次更晚的写入。
	corr, err := exec.Correct(typ, "C1", r1.Seq, map[string]string{"p1": "good", "p2": "c2-fixed"})
	if err != nil {
		t.Fatal(err)
	}
	tl.logf("订正成功: %s | 依据: 新序号 %d，原 seq=%d 保留", corr, corr.Seq, r1.Seq)
	if corr.Kind != KindCorrection || corr.Seq != 5 || corr.TargetSeq != r1.Seq {
		t.Fatalf("bad correction: %v", corr)
	}

	orig, ok := log.At(typ, r1.Seq)
	if !ok || orig.Kind != KindAction || orig.Changes[0].After != "bad" {
		t.Fatalf("original record altered: %+v ok=%v", orig, ok)
	}

	_, err = exec.Correct(typ, "C2", corr.Seq, map[string]string{"p1": "x"})
	tl.logf("订正指向订正 seq=%d | 实际输出: %v | 依据: 参数非法", corr.Seq, err)
	if !isIllegal(err) {
		t.Fatalf("want illegal (correction of correction), got %v", err)
	}

	s1, _ := replayer.StateAt(typ, r1.Seq)
	assertState(t, s1, map[string]string{"p1": "bad", "p2": "c2"}, "state@1 original kept")
	s2, _ := replayer.StateAt(typ, r2.Seq)
	assertState(t, s2, map[string]string{"p1": "bad", "p2": "c3"}, "state@2")
	s3, _ := replayer.StateAt(typ, corr.Seq)
	assertState(t, s3, map[string]string{"p1": "good", "p2": "c3"}, "correction replaces original, later write wins")
	tl.logf("重放: @r1=%v @r2=%v @corr=%v | 依据: p1 被订正覆盖；p2 保留更晚的 A2 写入 c3", s1, s2, s3)

	res, err := replayer.Replay(typ, r2.Seq, corr.Seq)
	if err != nil {
		t.Fatal(err)
	}
	assertState(t, res.StateFrom, map[string]string{"p1": "bad", "p2": "c3"}, "range from-state")
	assertState(t, res.StateTo, map[string]string{"p1": "good", "p2": "c3"}, "range to-state")
	if len(res.Events) != 1 || res.Events[0].Seq != corr.Seq {
		t.Fatalf("range events=%+v", res.Events)
	}

	// 同一区间多次重放必须幂等一致。
	for i := 0; i < 3; i++ {
		again, err := replayer.Replay(typ, r2.Seq, corr.Seq)
		if err != nil || !stateEqual(again.StateTo, res.StateTo) || len(again.Events) != 1 {
			t.Fatalf("replay not deterministic on iteration %d: %+v err=%v", i, again, err)
		}
	}

	if err := log.VerifyChain(typ); err != nil {
		t.Fatalf("chain: %v", err)
	}
}

// TestImmutableTamperDetected 覆盖：任何对既有记录的物理修改都会被
// 哈希链校验拒绝（系统本身不提供修改/删除接口）。
func TestImmutableTamperDetected(t *testing.T) {
	tl := &testLogger{}
	defer tl.flush(t)

	_, log, exec, _, _ := newSystem(4)
	const typ = "T"
	mustRegister(t, exec, typ, "x", "0")
	rec, err := exec.Execute(Action{ActionID: "A", TypeName: typ, Writes: []Write{{"x", "1"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tl.logf("写入 %s 后直接篡改底层切片并调用 VerifyChain", rec)

	if err := log.VerifyChain(typ); err != nil {
		t.Fatalf("clean chain failed: %v", err)
	}

	shard := log.shards[typ]
	shard.records[0].Changes[0].After = "HACKED"
	if err := log.VerifyChain(typ); err == nil {
		t.Fatalf("tampering was not detected")
	} else {
		tl.logf("实际输出: %v | 依据: 篡改被哈希链检测并拒绝", err)
	}
}
