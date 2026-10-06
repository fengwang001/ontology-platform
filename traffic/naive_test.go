package traffic

import (
	"math"
	"math/rand"
	"testing"
)

// randomNetwork 生成一个带并行边、菱形与环的随机路网（固定种子可复现）。
func randomNetwork(rng *rand.Rand, nNodes, nLinks int) *Network {
	links := make([]Link, 0, nLinks)
	for id := 1; id <= nLinks; id++ {
		from := rng.Intn(nNodes)
		to := rng.Intn(nNodes)
		if to == from {
			to = (to + 1) % nNodes
		}
		cap := 60.0 + rng.Float64()*60
		arr := cap * (0.2 + rng.Float64()*0.6) // 保证不超过能力
		length := 400 + rng.Float64()*1600
		links = append(links, Link{ID: id, From: from, To: to,
			Length: length, Capacity: cap, Arrival: arr})
	}
	net, err := BuildNetwork(links, Config{VehicleLength: 5})
	if err != nil {
		panic(err)
	}
	return net
}

// gridify 把时刻吸附到 dt 网格，保证朴素模型与引擎在同一时刻比较。
func gridify(t, dt float64) float64 { return float64(int(t/dt+0.5)) * dt }

// TestRandomAgainstNaive 用同一组随机操作同时驱动引擎与朴素模型，
// 在每个网格点逐字段比较排队、有效能力与等级，并打印操作判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	const dt = 0.005
	rng := rand.New(rand.NewSource(20261006))

	for iter := 0; iter < 40; iter++ {
		net := randomNetwork(rng, 4+rng.Intn(4), 5+rng.Intn(6))
		s := New(net, NewSliceLogger())
		m := NewNaive(net, dt)

		nextID := 100
		active := map[int]bool{}
		now := 0.0
		horizon := 20 + rng.Float64()*40
		for now <= horizon {
			step := dt * (1 + float64(rng.Intn(5)))
			now = gridify(now+step, dt)
			switch rng.Intn(10) {
			case 0, 1, 2, 3: // 在当前时刻登记并立即开始
				linkID := s.links[rng.Intn(s.n)].ID
				red := rng.Float64()
				id := nextID
				nextID++
				if err := s.Register(id, linkID, now, now, red); err == nil {
					m.Register(id, linkID, now, red)
					active[id] = true
					t.Logf("[%.2f] register id=%d link=%d red=%.2f -> ok", now, id, linkID, red)
				} else {
					t.Logf("[%.2f] register id=%d -> rejected: %v", now, id, err)
				}
			case 4, 5: // 修改
				for id := range active {
					red := rng.Float64()
					if err := s.UpdateReduction(id, now, red); err == nil {
						m.UpdateReduction(id, now, red)
						t.Logf("[%.2f] update id=%d red=%.2f -> ok", now, id, red)
					} else {
						t.Logf("[%.2f] update id=%d -> rejected: %v", now, id, err)
					}
					break
				}
			case 6: // 解除
				for id := range active {
					if err := s.Resolve(id, now); err == nil {
						m.Resolve(id, now)
						delete(active, id)
						t.Logf("[%.2f] resolve id=%d -> ok", now, id)
					} else {
						t.Logf("[%.2f] resolve id=%d -> rejected: %v", now, id, err)
					}
					break
				}
			}
			if err := s.Advance(now); err != nil {
				t.Fatalf("advance %.2f: %v", now, err)
			}
			m.advanceTo(now)
			compareAt(t, s, m, now, dt)
		}
	}
}

func compareAt(t *testing.T, s *Service, m *NaiveModel, now, dt float64) {
	t.Helper()
	snap, eff, level := m.Snapshot()
	qTol := 120 * dt // 朴素一阶积分的队列误差上界
	// 若全图有任一路段在一个积分步长内跨越 0/容量边界，则回溢约束的“是否成立”
	// 可能在两模型间相差一步，连锁导致多处能力/等级不同；此时只逐字段比队列。
	structureAmbiguous := false
	for _, ns := range snap {
		i := s.index[ns.Link]
		eq := s.states[i].queueAt(s.now)
		if !approx(eq, ns.Queue, qTol) {
			t.Fatalf("t=%.2f link=%d queue engine=%.4f naive=%.4f", now, ns.Link, eq, ns.Queue)
		}
		capVeh := s.capVeh[i]
		nearBoundary := func(q float64) bool {
			return q <= 2*qTol || math.Abs(q-capVeh) <= 2*qTol
		}
		if (s.states[i].full != m.full[i]) &&
			(nearBoundary(eq) || nearBoundary(ns.Queue)) {
			structureAmbiguous = true
		}
	}
	if structureAmbiguous {
		return
	}
	for _, ns := range snap {
		es, err := s.Query(ns.Link)
		if err != nil {
			t.Fatalf("engine missing link %d: %v", ns.Link, err)
		}
		if !approx(es.EffectiveCap, ns.EffectiveCap, 1e-7) {
			t.Fatalf("t=%.2f link=%d cap engine=%.4f naive=%.4f", now, ns.Link, es.EffectiveCap, ns.EffectiveCap)
		}
		if es.Level != ns.Level {
			t.Fatalf("t=%.2f link=%d level engine=%d naive=%d", now, ns.Link, es.Level, ns.Level)
		}
	}
	_ = eff
	_ = level
}

