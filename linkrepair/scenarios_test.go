package linkrepair

import (
	"reflect"
	"sync"
	"testing"
)

// 重复记录与基数冲突混合：完全相同判重，不同对方判基数，原因不混淆。
func TestDuplicateAndCardinalityMixed(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"lk": {ID: "lk", Cardinality: SingletonCardinality()},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("a", "x", "y"),
		Records: []RawRecord{
			rec(0, "lk", "a", "x"),
			rec(1, "lk", "a", "x"),
			rec(2, "lk", "a", "y"),
			rec(3, "lk", "a", "y"),
		},
	}
	res := Repair(snap)
	rmap := reasons(res)
	if rmap[1] != ReasonDuplicate {
		t.Fatalf("offset 1 want duplicate, got %v", rmap[1])
	}
	if rmap[2] != ReasonCardinality {
		t.Fatalf("offset 2 want cardinality, got %v", rmap[2])
	}
	// 去重先于基数：3 是 a->y 代表（offset 2）的重复，判重复。
	if rmap[3] != ReasonDuplicate {
		t.Fatalf("offset 3 want duplicate (folded before cardinality), got %v", rmap[3])
	}
	if len(res.Kept) != 1 || res.Kept[0].To != "x" {
		t.Fatalf("want only a->x, got %+v", res.Kept)
	}
}

// 四类原因同时出现：计数正确、每条记录恰好归一类。
func TestAllFourReasonsDistinct(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"lk": {ID: "lk", Cardinality: SingletonCardinality()},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("a", "b", "c"),
		Records: []RawRecord{
			rec(0, "lk", "a", ""),      // malformed
			rec(1, "lk", "a", "ghost"), // reference unavailable
			rec(2, "lk", "a", "b"),     // kept
			rec(3, "lk", "a", "b"),     // duplicate
			rec(4, "lk", "a", "c"),     // cardinality
		},
	}
	res := Repair(snap)
	if res.Stats.Malformed != 1 || res.Stats.RefUnavailable != 1 ||
		res.Stats.Duplicate != 1 || res.Stats.Cardinality != 1 ||
		res.Stats.Kept != 1 {
		t.Fatalf("reason counts wrong: %+v", res.Stats)
	}
	if res.Stats.Input != 5 || res.Stats.Kept+len(res.Dropped) != 5 {
		t.Fatalf("accounting must cover every record exactly once: %+v", res.Stats)
	}
}

// 同一对象同时卷入两种链接类型的冲突，两类裁决相互独立。
func TestMultipleLinkTypesIndependent(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"spouse": {ID: "spouse", Cardinality: SingletonCardinality()},
		"owns":   {ID: "owns", Cardinality: Cardinality{MaxFrom: 1, MaxTo: Unbounded}},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("a", "s1", "s2", "t1", "t2"),
		Records: []RawRecord{
			rec(0, "spouse", "a", "s2"),
			rec(1, "spouse", "a", "s1"),
			rec(2, "owns", "a", "t2"),
			rec(3, "owns", "a", "t1"),
		},
	}
	res := Repair(snap)
	got := keptSet(res)
	if !got[Link{Type: "spouse", From: "a", To: "s1"}] ||
		!got[Link{Type: "owns", From: "a", To: "t1"}] {
		t.Fatalf("each link type must adjudicate independently, got %+v", res.Kept)
	}
	if got[Link{Type: "spouse", From: "a", To: "s2"}] ||
		got[Link{Type: "owns", From: "a", To: "t2"}] {
		t.Fatalf("losers of each type must be dropped, got %+v", res.Kept)
	}
	rmap := reasons(res)
	for _, off := range []int{0, 2} {
		if rmap[off] != ReasonCardinality {
			t.Fatalf("offset %d want cardinality, got %v", off, rmap[off])
		}
	}
}

