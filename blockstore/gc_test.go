package blockstore

import (
	"errors"
	"testing"
)

// 第一轮时已开始、第二轮前才提交的会话引用待删块：块必须恢复且不被删除。
func TestRound1OpenSessionCommitsPendingBeforeRound2(t *testing.T) {
	st := New(0)
	st.BeginSession("long")
	must(t, st.Upload("long", dstr(1), []byte("data-1")))

	st.BeginSession("anchor")
	must(t, st.Upload("anchor", dstr(2), []byte("data-2")))
	must(t, st.Commit("anchor", "anchor-m", []Digest{dstr(2)}))
	must(t, st.EndSession("anchor"))

	r1, err := st.GCRound1()
	must(t, err)
	if len(r1.Pending) != 1 || r1.Pending[0] != dstr(1) {
		t.Fatalf("round1 pending=%v, want [blk-001]", r1.Pending)
	}
	if st.StateOf(dstr(1)) != StatePending {
		t.Fatalf("want pending, got %s", st.StateOf(dstr(1)))
	}
	if data, err := st.Read(dstr(1)); err != nil || string(data) != "data-1" {
		t.Fatalf("pending block not readable: data=%q err=%v", data, err)
	}
	if !contains(r1.Witness, "long") {
		t.Fatalf("witness=%v missing long", r1.Witness)
	}

	// long 会话在第二轮前才提交引用待删块的清单。
	must(t, st.Commit("long", "late-m", []Digest{dstr(1)}))
	must(t, st.EndSession("long"))

	r2, err := st.GCRound2()
	must(t, err)
	if r2.Skipped || len(r2.Deleted) != 0 {
		t.Fatalf("late-committed block must survive: %+v", r2)
	}
	if st.StateOf(dstr(1)) != StateNormal {
		t.Fatalf("want restored normal, got %s", st.StateOf(dstr(1)))
	}
	m, err := st.ReadManifest("late-m")
	must(t, err)
	if len(m.Blocks) != 1 || m.Blocks[0] != dstr(1) {
		t.Fatalf("manifest read back mismatch: %+v", m)
	}
}

// 会话复用待删块：Upload 命中待删块时立即恢复为正常。
func TestUploadReusesPendingBlockRestoresIt(t *testing.T) {
	st := New(0)
	st.BeginSession("s1")
	must(t, st.Upload("s1", dstr(1), []byte("orphan")))
	r1, err := st.GCRound1()
	must(t, err)
	if len(r1.Pending) != 1 || !contains(r1.Witness, "s1") {
		t.Fatalf("pending=%v witness=%v", r1.Pending, r1.Witness)
	}

	st.BeginSession("s2")
	must(t, st.Upload("s2", dstr(1), []byte("orphan")))
	if st.StateOf(dstr(1)) != StateNormal {
		t.Fatalf("reused pending block not restored: %s", st.StateOf(dstr(1)))
	}
	must(t, st.Commit("s2", "m", []Digest{dstr(1)}))
	must(t, st.EndSession("s2"))
	must(t, st.EndSession("s1"))

	r2, err := st.GCRound2()
	must(t, err)
	if len(r2.Deleted) != 0 {
		t.Fatalf("reused block deleted: %v", r2.Deleted)
	}
}

// 长会话阻止第二轮：未结束期间什么都不删；结束后无引用块才删除。
func TestLongSessionBlocksRound2ThenDeletes(t *testing.T) {
	st := New(0)
	st.BeginSession("long")
	st.BeginSession("short")
	must(t, st.Upload("short", dstr(1), []byte("orphan")))

	r1, err := st.GCRound1()
	must(t, err)
	if !contains(r1.Witness, "long") || !contains(r1.Witness, "short") {
		t.Fatalf("witness=%v", r1.Witness)
	}

	must(t, st.EndSession("short"))
	r2, err := st.GCRound2()
	must(t, err)
	if !r2.Skipped || len(r2.Deleted) != 0 {
		t.Fatalf("long session must block round2: %+v", r2)
	}
	if !contains(r2.WitnessLeft, "long") {
		t.Fatalf("witness-left=%v", r2.WitnessLeft)
	}
	if st.StateOf(dstr(1)) != StatePending {
		t.Fatalf("block state must remain pending, got %s", st.StateOf(dstr(1)))
	}

	must(t, st.EndSession("long"))
	r2b, err := st.GCRound2()
	must(t, err)
	if r2b.Skipped || len(r2b.Deleted) != 1 || r2b.Deleted[0] != dstr(1) {
		t.Fatalf("want delete blk-001 after witnesses end, got %+v", r2b)
	}
	if st.StateOf(dstr(1)) != StateDeleted {
		t.Fatalf("want deleted, got %s", st.StateOf(dstr(1)))
	}
	if _, err := st.Read(dstr(1)); !errors.Is(err, ErrBlockMissing) {
		t.Fatalf("deleted block readable? err=%v", err)
	}
}

// 始终无人引用的块经两轮后删除。
func TestUnreferencedDeletedAfterTwoRounds(t *testing.T) {
	st := New(0)
	st.BeginSession("s1")
	must(t, st.Upload("s1", dstr(1), []byte("a")))
	must(t, st.Upload("s1", dstr(2), []byte("b")))
	must(t, st.Commit("s1", "m", []Digest{dstr(1)}))
	must(t, st.EndSession("s1"))

	r1, err := st.GCRound1()
	must(t, err)
	if len(r1.Witness) != 0 {
		t.Fatalf("no witness expected, got %v", r1.Witness)
	}
	if len(r1.Pending) != 1 || r1.Pending[0] != dstr(2) {
		t.Fatalf("pending=%v, want blk-002", r1.Pending)
	}
	r2, err := st.GCRound2()
	must(t, err)
	if len(r2.Deleted) != 1 || r2.Deleted[0] != dstr(2) {
		t.Fatalf("deleted=%v, want blk-002", r2.Deleted)
	}
	if st.StateOf(dstr(1)) != StateNormal || st.StateOf(dstr(2)) != StateDeleted {
		t.Fatalf("states: %s %s", st.StateOf(dstr(1)), st.StateOf(dstr(2)))
	}
	m, err := st.ReadManifest("m")
	must(t, err)
	if len(m.Blocks) != 1 {
		t.Fatalf("manifest=%+v", m)
	}
}
