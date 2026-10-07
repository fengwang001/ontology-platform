package linkrepair

import "testing"

func objs(ids ...string) map[ObjectID]bool {
	m := make(map[ObjectID]bool, len(ids))
	for _, id := range ids {
		m[ObjectID(id)] = true
	}
	return m
}

func rec(offset int, typ LinkTypeID, from, to string) RawRecord {
	return RawRecord{Offset: offset, Type: typ, From: ObjectID(from), To: ObjectID(to)}
}

func reasons(res Result) map[int]Reason {
	m := map[int]Reason{}
	for _, d := range res.Dropped {
		m[d.Record.Offset] = d.Reason
	}
	return m
}

func keptSet(res Result) map[Link]bool {
	m := map[Link]bool{}
	for _, l := range res.Kept {
		m[l] = true
	}
	return m
}

// 单一基数（1:1）下 2 个与 3 个不同对方候选：按对方 ID 字典序决胜，
// 与 Offset 无关。
func TestSingletonCardinalityConflict(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"married": {ID: "married", Cardinality: SingletonCardinality()},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("a", "x", "y", "z"),
		Records: []RawRecord{
			rec(0, "married", "a", "y"),
			rec(1, "married", "a", "x"),
			rec(2, "married", "a", "z"),
		},
	}
	res := Repair(snap)
	if len(res.Kept) != 1 || res.Kept[0].To != "x" {
		t.Fatalf("want only a->x kept, got %+v", res.Kept)
	}
	rmap := reasons(res)
	for _, off := range []int{0, 2} {
		if rmap[off] != ReasonCardinality {
			t.Fatalf("offset %d: want cardinality_conflict, got %v", off, rmap[off])
		}
	}
	if _, present := rmap[1]; present {
		t.Fatalf("offset 1 must be kept, got reason %v", rmap[1])
	}

	// 两个候选的更小组合：大 Offset 的小 ID 仍然胜出。
	snap2 := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("a", "p", "q"),
		Records:          []RawRecord{rec(7, "married", "a", "q"), rec(9, "married", "a", "p")},
	}
	res2 := Repair(snap2)
	if len(res2.Kept) != 1 || res2.Kept[0].To != "p" {
		t.Fatalf("want a->p regardless of offset, got %+v", res2.Kept)
	}
}

// 带上限的基数约束恰好被超出（上限 2，候选 3 个）。
func TestBoundedCardinalityExceededByOne(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"owns": {ID: "owns", Cardinality: Cardinality{MaxFrom: 2, MaxTo: Unbounded}},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("o", "a", "b", "c"),
		Records: []RawRecord{
			rec(0, "owns", "o", "c"),
			rec(1, "owns", "o", "a"),
			rec(2, "owns", "o", "b"),
		},
	}
	res := Repair(snap)
	got := keptSet(res)
	if !got[Link{Type: "owns", From: "o", To: "a"}] ||
		!got[Link{Type: "owns", From: "o", To: "b"}] ||
		got[Link{Type: "owns", From: "o", To: "c"}] {
		t.Fatalf("want o->a,o->b kept and o->c dropped, got %+v", res.Kept)
	}
	if reasons(res)[0] != ReasonCardinality {
		t.Fatalf("offset 0 must be cardinality_conflict")
	}
	if res.Stats.ConflictGroups != 1 || res.Stats.ConflictGroupWork != 3 {
		t.Fatalf("conflict work stats wrong: %+v", res.Stats)
	}
}

// 未超限时无冲突分组，冲突工作量度量为 0（不触发排序）。
func TestNoConflictMeansNoConflictWork(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"owns": {ID: "owns", Cardinality: Cardinality{MaxFrom: 3, MaxTo: Unbounded}},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("o", "a", "b"),
		Records: []RawRecord{
			rec(0, "owns", "o", "a"),
			rec(1, "owns", "o", "b"),
		},
	}
	res := Repair(snap)
	if res.Stats.ConflictGroups != 0 || res.Stats.ConflictGroupWork != 0 {
		t.Fatalf("expected zero conflict work, got %+v", res.Stats)
	}
}

// 残留不完整记录：只知一端或类型未知，整体按结构损坏舍弃，不补全。
func TestResidualRecordDiscarded(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"lk": {ID: "lk", Cardinality: SingletonCardinality()},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("a", "b"),
		Records: []RawRecord{
			rec(0, "lk", "a", ""),
			rec(1, "lk", "", "b"),
			rec(2, "", "a", "b"),
			rec(3, "unknown", "a", "b"),
			rec(4, "lk", "a", "b"),
		},
	}
	res := Repair(snap)
	rmap := reasons(res)
	for _, off := range []int{0, 1, 2, 3} {
		if rmap[off] != ReasonMalformed {
			t.Fatalf("offset %d: want malformed_structure, got %v", off, rmap[off])
		}
	}
	if len(res.Kept) != 1 {
		t.Fatalf("only the complete record should survive, got %+v", res.Kept)
	}
}

// 记录完整但引用了不可用对象：原因独立于结构损坏与基数冲突。
func TestReferenceUnavailable(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"lk": {ID: "lk", Cardinality: SingletonCardinality()},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("a", "b"),
		Records: []RawRecord{
			rec(0, "lk", "a", "ghost"),
			rec(1, "lk", "a", "b"),
		},
	}
	res := Repair(snap)
	if r := reasons(res)[0]; r != ReasonRefUnavailable {
		t.Fatalf("want reference_unavailable, got %v", r)
	}
	if len(res.Kept) != 1 || res.Kept[0].To != "b" {
		t.Fatalf("dangling record must not win cardinality slot, got %+v", res.Kept)
	}
}
