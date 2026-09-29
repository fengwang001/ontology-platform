package dining

import (
	"math/rand"
	"testing"
)

// TestRandomInterleavingRing：5 环上 1000 次随机交错。
// 判定依据：逐步 CheckInvariants（叉/令牌唯一、相邻不同时进餐、优先无环）、
// 无饿死（结束时不存在请求已发出却仍饥饿的进程）、每个进程都至少进餐一次。
func TestRandomInterleavingRing(t *testing.T) {
	const runs, steps = 1000, 400
	procs, edges := ringTopology(5)
	totalEats := map[int]int{}
	for seed := int64(1); seed <= runs; seed++ {
		eats, maxOvertake, starved := runSimulation(t, procs, edges, seed, steps)
		if len(starved) != 0 {
			t.Fatalf("seed=%d starved=%v eatCount=%v", seed, starved, eats)
		}
		for p, n := range eats {
			totalEats[p] += n
		}
		if seed <= 3 {
			t.Logf("ring seed=%d 输入:(BecomeHungry/StartEating/FinishEating/Deliver 交错) "+
				"输出:eats=%v maxOvertake=%d 判定:无相邻同餐/优先无环/无饿死",
				seed, eats, maxOvertake)
		}
	}
	for _, p := range procs {
		if totalEats[p] == 0 {
			t.Fatalf("p%d never ate across %d runs", p, runs)
		}
	}
	t.Logf("ring 汇总: %d runs x %d steps, total eats=%v", runs, steps, totalEats)
}

// TestRandomInterleavingComplete：完全图 K4 上 1000 次随机交错。
func TestRandomInterleavingComplete(t *testing.T) {
	const runs, steps = 1000, 600
	procs, edges := completeTopology(4)
	totalEats := map[int]int{}
	for seed := int64(10000); seed < 10000+runs; seed++ {
		eats, _, starved := runSimulation(t, procs, edges, seed, steps)
		if len(starved) != 0 {
			t.Fatalf("seed=%d starved=%v", seed, starved)
		}
		for p, n := range eats {
			totalEats[p] += n
		}
	}
	for _, p := range procs {
		if totalEats[p] == 0 {
			t.Fatalf("p%d never ate across %d runs", p, runs)
		}
	}
	t.Logf("K4 汇总: %d runs x %d steps, total eats=%v (判定:任意两进程不同时进餐)",
		runs, steps, totalEats)
}

// TestBoundedOvertake 直接验证题设有界超越：
// 某进程请求送达持有者后，该邻居在它进餐前至多再进餐一次。
func TestBoundedOvertake(t *testing.T) {
	procs, edges := ringTopology(5)
	_, maxOvertake, starved := runSimulation(t, procs, edges, 42, 3000)
	if len(starved) != 0 {
		t.Fatalf("starved=%v", starved)
	}
	if maxOvertake > 1 {
		t.Fatalf("overtake bound violated: %d", maxOvertake)
	}
	t.Logf("bounded overtake: max neighbor meals while waiting = %d (<=1)",
		maxOvertake)
}

// recordedStep 是一步可重放的操作。
type recordedStep struct {
	kind string // "hungry" | "eat" | "finish" | "deliver"
	p    int
	d    Delivery
}

func recordRandomRun(t *testing.T, procs []int, edges [][2]int, seed int64,
	steps int) []recordedStep {
	net := NewQueueNetwork()
	c, err := NewCoordinator(procs, edges, net, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewSource(seed))
	var rec []recordedStep
	states, _ := c.Snapshot()
	for step := 0; step < steps; step++ {
		pending := c.Pending()
		var local []recordedStep
		for _, p := range procs {
			switch states[p] {
			case Thinking:
				local = append(local, recordedStep{kind: "hungry", p: p})
			case Eating:
				local = append(local, recordedStep{kind: "finish", p: p})
			case Hungry:
				if canEat(c, p) {
					local = append(local, recordedStep{kind: "eat", p: p})
				}
			}
		}
		if len(pending) > 0 && (len(local) == 0 || r.Intn(2) == 0) {
			d := pending[r.Intn(len(pending))]
			head, ok := net.Peek(d.From, d.To)
			if !ok {
				continue
			}
			st := recordedStep{kind: "deliver",
				d: Delivery{From: d.From, To: d.To, Msg: head}}
			rec = append(rec, st)
			if err := c.Deliver(st.d); err != nil {
				t.Fatal(err)
			}
		} else if len(local) > 0 {
			st := local[r.Intn(len(local))]
			rec = append(rec, st)
			var err error
			switch st.kind {
			case "hungry":
				err = c.BecomeHungry(st.p)
			case "eat":
				err = c.StartEating(st.p)
			case "finish":
				err = c.FinishEating(st.p)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		states, _ = c.Snapshot()
	}
	return rec
}

func replayRecorded(t *testing.T, procs []int, edges [][2]int,
	rec []recordedStep) []Event {
	logger := &SliceLogger{}
	c, err := NewCoordinator(procs, edges, NewQueueNetwork(), logger)
	if err != nil {
		t.Fatal(err)
	}
	for i, st := range rec {
		var err error
		switch st.kind {
		case "hungry":
			err = c.BecomeHungry(st.p)
		case "eat":
			err = c.StartEating(st.p)
		case "finish":
			err = c.FinishEating(st.p)
		case "deliver":
			err = c.Deliver(st.d)
		}
		if err != nil {
			t.Fatalf("replay step %d (%s): %v", i, st.kind, err)
		}
	}
	if v := c.CheckInvariants(); len(v) != 0 {
		t.Fatalf("replay invariants: %v", v)
	}
	return logger.Snapshot()
}

// TestReplayDeterminism：相同的操作与投递序列重放，判定事件必须逐字相同。
func TestReplayDeterminism(t *testing.T) {
	procs, edges := completeTopology(4)
	rec := recordRandomRun(t, procs, edges, 777, 500)
	first := replayRecorded(t, procs, edges, rec)
	second := replayRecorded(t, procs, edges, rec)
	if len(first) != len(second) {
		t.Fatalf("replay event count differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("event %d differs on replay:\n%+v\n%+v", i, first[i], second[i])
		}
	}
	t.Logf("replay determinism: %d recorded steps, %d events identical across replays",
		len(rec), len(first))
}
