package join

import "testing"

// TestRightArrivesBeforeLeft 覆盖“右表先于左表到达”。
func TestRightArrivesBeforeLeft(t *testing.T) {
	j := New(0)
	view := map[string]Entry{}

	res, err := j.Apply([]Op{
		rOp(Insert, "r2", "k1", "rv2"),
		rOp(Insert, "r1", "k1", "rv1"),
		rOp(Insert, "r3", "k2", "rv3"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 {
		t.Fatalf("右行先到且无左行时不应产生连接条目，got %v", res.Entries)
	}

	// 左行到达：与同键全部右行配对，右行按对侧标识字典序排列。
	res, err = j.Apply([]Op{lOp(Insert, "l1", "k1", "lv1")})
	if err != nil {
		t.Fatal(err)
	}
	lr1 := Row{ID: "l1", Key: "k1", Value: "lv1"}
	assertEntries(t, res.Entries, []Entry{
		pairedEntry(Insert, lr1, Row{ID: "r1", Key: "k1", Value: "rv1"}),
		pairedEntry(Insert, lr1, Row{ID: "r2", Key: "k1", Value: "rv2"}),
	})
	replayToView(view, res.Entries)

	// 无同键右行的左行只能以空填充行存在。
	res, err = j.Apply([]Op{lOp(Insert, "l2", "k3", "lv2")})
	if err != nil {
		t.Fatal(err)
	}
	lr2 := Row{ID: "l2", Key: "k3", Value: "lv2"}
	assertEntries(t, res.Entries, []Entry{paddedEntry(Insert, lr2)})
	replayToView(view, res.Entries)
	assertViewEqualsSnapshot(t, j, view)

	// k3 后来了右行：空填充行与配对行互斥，先撤回填充再配对，同一左行两条相邻。
	res, err = j.Apply([]Op{rOp(Insert, "r9", "k3", "rv9")})
	if err != nil {
		t.Fatal(err)
	}
	assertEntries(t, res.Entries, []Entry{
		paddedEntry(Delete, lr2),
		pairedEntry(Insert, lr2, Row{ID: "r9", Key: "k3", Value: "rv9"}),
	})
	replayToView(view, res.Entries)
	assertViewEqualsSnapshot(t, j, view)
}

// TestDeleteLastRightRestoresPadding 覆盖删除最后一条同键右行时补回空填充行。
func TestDeleteLastRightRestoresPadding(t *testing.T) {
	j := New(0)
	view := map[string]Entry{}

	apply := func(ops ...Op) {
		t.Helper()
		res, err := j.Apply(ops)
		if err != nil {
			t.Fatal(err)
		}
		replayToView(view, res.Entries)
		assertViewEqualsSnapshot(t, j, view)
	}

	lr1 := Row{ID: "l1", Key: "k1", Value: "v"}
	apply(lOp(Insert, "l1", "k1", "v"))
	apply(rOp(Insert, "r1", "k1", "a"))
	apply(rOp(Insert, "r2", "k1", "b"))

	// 删除非最后一条右行：仅撤回对应配对行，不产生空填充。
	res, err := j.Apply([]Op{rOp(Delete, "r2", "k1", "b")})
	if err != nil {
		t.Fatal(err)
	}
	assertEntries(t, res.Entries, []Entry{
		pairedEntry(Delete, lr1, Row{ID: "r2", Key: "k1", Value: "b"}),
	})
	replayToView(view, res.Entries)
	assertViewEqualsSnapshot(t, j, view)

	// 删除最后一条同键右行：撤回配对并补回空填充，同一左行两条相邻。
	res, err = j.Apply([]Op{rOp(Delete, "r1", "k1", "a")})
	if err != nil {
		t.Fatal(err)
	}
	assertEntries(t, res.Entries, []Entry{
		pairedEntry(Delete, lr1, Row{ID: "r1", Key: "k1", Value: "a"}),
		paddedEntry(Insert, lr1),
	})
	replayToView(view, res.Entries)
	assertViewEqualsSnapshot(t, j, view)

	snap := j.Snapshot()
	if len(snap) != 1 || !snap[0].IsPadded() {
		t.Fatalf("应仅剩一条空填充行，got %+v", snap)
	}
}

// TestFirstRightWithMultipleLefts 验证首条右行影响多个左行时的排序与相邻性。
func TestFirstRightWithMultipleLefts(t *testing.T) {
	j := New(0)
	if _, err := j.Apply([]Op{
		lOp(Insert, "lB", "k", "b"),
		lOp(Insert, "lA", "k", "a"),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := j.Apply([]Op{rOp(Insert, "r1", "k", "r")})
	if err != nil {
		t.Fatal(err)
	}
	// 右行视角对侧为左标识，按字典序：lA 的撤回/配对两条相邻，随后 lB。
	assertEntries(t, res.Entries, []Entry{
		paddedEntry(Delete, Row{ID: "lA", Key: "k", Value: "a"}),
		pairedEntry(Insert, Row{ID: "lA", Key: "k", Value: "a"}, Row{ID: "r1", Key: "k", Value: "r"}),
		paddedEntry(Delete, Row{ID: "lB", Key: "k", Value: "b"}),
		pairedEntry(Insert, Row{ID: "lB", Key: "k", Value: "b"}, Row{ID: "r1", Key: "k", Value: "r"}),
	})
}

// TestLeftDeleteAndRightDeleteWithoutLefts 覆盖左行删除撤回及无左行时删右行。
func TestLeftDeleteAndRightDeleteWithoutLefts(t *testing.T) {
	j := New(0)
	view := map[string]Entry{}

	res, err := j.Apply([]Op{
		lOp(Insert, "l1", "k", "v"),
		rOp(Insert, "r1", "k", "a"),
		rOp(Insert, "r2", "k", "b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	replayToView(view, res.Entries)

	res, err = j.Apply([]Op{lOp(Delete, "l1", "k", "v")})
	if err != nil {
		t.Fatal(err)
	}
	lr1 := Row{ID: "l1", Key: "k", Value: "v"}
	assertEntries(t, res.Entries, []Entry{
		pairedEntry(Delete, lr1, Row{ID: "r1", Key: "k", Value: "a"}),
		pairedEntry(Delete, lr1, Row{ID: "r2", Key: "k", Value: "b"}),
	})
	replayToView(view, res.Entries)
	assertViewEqualsSnapshot(t, j, view)

	// 已无左行时删除右行不产生连接条目，也不报错。
	res, err = j.Apply([]Op{rOp(Delete, "r1", "k", "a")})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 {
		t.Fatalf("无左行时删右行不应产生连接条目，got %+v", res.Entries)
	}
	assertViewEqualsSnapshot(t, j, view)
}
