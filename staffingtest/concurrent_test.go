package staffingtest

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/staffing"
)

// TestConcurrentNoOvercommit：并发风暴下不可能超占用。
// 大量 goroutine 同时对同一岗位发放/答复/查询，结束后逐岗位核对守恒。
func TestConcurrentNoOvercommit(t *testing.T) {
	s := staffing.New(3, 3)
	const pos = "P"
	if err := s.AddPosition(0, staffing.PositionSpec{
		ID: pos, BandLow: 100, BandHigh: 1000, Headcount: 20,
	}); err != nil {
		t.Fatal(err)
	}
	const n = 200
	for i := 0; i < n; i++ {
		if err := s.AddCandidate(0, fmt.Sprintf("C%04d", i)); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for k := 0; k < 400; k++ {
				now := rng.Intn(60)
				c := fmt.Sprintf("C%04d", rng.Intn(n))
				id, err := s.IssueOffer(now, c, pos, 100+rng.Intn(900), now+5)
				switch staffing.ErrCode(err) {
				case staffing.CodeOK:
					// 接受/拒绝/撤回/入职/查询 任意交错
					switch rng.Intn(4) {
					case 0:
						_ = s.Respond(now, id, false, -1)
					case 1:
						_ = s.Respond(now, id, true, now)
						_ = s.Onboard(now, id)
					case 2:
						_ = s.Withdraw(now, id)
					default:
						_, _ = s.GetOffer(now, id)
					}
				case staffing.CodeClockRollback:
					// 预期可能：并发交错推进了时钟
				}
			}
		}(int64(w + 1))
	}
	wg.Wait()

	snap, err := s.Snapshot(59)
	if err != nil {
		t.Fatal(err)
	}
	if err := staffing.CheckInvariant(snap); err != nil {
		t.Fatalf("invariant after concurrent storm: %v", err)
	}
	if occ := snap.Occupied[pos]; occ > 20 {
		t.Fatalf("overcommit: occupied=%d", occ)
	}
}

// TestConcurrentEquivalentSerial：同一批带时间戳的操作，
// 串行执行的（结果多重集, 最终状态）与并发执行一致——
// 因为所有方法都在同一把锁下原子执行，并发只是合法串行顺序的重排。
func TestConcurrentEquivalentSerial(t *testing.T) {
	build := func() (*staffing.Service, []func(*staffing.Service) (staffing.Code, int)) {
		s := staffing.New(2, 2)
		_ = s.AddPosition(0, staffing.PositionSpec{ID: "P", BandLow: 100, BandHigh: 200, Headcount: 6})
		const n = 12
		for i := 0; i < n; i++ {
			_ = s.AddCandidate(0, fmt.Sprintf("C%d", i))
		}
		var ops []func(*staffing.Service) (staffing.Code, int)
		// 所有操作使用同一个 now=5，锁下串行化，顺序不改变成功集合。
		for i := 0; i < n; i++ {
			c := fmt.Sprintf("C%d", i)
			ops = append(ops, func(s *staffing.Service) (staffing.Code, int) {
				id, err := s.IssueOffer(5, c, "P", 150, 20)
				if err != nil {
					return staffing.ErrCode(err), -1
				}
				return staffing.CodeOK, int(id)
			})
		}
		return s, ops
	}

	// 串行：统计结果
	s1, ops := build()
	serialCodes := map[staffing.Code]int{}
	var serialIDs []int
	for _, f := range ops {
		code, id := f(s1)
		serialCodes[code]++
		if code == staffing.CodeOK {
			serialIDs = append(serialIDs, id)
		}
	}

	// 并发
	s2, ops2 := build()
	var mu sync.Mutex
	parCodes := map[staffing.Code]int{}
	var parIDs []int
	var wg sync.WaitGroup
	for _, f := range ops2 {
		wg.Add(1)
		go func(fn func(*staffing.Service) (staffing.Code, int)) {
			defer wg.Done()
			code, id := fn(s2)
			mu.Lock()
			parCodes[code]++
			if code == staffing.CodeOK {
				parIDs = append(parIDs, id)
			}
			mu.Unlock()
		}(f)
	}
	wg.Wait()

	if len(serialCodes) != len(parCodes) {
		t.Fatalf("code multiset differs: serial=%v parallel=%v", serialCodes, parCodes)
	}
	for c, n1 := range serialCodes {
		if parCodes[c] != n1 {
			t.Fatalf("code %s count serial=%d parallel=%d", c, n1, parCodes[c])
		}
	}
	if len(serialIDs) != len(parIDs) {
		t.Fatalf("success count serial=%d parallel=%d", len(serialIDs), len(parIDs))
	}

	snap1, _ := s1.Snapshot(5)
	snap2, _ := s2.Snapshot(5)
	v1, v2 := ServiceView(snap1), ServiceView(snap2)
	// 占用/在岗/未决与各候选人的通知持有关系应一致（ID 分配顺序可能不同，
	// 故只比较聚合计数）。
	if v1.Occupied["P"] != v2.Occupied["P"] ||
		v1.Onboarded["P"] != v2.Onboarded["P"] ||
		v1.Pending["P"] != v2.Pending["P"] {
		t.Fatalf("aggregate occupancy differs: serial=%+v parallel=%+v",
			v1.Occupied, v2.Occupied)
	}
	if err := staffing.CheckInvariant(snap2); err != nil {
		t.Fatal(err)
	}
}

// TestReplayDeterminism：同一操作序列重放两次，结果与状态完全相同。
func TestReplayDeterminism(t *testing.T) {
	run := func() (string, StateView) {
		_, ops := Generate(rand.New(rand.NewSource(424242)), 200)
		s := staffing.New(4, 2)
		var codes string
		for _, op := range ops {
			r := RunOp(s, op)
			if r.OK {
				codes += "."
			} else {
				codes += fmt.Sprintf("%d,", r.Code)
			}
		}
		snap, _ := s.Snapshot(1 << 30)
		return codes, ServiceView(snap)
	}

	codes1, view1 := run()
	codes2, view2 := run()
	if codes1 != codes2 {
		t.Fatal("replay produced different result sequence")
	}
	if !viewsComparable(view1, view2) {
		t.Fatal("replay produced different final state")
	}
}

func viewsComparable(a, b StateView) bool {
	if a.Now != b.Now || len(a.Offers) != len(b.Offers) {
		return false
	}
	for id, oa := range a.Offers {
		ob, ok := b.Offers[id]
		if !ok || oa != ob {
			return false
		}
	}
	for k, va := range a.Occupied {
		if b.Occupied[k] != va {
			return false
		}
	}
	for k, ea := range a.Exceptions {
		if b.Exceptions[k] != ea {
			return false
		}
	}
	return true
}
