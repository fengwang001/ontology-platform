package visible

import (
	"errors"
	"math/rand"
	"ontology/snapshot"
	"ontology/txn"
	"sync"
	"testing"
)

var tab = txn.NewTable(map[txn.ID]txn.Record{1: {Status: txn.Committed, Commit: 5}, 2: {Status: txn.Committed, Commit: 10}, 3: {Status: txn.Aborted}, 4: {Status: txn.Active}, 5: {Status: txn.Committed, Commit: 7}})
func mkSnap(wm uint64, own txn.ID, act ...txn.ID) *snapshot.Snapshot {
	s, _ := snapshot.New(wm, own, act)
	return s
}
func must(t *testing.T, cond bool, msg string, args ...any) {
	if cond {
		return
	}
	t.Fatalf(msg, args...)
}
func TestJudge(t *testing.T) {
	names := []string{"counterexample: in active invisible", "commit == watermark invisible", "watermark zero invisible", "empty active visible", "own write visible", "aborted never visible", "unknown txn", "corrupt chain", "newest visible wins"}
	wms := []uint64{10, 10, 0, 10, 10, 10, 10, 10, 10}
	owns := []txn.ID{0, 0, 0, 0, 4, 0, 0, 0, 0}
	acts := [][]txn.ID{{1}, nil, nil, nil, {4}, nil, nil, nil, nil}
	chains := [][]txn.ID{{1}, {2}, {1}, {1}, {4}, {3}, {99}, {5, 1}, {1, 5}}
	wants := []txn.ID{0, 0, 0, 1, 4, 0, 0, 0, 5}
	errs := []error{nil, nil, nil, nil, nil, nil, txn.ErrUnknown, ErrCorrupt, nil}
	for i := range names {
		id, _, e := Judge(tab, mkSnap(wms[i], owns[i], acts[i]...), chains[i])
		must(t, id == wants[i] && errors.Is(e, errs[i]), "case %d %s: id=%d err=%v, want id=%d err=%v", i, names[i], id, e, wants[i], errs[i])
	}
	r1, _ := tab.Get(1)
	must(t, r1.Commit < 10, "naive rule must say visible for the counterexample")
}
func TestExtras(t *testing.T) {
	_, err := snapshot.New(1, 0, make([]txn.ID, snapshot.MaxActive+1))
	must(t, errors.Is(err, snapshot.ErrTooManyActive), "want ErrTooManyActive, got %v", err)
	recs, act := map[txn.ID]txn.Record{}, make([]txn.ID, 1000)
	for i := 1; i <= 10000; i++ {
		recs[txn.ID(i)] = txn.Record{Status: txn.Committed, Commit: uint64(i)}
		act[(i-1)%1000] = txn.ID(i)
	}
	tt, s := txn.NewTable(recs), mkSnap(5000, 0, act...)
	id, ok, n, err := JudgeCount(tt, s, []txn.ID{4000})
	must(t, s.Items() == 1000 && err == nil && ok && id == 4000 && n <= 2, "items=%d id=%d ok=%v lookups=%d err=%v", s.Items(), id, ok, n, err)
	rs := mkSnap(10, 0)
	rs.Release()
	_, _, err = Judge(tt, rs, nil)
	must(t, errors.Is(err, snapshot.ErrReleased), "want ErrReleased, got %v", err)
	s, want := mkSnap(10, 0, 1), []bool{false, true}
	var wg sync.WaitGroup
	bad := make(chan bool, 12000)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 1500; k++ {
				_, v, _ := Judge(tab, s, []txn.ID{txn.ID(k%2*4 + 1)})
				if v != want[k%2] {
					bad <- true
				}
			}
		}()
	}
	wg.Wait()
	must(t, len(bad) == 0, "concurrent judgements differed")
}
func naiveJudge(recs map[txn.ID]txn.Record, s *snapshot.Snapshot, id txn.ID) bool {
	if id == s.Owner() {
		return true
	}
	for tid, rec := range recs {
		if tid == id {
			return rec.Status == txn.Committed && rec.Commit < s.Watermark() && !s.InActive(id)
		}
	}
	return false
}
func TestRandomCrossCheck(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	recs := map[txn.ID]txn.Record{}
	for i := 1; i <= 500; i++ {
		recs[txn.ID(i)] = txn.Record{Status: txn.Status(r.Intn(3)), Commit: uint64(r.Intn(300))}
	}
	for k := 0; k < 10000; k++ {
		act := make([]txn.ID, 0, 25)
		for i := txn.ID(1); i <= 500; i++ {
			if r.Intn(20) == 0 {
				act = append(act, i)
			}
		}
		s := mkSnap(uint64(r.Intn(300)), txn.ID(r.Intn(501)), act...)
		id := txn.ID(r.Intn(500) + 1)
		_, got, err := Judge(txn.NewTable(recs), s, []txn.ID{id})
		must(t, err == nil && got == naiveJudge(recs, s, id), "case %d: got=%v err=%v", k, got, err)
	}
}
