package ontology

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// TestPartialLeafPointChecks 验证：仅与查询部分相交的叶子格才逐点判定，
// 整格包含不判定、不相交不访问；且结果与朴素扫描一致。
func TestPartialLeafPointChecks(t *testing.T) {
	idx := mustNew(t, 0, 0, 8, 2, testLogger(slog.LevelWarn))
	pts := []Point{
		{ID: "a", X: 0, Y: 0},
		{ID: "b", X: 1, Y: 1},
		{ID: "c", X: 6, Y: 6},
		{ID: "d", X: 7, Y: 7},
	}
	if err := idx.Insert(pts); err != nil {
		t.Fatal(err)
	}
	// 查询 [0,1)x[0,1) 只含 a，它所在叶子部分相交 -> a、b 都被逐点判定。
	res, err := idx.Query(context.Background(), Rect{0, 0, 1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.IDs, []string{"a"}) {
		t.Fatalf("want [a], got %v", res.IDs)
	}
	if res.Stats.PointChecks != 2 {
		t.Fatalf("partial leaf should point-check both points, got %d", res.Stats.PointChecks)
	}
	if res.Stats.PrunedNodes == 0 {
		t.Fatal("disjoint sibling cells should be pruned")
	}
	t.Logf("部分相交叶子: hits=%v pointChecks=%d pruned=%d tested=%d",
		res.IDs, res.Stats.PointChecks, res.Stats.PrunedNodes, res.Stats.BoundTestedNodes)

	// 整根包含：零逐点判定。
	full, err := idx.Query(context.Background(), Rect{0, 0, 8, 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.IDs) != 4 || full.Stats.PointChecks != 0 {
		t.Fatalf("full query: hits=%d checks=%d", len(full.IDs), full.Stats.PointChecks)
	}
}

func naiveOnMap(m map[string]Point, r Rect) []string {
	ids := make([]string, 0)
	for _, p := range m {
		if r.X0 <= p.X && p.X < r.X1 && r.Y0 <= p.Y && p.Y < r.Y1 {
			ids = append(ids, p.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func randomRect(rng *rand.Rand, side int64) Rect {
	x0, y0 := rng.Int63n(side+8)-4, rng.Int63n(side+8)-4
	x1, y1 := x0+rng.Int63n(12), y0+rng.Int63n(12)
	if rng.Intn(10) == 0 {
		x1 = x0
	}
	if rng.Intn(10) == 0 {
		x0, x1 = x1, x0
	}
	return Rect{x0, y0, x1, y1}
}

// TestRandomDifferential 用固定种子的随机序列对拍四叉树查询与朴素扫描，
// 同时注入非法插入/删除批次验证整体拒绝不改变状态。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			idx := mustNew(t, 0, 0, 64, 8, testLogger(slog.LevelError))
			reference := make(map[string]Point)

			id := func(n int) string { return fmt.Sprintf("id-%05d", n) }
			nextID := 1

			for step := 0; step < 1500; step++ {
				switch rng.Intn(4) {
				case 0, 1:
					batchSize := rng.Intn(4) + 1
					batch := make([]Point, 0, batchSize)
					valid := true
					for k := 0; k < batchSize; k++ {
						if rng.Intn(8) == 0 {
							switch rng.Intn(3) {
							case 0:
								batch = append(batch, Point{ID: id(nextID), X: 64, Y: 0})
							case 1:
								batch = append(batch, Point{ID: "", X: 0, Y: 0})
							default:
								if len(reference) == 0 {
									batch = append(batch, Point{ID: "", X: 0, Y: 0})
								} else {
									live := make([]string, 0, len(reference))
									for k := range reference {
										live = append(live, k)
									}
									batch = append(batch, Point{ID: live[rng.Intn(len(live))], X: 0, Y: 0})
								}
							}
							valid = false
							break
						}
						pid := id(nextID)
						nextID++
						batch = append(batch, Point{ID: pid, X: rng.Int63n(64), Y: rng.Int63n(64)})
					}
					err := idx.Insert(batch)
					if valid {
						if err != nil {
							t.Fatalf("step %d: unexpected insert error: %v", step, err)
						}
						for _, p := range batch {
							reference[p.ID] = p
						}
					} else if err == nil {
						t.Fatalf("step %d: invalid batch accepted: %+v", step, batch)
					}
				case 2:
					if len(reference) == 0 {
						break
					}
					live := make([]string, 0, len(reference))
					for k := range reference {
						live = append(live, k)
					}
					batchSize := rng.Intn(3) + 1
					batch := make([]string, 0, batchSize)
					valid := true
					for k := 0; k < batchSize; k++ {
						if rng.Intn(6) == 0 {
							batch = append(batch, fmt.Sprintf("ghost-%d", rng.Int()))
							valid = false
							break
						}
						batch = append(batch, live[rng.Intn(len(live))])
					}
					seen := map[string]bool{}
					for _, b := range batch {
						if seen[b] {
							valid = false
						}
						seen[b] = true
					}
					err := idx.Delete(batch)
					if valid {
						if err != nil {
							t.Fatalf("step %d: unexpected delete error: %v", step, err)
						}
						for _, b := range batch {
							delete(reference, b)
						}
					} else if err == nil {
						t.Fatalf("step %d: invalid delete accepted: %v", step, batch)
					}
				case 3:
					r := randomRect(rng, 64)
					if r.X1 < r.X0 || r.Y1 < r.Y0 {
						if _, err := idx.Query(context.Background(), r); err == nil {
							t.Fatalf("step %d: reversed rect accepted %v", step, r)
						}
						break
					}
					got, err := idx.Query(context.Background(), r)
					if err != nil {
						t.Fatalf("step %d: query error: %v", step, err)
					}
					want := naiveOnMap(reference, r)
					if !reflect.DeepEqual(got.IDs, want) {
						t.Fatalf("step %d: rect %v mismatch: got %v want %v", step, r, got.IDs, want)
					}
				}
			}
			if got := idx.Stats().PointCount; got != len(reference) {
				t.Fatalf("point count drift: tree=%d ref=%d", got, len(reference))
			}
			t.Logf("seed=%d 对拍通过，最终点数=%d 叶子=%d 溢出格=%d",
				seed, len(reference), idx.Stats().LeafCount, idx.Stats().OverflowCells)
		})
	}
}

// TestPruningEfficiency 均匀分布下，小面积查询的逐点判定次数远小于总点数。
func TestPruningEfficiency(t *testing.T) {
	const side int64 = 1024
	const n = 20000
	idx := mustNew(t, 0, 0, side, 16, testLogger(slog.LevelWarn))
	rng := rand.New(rand.NewSource(42))
	pts := make([]Point, n)
	for i := range pts {
		pts[i] = Point{ID: fmt.Sprintf("u%06d", i),
			X: rng.Int63n(side), Y: rng.Int63n(side)}
	}
	if err := idx.Insert(pts); err != nil {
		t.Fatal(err)
	}

	var totalChecks, totalHits int
	const queries = 200
	for i := 0; i < queries; i++ {
		w := int64(16)
		x0 := rng.Int63n(side - w)
		y0 := rng.Int63n(side - w)
		r := Rect{x0, y0, x0 + w, y0 + w}
		res, err := idx.Query(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if got := idx.naiveQuery(r); !reflect.DeepEqual(got, res.IDs) {
			t.Fatalf("query %d mismatch vs naive", i)
		}
		totalChecks += res.Stats.PointChecks
		totalHits += len(res.IDs)
	}
	avgChecks := totalChecks / queries
	avgHits := totalHits / queries
	ratio := float64(avgChecks) / float64(n)
	t.Logf("均匀分布 n=%d, 16x16 查询 x%d: 平均逐点判定=%d 平均命中=%d 判定/总点=%.4f%%",
		n, queries, avgChecks, avgHits, ratio*100)
	if ratio > 0.02 {
		t.Fatalf("point checks %d exceed 2%% of %d points", avgChecks, n)
	}
	if avgChecks > avgHits*4+16 {
		t.Fatalf("checks %d far above hits %d (pruning ineffective)", avgChecks, avgHits)
	}
}

// TestSameOperationSequenceDeterminism 同一操作序列重建两次，划分与查询完全相同。
func TestSameOperationSequenceDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	type op struct {
		inserts []Point
		deletes []string
		query   Rect
	}
	ops := make([]op, 120)
	next := 0
	for i := range ops {
		o := op{query: Rect{rng.Int63n(40), rng.Int63n(40),
			40 + rng.Int63n(30), 40 + rng.Int63n(30)}}
		for k := rng.Intn(5); k >= 0; k-- {
			o.inserts = append(o.inserts, Point{
				ID: fmt.Sprintf("d%05d", next),
				X:  rng.Int63n(64), Y: rng.Int63n(64)})
			next++
		}
		if rng.Intn(3) == 0 && next > 4 {
			o.deletes = append(o.deletes, fmt.Sprintf("d%05d", rng.Intn(next-1)))
		}
		ops[i] = o
	}

	run := func() (IndexStats, []QueryResult) {
		idx := mustNew(t, 0, 0, 64, 6, testLogger(slog.LevelError))
		results := make([]QueryResult, 0, len(ops))
		for _, o := range ops {
			if len(o.inserts) > 0 {
				if err := idx.Insert(o.inserts); err != nil {
					t.Fatal(err)
				}
			}
			if len(o.deletes) > 0 {
				_ = idx.Delete(o.deletes)
			}
			res, err := idx.Query(context.Background(), o.query)
			if err != nil {
				t.Fatal(err)
			}
			results = append(results, res)
		}
		return idx.Stats(), results
	}
	st1, r1 := run()
	st2, r2 := run()
	if st1 != st2 {
		t.Fatalf("structure stats differ: %+v vs %+v", st1, st2)
	}
	for i := range r1 {
		if !reflect.DeepEqual(r1[i], r2[i]) {
			t.Fatalf("query result %d differs between runs: %+v vs %+v", i, r1[i], r2[i])
		}
	}
	t.Logf("两次重建结构一致: %+v；%d 个查询结果（含统计）逐一相同", st1, len(r1))
}

// queryConsistent 是测试专用的快照一致性检查：在同一个读锁周期内，
// 四叉树结果必须与朴素扫描完全一致。可与写操作并发调用。
func (idx *Index) queryConsistent(t *testing.T, r Rect) {
	t.Helper()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	tree := idx.queryLocked(r)
	naive := idx.naiveQueryLocked(r)
	if !reflect.DeepEqual(tree.IDs, naive) {
		t.Fatalf("snapshot inconsistency: tree=%v naive=%v rect=%v", tree.IDs, naive, r)
	}
}

// TestConcurrentReadWrite 并发插入/删除/查询：无数据竞争，
// 每次查询都等于某一时刻点集上的朴素扫描，最终状态一致。
func TestConcurrentReadWrite(t *testing.T) {
	idx := mustNew(t, 0, 0, 256, 32, testLogger(slog.LevelError))
	const writers = 4
	const readers = 8
	const rounds = 300

	pre := make([][]Point, writers)
	for w := 0; w < writers; w++ {
		for i := 0; i < 50; i++ {
			pre[w] = append(pre[w], Point{
				ID: fmt.Sprintf("w%d-p%04d", w, i),
				X:  int64((w*53 + i*7) % 256),
				Y:  int64((w*31 + i*11) % 256)})
		}
		if err := idx.Insert(pre[w]); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(100 + w)))
			for round := 0; round < rounds; round++ {
				// 插入该线程专属的新编号点，再删除同编号（插入前先删保证可重复）。
				pid := fmt.Sprintf("w%d-new%04d", w, round)
				p := Point{ID: pid,
					X: rng.Int63n(256), Y: rng.Int63n(256)}
				if err := idx.Insert([]Point{p}); err != nil {
					t.Error(err)
					return
				}
				if rng.Intn(2) == 0 {
					if err := idx.Delete([]string{pid}); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(reader int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(900 + reader)))
			for k := 0; k < rounds; k++ {
				w := int64(8 + rng.Intn(64))
				x0 := rng.Int63n(256 - w)
				y0 := rng.Int63n(256 - w)
				idx.queryConsistent(t, Rect{x0, y0, x0 + w, y0 + w})
			}
		}(r)
	}
	wg.Wait()

	// 最终状态与独立朴素全量扫描一致。
	final, err := idx.Query(context.Background(), Rect{0, 0, 256, 256})
	if err != nil {
		t.Fatal(err)
	}
	all := idx.naiveQuery(Rect{0, 0, 256, 256})
	if !reflect.DeepEqual(final.IDs, all) {
		t.Fatalf("final state mismatch: %d vs %d", len(final.IDs), len(all))
	}
	t.Logf("并发结束: 存活点=%d 节点=%d 叶子=%d 溢出格=%d，所有读快照与朴素扫描一致",
		idx.Stats().PointCount, idx.Stats().NodeCount, idx.Stats().LeafCount, idx.Stats().OverflowCells)
}
