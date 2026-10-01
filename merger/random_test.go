package merger

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// randomWorld 用统一随机源驱动 Merger 与朴素 Oracle，
// 并记录段/句柄/存活键账本与逐步日志（输入、输出、判定依据）。
type randomWorld struct {
	rng          *rand.Rand
	log          strings.Builder
	step         int
	segIDs       []int
	openHandles  []int
	handleInputs map[int][]int
	liveKeys     []string
	keyPool      []string
	termPool     []string
}

func newRandomWorld(seed int64) *randomWorld {
	w := &randomWorld{
		rng:          rand.New(rand.NewSource(seed)),
		handleInputs: map[int][]int{},
		keyPool:      []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7"},
		termPool:     []string{"a", "b", "c", "d", "ab", "ba"},
	}
	fmt.Fprintf(&w.log, "seed=%d\n", seed)
	return w
}

func (w *randomWorld) record(input, output string) {
	w.step++
	fmt.Fprintf(&w.log, "%3d: %s => %s\n", w.step, input, output)
}

func rwPick[T any](rng *rand.Rand, xs []T) (T, bool) {
	var zero T
	if len(xs) == 0 {
		return zero, false
	}
	return xs[rng.Intn(len(xs))], true
}

func rwRemoveInt(xs []int, v int) []int {
	out := xs[:0]
	for _, x := range xs {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func rwRemoveString(xs []string, v string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func rwContainsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func postingsEqual(a, b []Posting) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Doc != b[i].Doc || a[i].TF != b[i].TF {
			return false
		}
		if len(a[i].Positions) != len(b[i].Positions) {
			return false
		}
		for j := range a[i].Positions {
			if a[i].Positions[j] != b[i].Positions[j] {
				return false
			}
		}
	}
	return true
}

func (w *randomWorld) randomTerms() []string {
	n := w.rng.Intn(5)
	terms := make([]string, 0, n)
	for i := 0; i < n; i++ {
		t, _ := rwPick(w.rng, w.termPool)
		terms = append(terms, t)
	}
	return terms
}

func (w *randomWorld) randomSegRef() int {
	if id, ok := rwPick(w.rng, w.segIDs); ok && w.rng.Intn(5) != 0 {
		return id
	}
	return 1000 + w.rng.Intn(20)
}

func (w *randomWorld) pickMergeIDs() []int {
	switch w.rng.Intn(8) {
	case 0:
		id := 7000
		if x, ok := rwPick(w.rng, w.segIDs); ok {
			id = x
		}
		return []int{id} // 参数非法：少于 2 个
	case 1:
		id := 7001
		if x, ok := rwPick(w.rng, w.segIDs); ok {
			id = x
		}
		return []int{id, id} // 参数非法：重复
	case 2:
		id := 7002
		if x, ok := rwPick(w.rng, w.segIDs); ok {
			id = x
		}
		return []int{id, 5000 + w.rng.Intn(3)} // 段不存在
	}
	if len(w.segIDs) < 2 {
		return []int{7003, 7004}
	}
	n := 2 + w.rng.Intn(2)
	if n > len(w.segIDs) {
		n = len(w.segIDs)
	}
	perm := w.rng.Perm(len(w.segIDs))[:n]
	ids := make([]int, n)
	for i, p := range perm {
		ids[i] = w.segIDs[p]
	}
	return ids
}

func (w *randomWorld) refreshLiveKeys(o *Oracle) {
	live := make([]string, 0, len(o.live))
	for key := range o.live {
		live = append(live, key)
	}
	w.liveKeys = live
}

func (w *randomWorld) hasLiveKey(key string) bool {
	for _, k := range w.liveKeys {
		if k == key {
			return true
		}
	}
	return false
}

func runRandomSequence(t *testing.T, seed int64) {
	t.Helper()
	m := New()
	o := NewOracle()
	w := newRandomWorld(seed)

	ops := 30 + w.rng.Intn(60)
	for i := 0; i < ops; i++ {
		switch w.rng.Intn(10) {
		case 0, 1, 2: // Register
			docs := w.randomDocs()
			gotID, gotErr := m.Register(docs)
			wantID, wantCode := o.Register(docs)
			w.record(fmt.Sprintf("Register(%v)", docs),
				fmt.Sprintf("id=%d err=%v | oracle id=%d err=%v", gotID, errCode(gotErr), wantID, wantCode))
			if gotID != wantID || errCode(gotErr) != wantCode {
				t.Fatalf("seed %d Register mismatch\n%s", seed, w.log.String())
			}
			if gotErr == nil {
				w.segIDs = append(w.segIDs, gotID)
				for _, d := range docs {
					w.liveKeys = append(w.liveKeys, d.Key)
				}
			}
		case 3: // Delete
			key := "missing"
			if k, ok := rwPick(w.rng, w.keyPool); ok {
				key = k
			}
			gotErr := m.Delete(key)
			wantCode := o.Delete(key)
			w.record(fmt.Sprintf("Delete(%q)", key),
				fmt.Sprintf("err=%v | oracle err=%v", errCode(gotErr), wantCode))
			if errCode(gotErr) != wantCode {
				t.Fatalf("seed %d Delete mismatch\n%s", seed, w.log.String())
			}
			if gotErr == nil {
				w.liveKeys = rwRemoveString(w.liveKeys, key)
			}
		case 4, 5: // BeginMerge
			ids := w.pickMergeIDs()
			gotH, gotErr := m.BeginMerge(ids)
			wantH, wantCode := o.BeginMerge(ids)
			w.record(fmt.Sprintf("BeginMerge(%v)", ids),
				fmt.Sprintf("handle=%d err=%v | oracle handle=%d err=%v", gotH, errCode(gotErr), wantH, wantCode))
			if gotH != wantH || errCode(gotErr) != wantCode {
				t.Fatalf("seed %d BeginMerge mismatch\n%s", seed, w.log.String())
			}
			if gotErr == nil {
				w.openHandles = append(w.openHandles, gotH)
				w.handleInputs[gotH] = append([]int(nil), ids...)
			}
		case 6: // Commit
			handle := 8000 + w.rng.Intn(4)
			if h, ok := rwPick(w.rng, w.openHandles); ok && w.rng.Intn(6) != 0 {
				handle = h
			}
			gotID, gotErr := m.Commit(handle)
			wantID, wantCode := o.Commit(handle)
			w.record(fmt.Sprintf("Commit(%d)", handle),
				fmt.Sprintf("id=%d err=%v | oracle id=%d err=%v", gotID, errCode(gotErr), wantID, wantCode))
			if gotID != wantID || errCode(gotErr) != wantCode {
				t.Fatalf("seed %d Commit mismatch\n%s", seed, w.log.String())
			}
			if gotErr == nil {
				for _, sid := range w.handleInputs[handle] {
					w.segIDs = rwRemoveInt(w.segIDs, sid)
				}
				delete(w.handleInputs, handle)
				w.segIDs = append(w.segIDs, gotID)
				w.openHandles = rwRemoveInt(w.openHandles, handle)
				w.refreshLiveKeys(o)
			}
		case 7: // Abort
			handle := 8000 + w.rng.Intn(4)
			if h, ok := rwPick(w.rng, w.openHandles); ok && w.rng.Intn(6) != 0 {
				handle = h
			}
			gotErr := m.Abort(handle)
			wantCode := o.Abort(handle)
			w.record(fmt.Sprintf("Abort(%d)", handle),
				fmt.Sprintf("err=%v | oracle err=%v", errCode(gotErr), wantCode))
			if errCode(gotErr) != wantCode {
				t.Fatalf("seed %d Abort mismatch\n%s", seed, w.log.String())
			}
			if gotErr == nil {
				delete(w.handleInputs, handle)
				w.openHandles = rwRemoveInt(w.openHandles, handle)
			}
		case 8: // Stats
			id := w.randomSegRef()
			got, gotErr := m.Stats(id)
			want, wantCode := o.Stats(id)
			w.record(fmt.Sprintf("Stats(%d)", id),
				fmt.Sprintf("stats=%+v err=%v | oracle %+v err=%v", got, errCode(gotErr), want, wantCode))
			if got != want || errCode(gotErr) != wantCode {
				t.Fatalf("seed %d Stats mismatch\n%s", seed, w.log.String())
			}
		case 9: // Postings
			id := w.randomSegRef()
			term := "zzz"
			if t0, ok := rwPick(w.rng, w.termPool); ok {
				term = t0
			}
			if w.rng.Intn(4) == 0 {
				term = "zzz"
			}
			got, gotErr := m.Postings(id, term)
			want, wantCode := o.Postings(id, term)
			w.record(fmt.Sprintf("Postings(%d,%q)", id, term),
				fmt.Sprintf("n=%d err=%v | oracle n=%d err=%v", len(got), errCode(gotErr), len(want), wantCode))
			if errCode(gotErr) != wantCode || !postingsEqual(got, want) {
				t.Fatalf("seed %d Postings mismatch got=%+v want=%+v\n%s", seed, got, want, w.log.String())
			}
		}

		// 判定依据：全量快照一致（段集合、Stats、按字节序的词项集合、全部倒排表，
		// 且各段 numDocs 之和等于存活键个数）。
		gotSnap := m.snapshot()
		wantSnap := o.Snapshot()
		if equal, reason := snapshotsEqual(gotSnap, wantSnap); !equal {
			fmt.Fprintf(&w.log, "SNAPSHOT MISMATCH: %s\n  got=%+v\n want=%+v\n", reason, gotSnap, wantSnap)
			t.Fatalf("seed %d snapshot mismatch: %s\n%s", seed, reason, w.log.String())
		}
	}

	if len(w.liveKeys) != len(o.live) {
		t.Fatalf("seed %d live key count mismatch\n%s", seed, w.log.String())
	}
	fmt.Fprintf(&w.log, "verdict=MATCH: segIDs=%v; stats, byte-ordered terms and all postings equal; sum(numDocs)==liveKeys==%d\n",
		w.segIDs, len(o.live))
	t.Logf("seed=%d ops=%d\n%s", seed, ops, w.log.String())
}

func (w *randomWorld) randomDocs() []Doc {
	switch w.rng.Intn(10) {
	case 0:
		return nil // 空批
	case 1:
		return []Doc{{Key: ""}} // 空键
	case 2:
		return []Doc{{Key: "bad", Terms: []string{"ok", ""}}} // 空词项
	case 3:
		if len(w.liveKeys) > 0 {
			key, _ := rwPick(w.rng, w.liveKeys)
			return []Doc{{Key: key, Terms: w.randomTerms()}} // 存活键冲突
		}
	case 4:
		return []Doc{
			{Key: "dup", Terms: w.randomTerms()},
			{Key: "dup", Terms: w.randomTerms()},
		} // 批内重复
	}
	n := 1 + w.rng.Intn(3)
	docs := make([]Doc, 0, n)
	used := map[string]bool{}
	for j := 0; j < n; j++ {
		key, _ := rwPick(w.rng, w.keyPool)
		if used[key] || w.hasLiveKey(key) {
			continue
		}
		used[key] = true
		docs = append(docs, Doc{Key: key, Terms: w.randomTerms()})
	}
	if len(docs) == 0 {
		return nil
	}
	return docs
}

func TestRandomSequencesAgainstOracle(t *testing.T) {
	const groups = 2000
	for seed := int64(1); seed <= groups; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			runRandomSequence(t, seed)
		})
	}
}

