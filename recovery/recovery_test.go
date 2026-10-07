package recovery

import (
	"bytes"
	"testing"
)

func statePtr(s State) *State { return &s }

func mustEncodeSnapshot(t *testing.T, s Snapshot) []byte {
	t.Helper()
	b, err := EncodeSnapshot(s)
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	return b
}

func mustEncodeLog(t *testing.T, start Version, actions []Action) []byte {
	t.Helper()
	b, err := EncodeLog(start, actions)
	if err != nil {
		t.Fatalf("encode log: %v", err)
	}
	return b
}

func flipByte(t *testing.T, b []byte, offset int) []byte {
	t.Helper()
	out := append([]byte(nil), b...)
	if offset < 0 || offset >= len(out) {
		t.Fatalf("flip offset %d out of range [0,%d)", offset, len(out))
	}
	out[offset] ^= 0xFF
	return out
}

func findSubbyte(t *testing.T, haystack []byte, needle string, occurrence int) int {
	t.Helper()
	idx := -1
	for k := 0; k <= occurrence; k++ {
		j := bytes.Index(haystack[idx+1:], []byte(needle))
		if j < 0 {
			t.Fatalf("occurrence %d of %q not found", occurrence, needle)
		}
		idx = idx + 1 + j
	}
	return idx
}

// TestSnapshotCorruptionIsObjectLevel：快照记录损坏必须按对象报告，
// 不可读与对象不存在可区分。
func TestSnapshotCorruptionIsObjectLevel(t *testing.T) {
	base := Version{Epoch: 1, Seq: 10}
	snap := Snapshot{BaseVersion: base, Objects: []ObjectSnapshot{
		{ID: "a", Exists: true, State: "A0"},
		{ID: "b", Exists: true, State: "B0"},
		{ID: "c", Exists: false, State: ""},
	}}
	raw := mustEncodeSnapshot(t, snap)

	bHeader := findSubbyte(t, raw, `{"id":"b"}`, 0)
	broken := flipByte(t, raw, bHeader+len(`{"id":"b"}`)+8)

	decoded, corrupt, err := DecodeSnapshot(broken)
	if err != nil {
		t.Fatalf("object-level corruption must not be a hard error: %v", err)
	}
	if len(corrupt) != 1 || corrupt["b"] == "" {
		t.Fatalf("want exactly object b reported corrupt, got %v", corrupt)
	}
	if len(decoded.Objects) != 2 {
		t.Fatalf("want 2 intact records, got %d", len(decoded.Objects))
	}

	c, err := NewCoordinator(broken, mustEncodeLog(t, base, nil), NopLogger{})
	if err != nil {
		t.Fatalf("new coordinator: %v", err)
	}
	rep, err := c.RepairAll()
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !rep.Objects["b"].SnapshotUnreadable || rep.Objects["b"].Source != SourceUnknown {
		t.Fatalf("b must be snapshot-unreadable/unknown, got %+v", rep.Objects["b"])
	}
	if rep.Objects["a"].Source != SourceSnapshot || rep.Objects["a"].State != "A0" {
		t.Fatalf("a must keep intact snapshot state, got %+v", rep.Objects["a"])
	}
	if rep.Objects["c"].SnapshotUnreadable || rep.Objects["c"].Exists ||
		rep.Objects["c"].Source != SourceSnapshot {
		t.Fatalf("c must be readable-tombstone, got %+v", rep.Objects["c"])
	}
	if len(rep.SnapshotCorruptions) != 1 {
		t.Fatalf("object-level corruption report expected, got %v", rep.SnapshotCorruptions)
	}
}

// TestActionCorruptionIsAllOrNothing：动作记录损坏时整条不可采信，
// 覆盖“损坏恰好落在涉及对象集合边界上”的情形。
func TestActionCorruptionIsAllOrNothing(t *testing.T) {
	base := Version{Epoch: 1, Seq: 0}
	snap := Snapshot{BaseVersion: base, Objects: []ObjectSnapshot{
		{ID: "a", Exists: true, State: "A0"},
	}}
	a1 := Action{Seq: 1, Version: base.Next(), Effects: map[ObjectID]ObjectEffect{
		"a": {Change: "A1"},
		"b": {Start: statePtr("B0"), Change: "B1"},
	}}
	log := mustEncodeLog(t, base, []Action{a1})

	term := bytes.LastIndex(log, []byte{'E'})
	crcBroken := flipByte(t, log, term-1)
	c, err := NewCoordinator(mustEncodeSnapshot(t, snap), crcBroken, NopLogger{})
	if err != nil {
		t.Fatalf("corrupt action record must not fail construction: %v", err)
	}
	rep, _ := c.RepairAll()
	if len(rep.Judgments) != 1 || rep.Judgments[0].Outcome != ActionCorrupt {
		t.Fatalf("want single corrupt judgment, got %+v", rep.Judgments)
	}
	if ra := rep.Objects["a"]; ra.State != "A0" || ra.Source != SourceSnapshot {
		t.Fatalf("a must keep snapshot state, got %+v", ra)
	}
	if _, err := c.Lookup("b"); AsError(err) == nil || AsError(err).Kind != KindOutOfCoverage {
		t.Fatalf("b must be out of coverage, got err=%v", err)
	}

	bKey := findSubbyte(t, log, `"b"`, 0)
	payloadBroken := flipByte(t, log, bKey)
	c2, err := NewCoordinator(mustEncodeSnapshot(t, snap), payloadBroken, NopLogger{})
	if err != nil {
		t.Fatalf("construction: %v", err)
	}
	rep2, _ := c2.RepairAll()
	if rep2.Judgments[0].Outcome != ActionCorrupt {
		t.Fatalf("boundary corruption must corrupt the whole record, got %+v", rep2.Judgments[0])
	}
}