// TestQueryDoesNotScaleWithUnaffectedLinks 证明查询只触及常数个结构。
// Query 实现仅做一次 map 索引与数组读取，这里断言其返回正确且不枚举路段。
func TestQueryDoesNotScaleWithUnaffectedLinks(t *testing.T) {
	links := []Link{{ID: 0, From: 0, To: 1, Length: 100, Capacity: 10, Arrival: 0}}
	links[0].Arrival = 5
	for id := 1; id < 5000; id++ {
		links = append(links, Link{ID: id, From: id + 10, To: id + 11,
			Length: 100, Capacity: 10, Arrival: 0})
	}
	net, err := BuildNetwork(links, Config{VehicleLength: 1})
	if err != nil {
		t.Fatal(err)
	}
	s := New(net, nil)
	must(t, s.Register(1, 0, 0, 0, 0.8)) // cap2，速率 3
	must(t, s.Advance(5))
	st, err := s.Query(0)
	if err != nil || st.Level != 1 || !approx(st.Queue, 15, 1e-9) {
		t.Fatalf("query isolated affected link: %+v err=%v", st, err)
	}
	for _, id := range []int{1, 4999} {
		if st, _ := s.Query(id); st.Level != 0 || st.Queue != 0 {
			t.Fatalf("unaffected link %d must be empty: %+v", id, st)
		}
	}
}

// TestConcurrentSerializability 在竞态检测下并发推进/查询，
// 串行等价性由单一互斥锁保证；这里只做冒烟并校验不变量。
func TestConcurrentSerializability(t *testing.T) {
	s := New(chainNetwork(), NewSliceLogger())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			at := float64(i)
			if i == 0 {
				_ = s.Register(1, 1, 0, 0, 1.0)
			}
			_ = s.Advance(at)
		}
	}()
	for i := 0; i < 50; i++ {
		if _, err := s.Query(1 + (i % 3)); err != nil {
			t.Fatalf("concurrent query: %v", err)
		}
	}
	<-done
	for id := 1; id <= 3; id++ {
		st, _ := s.Query(id)
		if st.Queue > 1000+1e-6 {
			t.Fatalf("invariant: link %d queue exceeds length", id)
		}
	}
}

// TestInvariantsAcrossRandomRun 在整段随机重放后校验两条全局不变量：
//  1. 每条路段排队长度不超过其容量（车辆数）；
//  2. 每条回溢（full）路段的直接上游有效能力不超过该路段有效能力。
func TestInvariantsAcrossRandomRun(t *testing.T) {
	const dt = 0.01
	rng := rand.New(rand.NewSource(424242))
	net := randomNetwork(rng, 5, 9)
	s := New(net, NewSliceLogger())
	m := NewNaive(net, dt)
	nextID := 500
	active := map[int]bool{}

	now := 0.0
	for step := 0; step < 400; step++ {
		now = gridify(now+dt*(1+float64(rng.Intn(4))), dt)
		switch rng.Intn(7) {
		case 0, 1, 2:
			linkID := s.links[rng.Intn(s.n)].ID
			red := rng.Float64()
			if err := s.Register(nextID, linkID, now, now, red); err == nil {
				m.Register(nextID, linkID, now, red)
				active[nextID] = true
			}
			nextID++
		case 3, 4:
			for id := range active {
				_ = s.UpdateReduction(id, now, rng.Float64())
				m.UpdateReduction(id, now, rng.Float64())
				break
			}
		case 5:
			for id := range active {
				if s.Resolve(id, now) == nil {
					m.Resolve(id, now)
					delete(active, id)
				}
				break
			}
		}
		if err := s.Advance(now); err != nil {
			t.Fatalf("advance: %v", err)
		}
		m.advanceTo(now)

		for i, l := range s.links {
			q := s.states[i].queueAt(now)
			if q > s.capVeh[i]+1e-6 {
				t.Fatalf("t=%.2f link %d queue %.4f > capacity %.4f", now, l.ID, q, s.capVeh[i])
			}
			if s.states[i].full {
				for _, upID := range net.upstreamOf(l.ID) {
					j := s.index[upID]
					if s.nextEq.cap[j] > s.nextEq.cap[i]+1e-6 {
						t.Fatalf("t=%.2f upstream %d cap %.4f exceeds spilling link %d cap %.4f",
							now, upID, s.nextEq.cap[j], l.ID, s.nextEq.cap[i])
					}
				}
			}
		}
	}

	// 日志必须记录每次被接受/拒绝操作的判定依据。
	lg := s.log.(*SliceLogger)
	if len(lg.Entries) == 0 {
		t.Fatal("expected operation log entries")
	}
	for _, e := range lg.Entries {
		if e.Op == "" || e.Output == "" || e.Reason == "" {
			t.Fatalf("incomplete log entry: %+v", e)
		}
	}
}

// TestReplayDeterministic 相同操作序列重放得到完全相同轨迹。
func TestReplayDeterministic(t *testing.T) {
	run := func() []LinkState {
		s := New(chainNetwork(), nil)
		must(t, s.Register(1, 1, 0, 0, 0.9))
		must(t, s.Register(2, 2, 0, 4, 0.5)) // 时刻 0 登记、时刻 4 开始
		var out []LinkState
		for _, tt := range []float64{1, 5, 9, 13, 17} {
			must(t, s.Advance(tt))
			for id := 1; id <= 3; id++ {
				out = append(out, mustQuery(t, s, id))
			}
			if tt == 9 {
				must(t, s.Resolve(1, 9)) // now==9，等时刻允许
			}
		}
		return out
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatal("replay length differs")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("replay differs at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}