// TestConcurrentSmoke 在 -race 下验证所有操作可并发调用且账本不变量始终成立。
func TestConcurrentSmoke(t *testing.T) {
	m := New()
	ids := make(chan int, 16)
	for i := 0; i < 4; i++ {
		id, err := m.Register([]Doc{{
			Key:   fmt.Sprintf("init%d", i),
			Terms: []string{"x", "y"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		ids <- id
	}
	close(ids)

	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			all := []int{1, 2, 3, 4}
			for i := 0; i < 200; i++ {
				switch rng.Intn(6) {
				case 0:
					_, _ = m.Register([]Doc{{Key: fmt.Sprintf("g%dk%d", g, i), Terms: []string{"z"}}})
				case 1:
					_ = m.Delete(fmt.Sprintf("init%d", rng.Intn(4)))
				case 2:
					perm := rng.Perm(4)
					_, _ = m.BeginMerge([]int{all[perm[0]], all[perm[1]]})
				case 3:
					_, _ = m.Commit(1 + rng.Intn(8))
				case 4:
					_ = m.Abort(1 + rng.Intn(8))
				case 5:
					_, _ = m.Stats(1 + rng.Intn(6))
				}
			}
		}(g)
	}
	wg.Wait()

	// 并发结束后不变量：各在册段 numDocs 之和等于存活键个数。
	snap := m.snapshot()
	if snap.numDocs != snap.liveKeys {
		t.Fatalf("invariant violated: numDocs=%d liveKeys=%d", snap.numDocs, snap.liveKeys)
	}
}
