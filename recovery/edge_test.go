package recovery

import "testing"

// TestBlockedActionObjectIsUnknownNotOutOfCoverage：对象仅被一条完整但
// 被整体放弃的动作涉及时，来源分类为 unknown，且不属于超范围错误。
func TestBlockedActionObjectIsUnknownNotOutOfCoverage(t *testing.T) {
	base := Version{Epoch: 5, Seq: 0}
	snap := Snapshot{BaseVersion: base, Objects: []ObjectSnapshot{
		{ID: "a", Exists: true, State: "A0"},
	}}
	actions := []Action{
		{Seq: 1, Version: base.Next(), Effects: map[ObjectID]ObjectEffect{
			"a": {Change: "A1"},
			"z": {Change: "Z1"}, // z 起点未知且无锚点 => 整体放弃
		}},
		{Seq: 2, Version: Version{Epoch: 5, Seq: 2}, Effects: map[ObjectID]ObjectEffect{
			"z": {Start: statePtr("Z0"), Change: "Z2"}, // 此后 z 自锚点重建
		}},
	}
	c, err := NewCoordinator(
		mustEncodeSnapshot(t, snap),
		mustEncodeLog(t, base, actions), NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := c.Repair([]ObjectID{"a", "z"})
	if err != nil {
		t.Fatalf("z is covered by a complete record, must not be out-of-coverage: %v", err)
	}
	if rep.Judgments[0].Outcome != ActionBlocked || rep.Judgments[1].Outcome != ActionApplied {
		t.Fatalf("judgments = %+v", rep.Judgments)
	}
	if rep.Objects["a"].State != "A0" {
		t.Fatalf("a must be unchanged by blocked action, got %q", rep.Objects["a"].State)
	}
	z := rep.Objects["z"]
	if z.Source != SourceRebuilt || z.State != "Z2" || z.AnchorSeq != 2 {
		t.Fatalf("z rebuilt from seq-2 anchor, got %+v", z)
	}

	// 仅构造出“仍不可读”的形态：删掉重建动作后 z 应为 unknown。
	c2, _ := NewCoordinator(
		mustEncodeSnapshot(t, snap),
		mustEncodeLog(t, base, actions[:1]), NopLogger{})
	rep2, err := c2.Repair([]ObjectID{"z"})
	if err != nil {
		t.Fatalf("z still covered: %v", err)
	}
	if rep2.Objects["z"].Source != SourceUnknown {
		t.Fatalf("z must remain unknown, got %+v", rep2.Objects["z"])
	}
	if _, err := c2.Lookup("absent"); AsError(err).Kind != KindOutOfCoverage {
		t.Fatalf("absent object must be out-of-coverage, got %v", err)
	}
}

// TestDecodeActionStandalone 单帧编解码与损坏判定。
func TestDecodeActionStandalone(t *testing.T) {
	a := Action{Seq: 7, Version: Version{Epoch: 1, Seq: 8},
		Effects: map[ObjectID]ObjectEffect{"x": {Change: "X"}}}
	rec, err := EncodeAction(a)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeAction(rec, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != 7 || got.Effects["x"].Change != "X" || got.Effects["x"].Start != nil {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	broken := flipByte(t, rec, 5)
	_, err = DecodeAction(broken, 3)
	if e := AsError(err); e == nil || e.Kind != KindActionRecordCorrupt || e.RecordIndex != 3 {
		t.Fatalf("want corrupt/index=3, got %v", err)
	}
}