// 反向基数（To 端 MaxTo=1）生效：多个 From 指向同一 To 时裁决。
func TestToSideCardinality(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"memberOf": {ID: "memberOf", Cardinality: Cardinality{MaxFrom: Unbounded, MaxTo: 1}},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("g", "u1", "u2", "u3"),
		Records: []RawRecord{
			rec(0, "memberOf", "u3", "g"),
			rec(1, "memberOf", "u1", "g"),
			rec(2, "memberOf", "u2", "g"),
		},
	}
	res := Repair(snap)
	if len(res.Kept) != 1 || res.Kept[0].From != "u1" {
		t.Fatalf("MaxTo=1 must keep smallest From u1, got %+v", res.Kept)
	}
}

// 反复执行与并发调用结果一致，且输入快照不被修改。
func TestDeterministicConcurrentAndImmutable(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"lk": {ID: "lk", Cardinality: SingletonCardinality()},
		"mn": {ID: "mn", Cardinality: Cardinality{MaxFrom: 2, MaxTo: 1}},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("a", "b", "c", "d"),
		Records: []RawRecord{
			rec(5, "lk", "a", "d"),
			rec(2, "lk", "a", "c"),
			rec(9, "lk", "a", "b"),
			rec(3, "mn", "d", "a"),
			rec(1, "mn", "c", "a"),
			rec(8, "mn", "b", "a"),
			rec(4, "lk", "a", "b"),
			rec(6, "lk", "a", "c"),
		},
	}
	before := append([]RawRecord(nil), snap.Records...)

	base := Repair(snap)
	for i := 0; i < 10; i++ {
		got := Repair(snap)
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("run %d drifted from baseline", i)
		}
	}

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := Repair(snap)
			if !reflect.DeepEqual(got, base) {
				errs <- errResultMismatch
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}

	if !reflect.DeepEqual(snap.Records, before) {
		t.Fatalf("Repair mutated the input snapshot records")
	}
}

var errResultMismatch = mismatchError{}

type mismatchError struct{}

func (mismatchError) Error() string { return "concurrent Repair produced a different result" }

// 结果满足所有声明的基数约束（恢复结果不变量）。
func TestResultSatisfiesAllCardinality(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"one": {ID: "one", Cardinality: SingletonCardinality()},
		"cap": {ID: "cap", Cardinality: Cardinality{MaxFrom: 2, MaxTo: 2}},
	}
	snap := Snapshot{
		LinkTypes:        types,
		AvailableObjects: objs("a", "b", "c", "d", "e"),
		Records: []RawRecord{
			rec(0, "one", "a", "b"),
			rec(1, "one", "a", "c"),
			rec(2, "one", "d", "b"),
			rec(3, "one", "e", "b"),
			rec(4, "cap", "a", "b"),
			rec(5, "cap", "a", "c"),
			rec(6, "cap", "a", "d"),
			rec(7, "cap", "b", "e"),
			rec(8, "cap", "c", "e"),
			rec(9, "cap", "d", "e"),
		},
	}
	res := Repair(snap)
	countFrom := map[LinkTypeID]map[ObjectID]map[ObjectID]bool{}
	countTo := map[LinkTypeID]map[ObjectID]map[ObjectID]bool{}
	for _, l := range res.Kept {
		addCount(countFrom, l.Type, l.From, l.To)
		addCount(countTo, l.Type, l.To, l.From)
	}
	for typ, owners := range countFrom {
		for owner, peers := range owners {
			if max := types[typ].Cardinality.MaxFrom; max != Unbounded && len(peers) > max {
				t.Fatalf("MaxFrom violated: %s from %s has %d peers", typ, owner, len(peers))
			}
		}
	}
	for typ, owners := range countTo {
		for owner, peers := range owners {
			if max := types[typ].Cardinality.MaxTo; max != Unbounded && len(peers) > max {
				t.Fatalf("MaxTo violated: %s to %s has %d peers", typ, owner, len(peers))
			}
		}
	}
}

func addCount(m map[LinkTypeID]map[ObjectID]map[ObjectID]bool, typ LinkTypeID, owner, peer ObjectID) {
	owners, ok := m[typ]
	if !ok {
		owners = map[ObjectID]map[ObjectID]bool{}
		m[typ] = owners
	}
	peers, ok := owners[owner]
	if !ok {
		peers = map[ObjectID]bool{}
		owners[owner] = peers
	}
	peers[peer] = true
}
