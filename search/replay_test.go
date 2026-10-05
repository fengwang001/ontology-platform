package search_test

import (
	"cmp"
	"errors"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"ontology/pit"
	"ontology/search"
	"ontology/segstore"
)

// 朴素模拟：按题目规则逐步实现（开启时复制删除位图、每次操作后全量扫描
// 释放），与三个包的实现对照 1500 组随机操作序列。

type simDoc struct {
	id    string
	val   int64
	delOp int64
}

type simSeg struct {
	docs []simDoc // 按 (val, id) 排序，下标即段内序号
}

type simPIT struct {
	segs map[int64]bool
	exp  int64
	del  map[int64]map[string]bool // 开启时刻各段已删除的 id
}

type sim struct {
	clock    int64
	opSeq    int64
	segSeq   int64
	pitSeq   int64
	pmax     int
	segs     map[int64]*simSeg
	view     map[int64]bool
	pits     map[int64]*simPIT
	released []int64
}

func newSim(pmax int) *sim {
	return &sim{
		pmax: pmax,
		segs: map[int64]*simSeg{},
		view: map[int64]bool{},
		pits: map[int64]*simPIT{},
	}
}

// op 是模拟的操作流水线：时钟检查 → 落地过期 → 推进时钟 → body → 全量释放扫描。
// 落地与时钟推进在 body 之前生效，body 失败也不回滚。
func (sm *sim) op(now int64, body func() error) error {
	if now < sm.clock {
		return segstore.ErrClock
	}
	for pid, p := range sm.pits {
		if p.exp <= now {
			delete(sm.pits, pid)
		}
	}
	sm.clock = now
	err := body()
	sm.sweep()
	return err
}

// sweep 释放失去全部引用（在视图中或被某存活 PIT 持有）的段，按段号升序。
func (sm *sim) sweep() {
	ref := map[int64]bool{}
	for n := range sm.view {
		ref[n] = true
	}
	for _, p := range sm.pits {
		for n := range p.segs {
			ref[n] = true
		}
	}
	var gone []int64
	for n := range sm.segs {
		if !ref[n] {
			gone = append(gone, n)
		}
	}
	slices.Sort(gone)
	for _, n := range gone {
		delete(sm.segs, n)
	}
	sm.released = append(sm.released, gone...)
}

func (sm *sim) aliveInView(id string) bool {
	for n := range sm.view {
		for _, d := range sm.segs[n].docs {
			if d.id == id && d.delOp == 0 {
				return true
			}
		}
	}
	return false
}

func sortSimDocs(docs []simDoc) {
	slices.SortFunc(docs, func(a, b simDoc) int {
		if a.val != b.val {
			return cmp.Compare(a.val, b.val)
		}
		return strings.Compare(a.id, b.id)
	})
}

