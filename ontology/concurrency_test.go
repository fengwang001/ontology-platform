package ontology

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// buildRandomGraph 构造一个确定性随机图，返回对象与链接 ID 列表。
func buildRandomGraph(t *testing.T, rng *rand.Rand, s *Store, n int) ([]ObjectID, []LinkID) {
	t.Helper()
	objs := make([]ObjectID, n)
	for i := range objs {
		objs[i] = ObjectID(fmt.Sprintf("o%03d", i))
		mustAddObject(t, s, objs[i])
	}
	var links []LinkID
	for i := 0; i < n*2; i++ {
		id := LinkID(fmt.Sprintf("l%04d", i))
		l := Link{
			ID:   id,
			Type: []string{"t0", "t1"}[rng.Intn(2)],
			From: objs[rng.Intn(n)],
			To:   objs[rng.Intn(n)],
		}
		if err := s.AddLink(l); err == nil {
			links = append(links, id)
		}
	}
	return objs, links
}

func randomSpec(rng *rand.Rand, objs []ObjectID) TraversalSpec {
	spec := TraversalSpec{Start: objs[rng.Intn(len(objs))]}
	for i, nh := 0, 1+rng.Intn(3); i < nh; i++ {
		spec.Hops = append(spec.Hops, HopSpec{
			LinkType: []string{"", "t0", "t1"}[rng.Intn(3)],
			Dir:      Direction(rng.Intn(3)),
			Limit:    []int{0, 1, 2, 3, 5}[rng.Intn(5)],
		})
	}
	return spec
}

// TestRandomDifferentialAgainstNaive 在大量随机场景上，将分页服务（页间
// 交织随机图修改）的输出与“确定快照上一次性遍历后手工切页”的朴素
// 实现对照，二者须完全一致。
func TestRandomDifferentialAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			s := NewStore()
			objs, links := buildRandomGraph(t, rng, s, 20+rng.Intn(40))
			spec := randomSpec(rng, objs)
			mode := Mode(rng.Intn(2))
			svc, _ := newTestService(s)

			aliveObjs := append([]ObjectID{}, objs...)
			aliveLinks := append([]LinkID{}, links...)
			mutCounter := 0
			between := func(page int) {
				for k := rng.Intn(4); k > 0; k-- {
					switch rng.Intn(3) {
					case 0: // 随机删除对象（连带其链接）
						if len(aliveObjs) > 0 {
							i := rng.Intn(len(aliveObjs))
							if err := s.DeleteObject(aliveObjs[i]); err == nil {
								aliveObjs = append(aliveObjs[:i], aliveObjs[i+1:]...)
							}
						}
					case 1: // 随机删除链接
						if len(aliveLinks) > 0 {
							i := rng.Intn(len(aliveLinks))
							if err := s.DeleteLink(aliveLinks[i]); err == nil {
								aliveLinks = append(aliveLinks[:i], aliveLinks[i+1:]...)
							}
						}
					case 2: // 随机新增对象与链接
						mutCounter++
						id := ObjectID(fmt.Sprintf("n%04d", mutCounter))
						if err := s.AddObject(Object{ID: id, Type: "new"}); err == nil {
							aliveObjs = append(aliveObjs, id)
							if len(aliveObjs) > 1 {
								lid := LinkID(fmt.Sprintf("m%04d", mutCounter))
								l := Link{ID: lid, Type: "t0",
									From: aliveObjs[rng.Intn(len(aliveObjs))], To: id}
								if err := s.AddLink(l); err == nil {
									aliveLinks = append(aliveLinks, lid)
								}
							}
						}
					}
				}
			}
			got, gotMarkers, snap, release := runTraversal(t, svc, spec, mode,
				func(int) int { return 1 + rng.Intn(9) }, between)
			want, wantMarkers := naiveTraverse(s, snap, spec, mode)
			release()
			assertSameIDs(t, want, got)
			assertSameMarkers(t, wantMarkers, gotMarkers)
		})
	}
}

