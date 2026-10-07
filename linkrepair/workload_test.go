package linkrepair

import "testing"

// 可复核的工作量证明：
// 在单基数（cap=1）下，无冲突分组的记录数增长不应计入冲突工作量；
// ConflictGroupWork 恰好等于各超限分组的候选数之和。
func TestConflictWorkProportionalToConflictSites(t *testing.T) {
	types := map[LinkTypeID]LinkType{
		"lk": {ID: "lk", Cardinality: SingletonCardinality()},
	}
	avail := map[ObjectID]bool{}
	var recs []RawRecord
	off := 0

	// 10000 个互不冲突的“一人一条”记录：全部线性登记，零排序工作。
	for i := 0; i < 10000; i++ {
		from := ObjectID("f" + padW(i))
		to := ObjectID("t" + padW(i))
		avail[from] = true
		avail[to] = true
		recs = append(recs, rec(off, "lk", string(from), string(to)))
		off++
	}
	res := Repair(Snapshot{LinkTypes: types, AvailableObjects: avail, Records: recs})
	if res.Stats.ConflictGroups != 0 || res.Stats.ConflictGroupWork != 0 {
		t.Fatalf("clean data must incur zero conflict work, got groups=%d work=%d",
			res.Stats.ConflictGroups, res.Stats.ConflictGroupWork)
	}
	if res.Stats.Kept != 10000 {
		t.Fatalf("all 10000 non-conflicting records must survive, got %d", res.Stats.Kept)
	}

	// 再注入 3 个局部冲突点：分别有 4、2、5 个不同对方候选。
	conflictOwners := map[string]int{"cf1": 4, "cf2": 2, "cf3": 5}
	for owner, n := range conflictOwners {
		avail[ObjectID(owner)] = true
		for j := 0; j < n; j++ {
			peer := ObjectID("p" + owner[1:] + "_" + padW(j))
			avail[peer] = true
			recs = append(recs, rec(off, "lk", owner, string(peer)))
			off++
		}
	}
	res = Repair(Snapshot{LinkTypes: types, AvailableObjects: avail, Records: recs})
	// 每个冲突点在 From 侧构成 1 个超限分组；To 侧各 peer 只有 1 条，
	// 不超限，因此冲突分组恰为 From 侧 3 个。
	if res.Stats.ConflictGroups != 3 {
		t.Fatalf("want 3 over-cap from-side groups, got %d", res.Stats.ConflictGroups)
	}
	if res.Stats.ConflictGroupWork != 4+2+5 {
		t.Fatalf("conflict work must equal sum of candidates only in over-cap groups (11), got %d",
			res.Stats.ConflictGroupWork)
	}
}

func padW(i int) string {
	switch {
	case i < 10:
		return "0000" + itoaTest(i)
	case i < 100:
		return "000" + itoaTest(i)
	case i < 1000:
		return "00" + itoaTest(i)
	case i < 10000:
		return "0" + itoaTest(i)
	default:
		return itoaTest(i)
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