func (sm *sim) addSegment(now int64, docs []segstore.Doc) (int64, error) {
	if now < 0 || now > segstore.MaxNow || len(docs) < 1 || len(docs) > 10000 {
		return 0, segstore.ErrInvalidParam
	}
	seen := map[string]bool{}
	for _, d := range docs {
		if d.ID == "" || seen[d.ID] {
			return 0, segstore.ErrInvalidParam
		}
		seen[d.ID] = true
	}
	var num int64
	err := sm.op(now, func() error {
		for _, d := range docs {
			if sm.aliveInView(d.ID) {
				return segstore.ErrIDConflict
			}
		}
		sorted := make([]simDoc, len(docs))
		for i, d := range docs {
			sorted[i] = simDoc{id: d.ID, val: d.SortVal}
		}
		sortSimDocs(sorted)
		sm.segSeq++
		num = sm.segSeq
		sm.segs[num] = &simSeg{docs: sorted}
		sm.view[num] = true
		sm.opSeq++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return num, nil
}

func (sm *sim) delete(now int64, id string) error {
	if now < 0 || now > segstore.MaxNow {
		return segstore.ErrInvalidParam
	}
	return sm.op(now, func() error {
		for n := range sm.view {
			seg := sm.segs[n]
			for i := range seg.docs {
				if seg.docs[i].id == id && seg.docs[i].delOp == 0 {
					sm.opSeq++
					seg.docs[i].delOp = sm.opSeq
					return nil
				}
			}
		}
		return segstore.ErrDocNotFound
	})
}

func (sm *sim) merge(now int64, nums []int64) (int64, error) {
	if now < 0 || now > segstore.MaxNow || len(nums) < 2 || len(nums) > 10 {
		return 0, segstore.ErrInvalidParam
	}
	seen := map[int64]bool{}
	for _, n := range nums {
		if seen[n] {
			return 0, segstore.ErrInvalidParam
		}
		seen[n] = true
	}
	var newNum int64
	err := sm.op(now, func() error {
		for _, n := range nums {
			if !sm.view[n] {
				return segstore.ErrSegNotFound
			}
		}
		var live []simDoc
		for _, n := range nums {
			for _, d := range sm.segs[n].docs {
				if d.delOp == 0 {
					live = append(live, simDoc{id: d.id, val: d.val})
				}
			}
		}
		for _, n := range nums {
			delete(sm.view, n)
		}
		if len(live) > 0 {
			sortSimDocs(live)
			sm.segSeq++
			newNum = sm.segSeq
			sm.segs[newNum] = &simSeg{docs: live}
			sm.view[newNum] = true
		}
		sm.opSeq++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newNum, nil
}

func (sm *sim) open(now, ka int64) (int64, error) {
	if now < 0 || now > segstore.MaxNow || ka < 1 || ka > pit.MaxKA {
		return 0, segstore.ErrInvalidParam
	}
	var pid int64
	err := sm.op(now, func() error {
		if len(sm.pits) >= sm.pmax {
			return pit.ErrLimit
		}
		sm.opSeq++
		sm.pitSeq++
		pid = sm.pitSeq
		p := &simPIT{segs: map[int64]bool{}, exp: now + ka, del: map[int64]map[string]bool{}}
		for n := range sm.view {
			p.segs[n] = true
			del := map[string]bool{}
			for _, d := range sm.segs[n].docs {
				if d.delOp > 0 {
					del[d.id] = true
				}
			}
			p.del[n] = del
		}
		sm.pits[pid] = p
		return nil
	})
	if err != nil {
		return 0, err
	}
	return pid, nil
}

func (sm *sim) close(now, pid int64) error {
	if now < 0 || now > segstore.MaxNow {
		return segstore.ErrInvalidParam
	}
	return sm.op(now, func() error {
		if _, ok := sm.pits[pid]; !ok {
			return pit.ErrNotFound
		}
		delete(sm.pits, pid)
		return nil
	})
}

func keyLess(a, b search.Key) bool {
	if a.SortVal != b.SortVal {
		return a.SortVal < b.SortVal
	}
	if a.Seg != b.Seg {
		return a.Seg < b.Seg
	}
	return a.Idx < b.Idx
}

func (sm *sim) search(now, pid int64, size int, after *search.Key, ka int64) ([]search.Hit, error) {
	if now < 0 || now > segstore.MaxNow || size < 1 || size > 1000 ||
		ka < 0 || ka > pit.MaxKA || pid < 0 {
		return nil, segstore.ErrInvalidParam
	}
	if pid == 0 && (after != nil || ka != 0) {
		return nil, segstore.ErrInvalidParam
	}
	var hits []search.Hit
	err := sm.op(now, func() error {
		var cands []search.Hit
		if pid == 0 {
			for n := range sm.view {
				for i, d := range sm.segs[n].docs {
					if d.delOp == 0 {
						cands = append(cands, search.Hit{ID: d.id, Key: key(d.val, n, int64(i))})
					}
				}
			}
		} else {
			p, ok := sm.pits[pid]
			if !ok {
				return pit.ErrNotFound
			}
			if ka > 0 && now+ka > p.exp {
				p.exp = now + ka
			}
			for n := range p.segs {
				seg := sm.segs[n]
				if seg == nil {
					continue
				}
				for i, d := range seg.docs {
					if !p.del[n][d.id] {
						cands = append(cands, search.Hit{ID: d.id, Key: key(d.val, n, int64(i))})
					}
				}
			}
		}
		slices.SortFunc(cands, func(a, b search.Hit) int {
			if keyLess(a.Key, b.Key) {
				return -1
			}
			return 1
		})
		for _, c := range cands {
			if after != nil && !keyLess(*after, c.Key) {
				continue
			}
			hits = append(hits, c)
			if len(hits) == size {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// errCat 用 errors.Is 把错误归类，作为判定依据打印并用于对照。
func errCat(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, segstore.ErrInvalidParam):
		return "invalid-param"
	case errors.Is(err, segstore.ErrClock):
		return "clock-regression"
	case errors.Is(err, pit.ErrNotFound):
		return "pit-not-found"
	case errors.Is(err, segstore.ErrIDConflict):
		return "id-conflict"
	case errors.Is(err, segstore.ErrDocNotFound):
		return "doc-not-found"
	case errors.Is(err, segstore.ErrSegNotFound):
		return "seg-not-found"
	case errors.Is(err, pit.ErrLimit):
		return "pit-limit"
	default:
		return "unknown: " + err.Error()
	}
}

func TestRandomReplayAgainstNaiveSim(t *testing.T) {
	for seed := int64(0); seed < 1500; seed++ {
		runSequence(t, seed)
	}
}

func runSequence(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	store := segstore.NewStore()
	pmax := 1 + rng.Intn(4)
	mgr := pit.NewManager(store, pmax)
	sch := search.NewSearcher(store, mgr)
	sm := newSim(pmax)

	logf := func(format string, args ...any) {
		t.Logf("seed=%d "+format, append([]any{seed}, args...)...)
	}
	checkReleased := func() {
		if got, want := store.Released(), sm.released; !slices.Equal(got, want) {
			t.Fatalf("seed=%d: Released = %v, sim = %v", seed, got, want)
		}
	}

	now := int64(0)
	var lastHits []search.Hit
	nOps := 30 + rng.Intn(40)
	for i := 0; i < nOps; i++ {
		// 时钟：多数前进，偶尔不动或回退，极少产生越界 now。
		switch r := rng.Intn(100); {
		case r < 70:
			now += int64(rng.Intn(40))
		case r < 80:
			// 不变
		default:
			now -= int64(rng.Intn(15))
			if now < 0 {
				now = 0
			}
		}
		opNow := now
		switch r := rng.Intn(100); {
		case r < 2:
			opNow = -1
		case r < 4:
			opNow = segstore.MaxNow + 1
		}

		switch kind := rng.Intn(100); {
		case kind < 25: // AddSegment
			n := 1 + rng.Intn(15)
			docs := make([]segstore.Doc, n)
			for j := range docs {
				docs[j] = segstore.Doc{
					ID:      randID(rng),
					SortVal: int64(rng.Intn(8)),
				}
			}
			gotN, gotErr := store.AddSegment(opNow, docs)
			wantN, wantErr := sm.addSegment(opNow, docs)
			logf("op=%d AddSegment(now=%d, %d docs) -> seg=%d %s", i, opNow, n, gotN, errCat(gotErr))
			if gotN != wantN || errCat(gotErr) != errCat(wantErr) {
				t.Fatalf("seed=%d op=%d AddSegment: got (%d, %s), sim (%d, %s)",
					seed, i, gotN, errCat(gotErr), wantN, errCat(wantErr))
			}
		case kind < 45: // Delete
			id := randID(rng)
			gotErr := store.Delete(opNow, id)
			wantErr := sm.delete(opNow, id)
			logf("op=%d Delete(now=%d, %s) -> %s", i, opNow, id, errCat(gotErr))
			if errCat(gotErr) != errCat(wantErr) {
				t.Fatalf("seed=%d op=%d Delete(%s): got %s, sim %s",
					seed, i, id, errCat(gotErr), errCat(wantErr))
			}
		case kind < 60: // Merge
			nums := randMergeInput(rng, sm)
			gotN, gotErr := store.Merge(opNow, nums)
			wantN, wantErr := sm.merge(opNow, nums)
			logf("op=%d Merge(now=%d, %v) -> seg=%d %s", i, opNow, nums, gotN, errCat(gotErr))
			if gotN != wantN || errCat(gotErr) != errCat(wantErr) {
				t.Fatalf("seed=%d op=%d Merge(%v): got (%d, %s), sim (%d, %s)",
					seed, i, nums, gotN, errCat(gotErr), wantN, errCat(wantErr))
			}
		case kind < 72: // Open
			ka := int64(1 + rng.Intn(120))
			if r := rng.Intn(100); r < 3 {
				ka = 0
			} else if r < 6 {
				ka = pit.MaxKA + 1
			}
			gotP, gotErr := mgr.Open(opNow, ka)
			wantP, wantErr := sm.open(opNow, ka)
			logf("op=%d Open(now=%d, ka=%d) -> pit=%d %s", i, opNow, ka, gotP, errCat(gotErr))
			if gotP != wantP || errCat(gotErr) != errCat(wantErr) {
				t.Fatalf("seed=%d op=%d Open: got (%d, %s), sim (%d, %s)",
					seed, i, gotP, errCat(gotErr), wantP, errCat(wantErr))
			}
		case kind < 82: // Close
			pid := int64(1 + rng.Intn(int(sm.pitSeq)+2))
			gotErr := mgr.Close(opNow, pid)
			wantErr := sm.close(opNow, pid)
			logf("op=%d Close(now=%d, pit=%d) -> %s", i, opNow, pid, errCat(gotErr))
			if errCat(gotErr) != errCat(wantErr) {
				t.Fatalf("seed=%d op=%d Close(%d): got %s, sim %s",
					seed, i, pid, errCat(gotErr), errCat(wantErr))
			}
		default: // Search
			pid, size, after, ka := randSearchInput(rng, sm, lastHits)
			gotHits, gotErr := sch.Search(opNow, pid, size, after, ka)
			wantHits, wantErr := sm.search(opNow, pid, size, after, ka)
			logf("op=%d Search(now=%d, pit=%d, size=%d, after=%v, ka=%d) -> %d hits %s",
				i, opNow, pid, size, after, ka, len(gotHits), errCat(gotErr))
			if errCat(gotErr) != errCat(wantErr) {
				t.Fatalf("seed=%d op=%d Search: got %s, sim %s",
					seed, i, errCat(gotErr), errCat(wantErr))
			}
			if !slices.Equal(gotHits, wantHits) {
				t.Fatalf("seed=%d op=%d Search: got %v, sim %v", seed, i, gotHits, wantHits)
			}
			lastHits = gotHits
		}
		checkReleased()
	}
	logf("done: %d ops, released=%v", nOps, sm.released)
}

func randID(rng *rand.Rand) string {
	return string(rune('a'+rng.Intn(26))) + string(rune('a'+rng.Intn(4)))
}

func randMergeInput(rng *rand.Rand, sm *sim) []int64 {
	if r := rng.Intn(100); r < 3 {
		return []int64{1} // 太少
	} else if r < 6 {
		return []int64{1, 1} // 重复
	}
	k := 2 + rng.Intn(3)
	var view []int64
	for n := range sm.view {
		view = append(view, n)
	}
	out := make([]int64, 0, k)
	for len(out) < k {
		if len(view) > 0 && rng.Intn(100) < 70 {
			out = append(out, view[rng.Intn(len(view))])
		} else {
			out = append(out, int64(1+rng.Intn(int(sm.segSeq)+2)))
		}
	}
	return out
}

func randSearchInput(rng *rand.Rand, sm *sim, lastHits []search.Hit) (pid int64, size int, after *search.Key, ka int64) {
	if rng.Intn(100) < 30 || sm.pitSeq == 0 {
		pid = 0
	} else {
		pid = int64(1 + rng.Intn(int(sm.pitSeq)+1))
	}
	size = 1 + rng.Intn(25)
	if r := rng.Intn(100); r < 2 {
		size = 0
	} else if r < 4 {
		size = 1001
	}
	switch r := rng.Intn(100); {
	case r < 40 || pid == 0:
		after = nil
	case r < 70 && len(lastHits) > 0:
		k := lastHits[len(lastHits)-1].Key
		after = &k
	default:
		k := key(int64(rng.Intn(8)), int64(rng.Intn(int(sm.segSeq)+1)), int64(rng.Intn(3)))
		after = &k
	}
	if pid != 0 {
		switch r := rng.Intn(100); {
		case r < 50:
			ka = 0
		case r < 95:
			ka = int64(1 + rng.Intn(60))
		default:
			ka = pit.MaxKA + 1
		}
	}
	return pid, size, after, ka
}