// TestConcurrentTraversalsAndMutations 并发分页请求（多个遍历共享同一
// 服务）与并发图修改交织执行，每个遍历的输出仍须等价于其确定快照上
// 的一次性遍历结果。
func TestConcurrentTraversalsAndMutations(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := NewStore()
	objs, links := buildRandomGraph(t, rng, s, 40)
	svc, logger := newTestService(s)

	const T = 8
	specs := make([]TraversalSpec, T)
	modes := make([]Mode, T)
	cursors := make([]string, T)
	snaps := make([]uint64, T)
	releases := make([]func(), T)
	objects := make([][]ObjectID, T)
	markers := make([][]TruncationMarker, T)

	// 各遍历的首页（在修改开始前发出，保证起始对象存在），并钉住各自快照。
	for i := 0; i < T; i++ {
		specs[i] = randomSpec(rng, objs)
		modes[i] = Mode(rng.Intn(2))
		logsBefore := len(logger.all())
		resp, err := svc.Page(PageRequest{Spec: specs[i], BatchSize: 2 + i, Mode: modes[i]})
		if err != nil {
			t.Fatalf("traversal %d first page: %v", i, err)
		}
		logs := logger.all()
		snaps[i] = logs[logsBefore].Snapshot
		releases[i] = s.Pin(snaps[i])
		defer releases[i]()
		objects[i] = append(objects[i], resp.Objects...)
		markers[i] = append(markers[i], resp.Markers...)
		cursors[i] = resp.Cursor
	}

	// 并发修改：两个工作者持续增删对象与链接。
	var mu sync.Mutex
	aliveObjs := append([]ObjectID{}, objs...)
	aliveLinks := append([]LinkID{}, links...)
	stop := make(chan struct{})
	var mutWg sync.WaitGroup
	for w := 0; w < 2; w++ {
		mutWg.Add(1)
		go func(w int) {
			defer mutWg.Done()
			mr := rand.New(rand.NewSource(int64(1000 + w)))
			for c := 0; ; c++ {
				select {
				case <-stop:
					return
				default:
				}
				mu.Lock()
				switch mr.Intn(3) {
				case 0:
					if len(aliveObjs) > 0 {
						i := mr.Intn(len(aliveObjs))
						if err := s.DeleteObject(aliveObjs[i]); err == nil {
							aliveObjs = append(aliveObjs[:i], aliveObjs[i+1:]...)
						}
					}
				case 1:
					if len(aliveLinks) > 0 {
						i := mr.Intn(len(aliveLinks))
						if err := s.DeleteLink(aliveLinks[i]); err == nil {
							aliveLinks = append(aliveLinks[:i], aliveLinks[i+1:]...)
						}
					}
				case 2:
					id := ObjectID(fmt.Sprintf("c%d_%04d", w, c))
					if err := s.AddObject(Object{ID: id, Type: "c"}); err == nil {
						aliveObjs = append(aliveObjs, id)
						if len(aliveObjs) > 1 {
							lid := LinkID(fmt.Sprintf("cl%d_%04d", w, c))
							l := Link{ID: lid, Type: "t1",
								From: aliveObjs[mr.Intn(len(aliveObjs))], To: id}
							if err := s.AddLink(l); err == nil {
								aliveLinks = append(aliveLinks, lid)
							}
						}
					}
				}
				mu.Unlock()
			}
		}(w)
	}

	// 并发翻页：每个遍历一个 goroutine，批次大小逐页随机。
	var wg sync.WaitGroup
	errs := make([]error, T)
	for i := 0; i < T; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pr := rand.New(rand.NewSource(int64(2000 + i)))
			cursor := cursors[i]
			for cursor != "" {
				resp, err := svc.Page(PageRequest{
					Cursor: cursor, BatchSize: 1 + pr.Intn(6), Mode: modes[i],
				})
				if err != nil {
					errs[i] = err
					return
				}
				objects[i] = append(objects[i], resp.Objects...)
				markers[i] = append(markers[i], resp.Markers...)
				cursor = resp.Cursor
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	mutWg.Wait()

	for i := 0; i < T; i++ {
		if errs[i] != nil {
			t.Fatalf("traversal %d failed: %v", i, errs[i])
		}
		want, wantMarkers := naiveTraverse(s, snaps[i], specs[i], modes[i])
		assertSameIDs(t, want, objects[i])
		assertSameMarkers(t, wantMarkers, markers[i])
	}
}

// TestDuplicateCursorConcurrent 针对同一遍历同一位置的并发重复请求，
// 恰好一个成功，其余须以 ErrInvalidCursor 被拒。
func TestDuplicateCursorConcurrent(t *testing.T) {
	s := NewStore()
	buildFanGraph(t, s, 6)
	svc, _ := newTestService(s)
	spec := TraversalSpec{Start: "s", Hops: []HopSpec{{Dir: DirOut}, {Dir: DirOut}}}

	r1, err := svc.Page(PageRequest{Spec: spec, BatchSize: 2, Mode: ModeSilent})
	if err != nil {
		t.Fatal(err)
	}
	const K = 4
	var wg sync.WaitGroup
	resps := make([]PageResponse, K)
	errs := make([]error, K)
	for k := 0; k < K; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			resps[k], errs[k] = svc.Page(PageRequest{Cursor: r1.Cursor, BatchSize: 2, Mode: ModeSilent})
		}(k)
	}
	wg.Wait()
	wins, winner := 0, -1
	for k := 0; k < K; k++ {
		if errs[k] == nil {
			wins++
			winner = k
		} else {
			expectErrKind(t, errs[k], ErrInvalidCursor)
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly 1 winner among %d duplicate requests, got %d", K, wins)
	}
	// 胜出游标可继续完成遍历，且整体结果与朴素参照一致。
	all := append([]ObjectID{}, r1.Objects...)
	all = append(all, resps[winner].Objects...)
	cursor := resps[winner].Cursor
	for cursor != "" {
		resp, err := svc.Page(PageRequest{Cursor: cursor, BatchSize: 2, Mode: ModeSilent})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, resp.Objects...)
		cursor = resp.Cursor
	}
	want, _ := naiveTraverse(s, s.Snapshot(), spec, ModeSilent)
	assertSameIDs(t, want, all)
}