// TestAtomicityMixedReadableUnreadable：动作涉及对象部分可读、部分起点未知时
// 整体放弃；之后出现提供锚点的动作则自该动作起重建，不得向更早回溯。
func TestAtomicityMixedReadableUnreadable(t *testing.T) {
	base := Version{Epoch: 2, Seq: 0}
	snap := Snapshot{BaseVersion: base, Objects: []ObjectSnapshot{
		{ID: "a", Exists: true, State: "A0"},
		{ID: "b", Exists: true, State: "B0"},
	}}
	rawSnap := mustEncodeSnapshot(t, snap)
	bHeader := findSubbyte(t, rawSnap, `{"id":"b"}`, 0)
	brokenSnap := flipByte(t, rawSnap, bHeader+len(`{"id":"b"}`)+8)

	a1 := Action{Seq: 1, Version: base.Next(), Effects: map[ObjectID]ObjectEffect{
		"a": {Change: "A1"},
		"b": {Change: "B1"},
	}}
	a2 := Action{Seq: 2, Version: Version{Epoch: 2, Seq: 2}, Effects: map[ObjectID]ObjectEffect{
		"b": {Start: statePtr("B-anchor"), Change: "B2"},
	}}
	a3 := Action{Seq: 3, Version: Version{Epoch: 2, Seq: 3}, Effects: map[ObjectID]ObjectEffect{
		"a": {Change: "A3"},
		"b": {Change: "B3"},
	}}
	log := mustEncodeLog(t, base, []Action{a1, a2, a3})

	c, err := NewCoordinator(brokenSnap, log, NopLogger{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	rep, err := c.RepairAll()
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	want := map[ObjectID]ObjectReport{
		"a": {Object: "a", Exists: true, State: "A3",
			Source: SourceSnapshot, AnchorSeq: -1},
		"b": {Object: "b", Exists: true, State: "B3",
			Source: SourceRebuilt, AnchorSeq: 2, SnapshotUnreadable: true},
	}
	for id, w := range want {
		got := rep.Objects[id]
		if got.Exists != w.Exists || got.State != w.State || got.Source != w.Source ||
			got.AnchorSeq != w.AnchorSeq || got.SnapshotUnreadable != w.SnapshotUnreadable {
			t.Fatalf("%s mismatch:\n got %+v\nwant %+v", id, got, w)
		}
	}
	outcomes := []ActionOutcome{
		rep.Judgments[0].Outcome, rep.Judgments[1].Outcome, rep.Judgments[2].Outcome,
	}
	wantOutcomes := []ActionOutcome{ActionBlocked, ActionApplied, ActionApplied}
	for i := range outcomes {
		if outcomes[i] != wantOutcomes[i] {
			t.Fatalf("judgments %v want %v", outcomes, wantOutcomes)
		}
	}
	if j0 := rep.Judgments[0]; len(j0.BlockingObjects) != 1 || j0.BlockingObjects[0] != "b" {
		t.Fatalf("blocked judgment must name b, got %+v", j0)
	}
}

// TestCorruptRecordBetweenActionsPropagation：连续动作中间夹损坏记录。
func TestCorruptRecordBetweenActionsPropagation(t *testing.T) {
	base := Version{Epoch: 3, Seq: 0}
	snap := Snapshot{BaseVersion: base, Objects: []ObjectSnapshot{
		{ID: "a", Exists: true, State: "A0"},
	}}
	actions := []Action{
		{Seq: 1, Version: base.Next(),
			Effects: map[ObjectID]ObjectEffect{"a": {Change: "A1"}}},
		{Seq: 2, Version: Version{Epoch: 3, Seq: 2},
			Effects: map[ObjectID]ObjectEffect{
				"a": {Change: "A2-bad"},
				"x": {Start: statePtr("X0"), Change: "X2"},
			}},
		{Seq: 3, Version: Version{Epoch: 3, Seq: 3},
			Effects: map[ObjectID]ObjectEffect{"a": {Change: "A3"}}},
	}
	log := mustEncodeLog(t, base, actions)

	second := findSubbyte(t, log, `"seq":2`, 0)
	broken := flipByte(t, log, second)

	c, err := NewCoordinator(mustEncodeSnapshot(t, snap), broken, NopLogger{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	rep, _ := c.RepairAll()
	if rep.Objects["a"].State != "A3" {
		t.Fatalf("a should advance over corrupt record to A3, got %q", rep.Objects["a"].State)
	}
	if _, covered := rep.Objects["x"]; covered {
		t.Fatalf("x from corrupt record must not enter coverage, got %+v", rep.Objects["x"])
	}
	if j := rep.Judgments; j[0].Outcome != ActionApplied ||
		j[1].Outcome != ActionCorrupt || j[2].Outcome != ActionApplied {
		t.Fatalf("judgments = %+v", j)
	}
}

// TestRejectionPriorityAndErrorKinds：超范围 > 版本不衔接；错误类别可区分。
func TestRejectionPriorityAndErrorKinds(t *testing.T) {
	base := Version{Epoch: 1, Seq: 5}
	snap := Snapshot{BaseVersion: base, Objects: []ObjectSnapshot{
		{ID: "a", Exists: true, State: "A0"},
	}}

	// 版本不衔接，但请求同时包含超出覆盖范围的对象：必须先报超范围。
	mismatched, err := NewCoordinator(
		mustEncodeSnapshot(t, snap),
		mustEncodeLog(t, Version{Epoch: 9, Seq: 99}, nil),
		NopLogger{})
	if err != nil {
		t.Fatalf("mismatch is reported on request, not construction: %v", err)
	}
	if mismatched.VersionConnected() {
		t.Fatal("VersionConnected should be false")
	}
	if _, err := mismatched.Repair([]ObjectID{"a", "ghost"}); AsError(err).Kind != KindOutOfCoverage {
		t.Fatalf("out-of-coverage must win over version mismatch, got %v", err)
	}
	if _, err := mismatched.Lookup("ghost"); AsError(err).Kind != KindOutOfCoverage {
		t.Fatalf("lookup: out-of-coverage must win, got %v", err)
	}
	// 全部对象均在覆盖范围内时，才报版本不衔接。
	if _, err := mismatched.Repair([]ObjectID{"a"}); AsError(err).Kind != KindVersionMismatch {
		t.Fatalf("want version mismatch for covered request, got %v", err)
	}
	if _, err := mismatched.Lookup("a"); AsError(err).Kind != KindVersionMismatch {
		t.Fatalf("lookup covered object: want version mismatch, got %v", err)
	}

	c, err := NewCoordinator(mustEncodeSnapshot(t, snap), mustEncodeLog(t, base, nil), NopLogger{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err = c.Repair([]ObjectID{"a", "ghost"}); AsError(err).Kind != KindOutOfCoverage ||
		AsError(err).Object != "ghost" {
		t.Fatalf("want out-of-coverage on ghost, got %v", err)
	}
	if _, err = c.Lookup("ghost"); AsError(err).Kind != KindOutOfCoverage {
		t.Fatalf("lookup kind: %v", err)
	}

	rep, err := c.Repair([]ObjectID{"a"})
	if err != nil || rep.Objects["a"].Source != SourceSnapshot {
		t.Fatalf("valid request must not be an error, got %v %+v", err, rep)
	}
}

// TestRejectedRequestChangesNothing：被拒绝的修复请求不改变任何判定。
func TestRejectedRequestChangesNothing(t *testing.T) {
	base := Version{Epoch: 4, Seq: 0}
	snap := Snapshot{BaseVersion: base, Objects: []ObjectSnapshot{
		{ID: "a", Exists: true, State: "A0"},
	}}
	c, err := NewCoordinator(mustEncodeSnapshot(t, snap), mustEncodeLog(t, base, nil), NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := c.RepairAll()
	for i := 0; i < 5; i++ {
		if _, err := c.Repair([]ObjectID{ObjectID("nope-" + string(rune('a'+i)))}); err == nil {
			t.Fatal("expected rejection")
		}
	}
	after, _ := c.RepairAll()
	if len(after.Judgments) != len(before.Judgments) {
		t.Fatalf("judgments changed after rejections: %d -> %d",
			len(before.Judgments), len(after.Judgments))
	}
}
