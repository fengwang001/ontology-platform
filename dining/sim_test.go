package dining

import (
	"math/rand"
	"testing"
)

// rng 提供可重放的伪随机交错。
type simRNG struct{ r *rand.Rand }

// runSimulation 用固定种子在 topo 上随机交错操作与消息注入。
//
// 每步以加权随机方式选择：投递某信道队首消息、让思考者饥饿、让收齐叉的
// 饥饿者进餐、让进餐者结束。投递严格按网络 FIFO（只能投递队首），但不同
// 信道之间任意交错，符合题设网络模型。每一步都校验全部不变量。
func runSimulation(t *testing.T, procs []int, edges [][2]int, seed int64,
	steps int) (eatCount map[int]int, maxOvertake int, starved []int) {
	t.Helper()
	net := NewQueueNetwork()
	c, err := NewCoordinator(procs, edges, net, &SliceLogger{})
	if err != nil {
		t.Fatalf("seed=%d topology rejected: %v", seed, err)
	}
	r := &simRNG{r: rand.New(rand.NewSource(seed))}

	eatCount = map[int]int{}
	// overtake[[2]int{hungry,neighbor}]：自 hungry 的请求送达 neighbor 后，
	// neighbor 在 hungry 进餐前进餐的次数；每条边独立上界为 1。
	overtake := map[[2]int]int{}
	states, _ := c.Snapshot()

	for step := 0; step < steps; step++ {
		if v := c.CheckInvariants(); len(v) != 0 {
			t.Fatalf("seed=%d step=%d invariants broken: %v", seed, step, v)
		}
		pending := c.Pending()
		type localOp struct {
			kind string
			p    int
		}
		var ops []localOp
		for _, p := range procs {
			switch states[p] {
			case Thinking:
				ops = append(ops, localOp{"hungry", p})
			case Eating:
				ops = append(ops, localOp{"finish", p})
			case Hungry:
				if canEat(c, p) {
					ops = append(ops, localOp{"eat", p})
				}
			}
		}

		if len(pending) > 0 && (len(ops) == 0 || r.r.Intn(2) == 0) {
			d := pending[r.r.Intn(len(pending))]
			head, ok := net.Peek(d.From, d.To)
			if !ok {
				continue
			}
			if err := c.Deliver(Delivery{From: d.From, To: d.To, Msg: head}); err != nil {
				t.Fatalf("seed=%d deliver: %v", seed, err)
			}
		} else if len(ops) > 0 {
			choice := ops[r.r.Intn(len(ops))]
			switch choice.kind {
			case "hungry":
				if err := c.BecomeHungry(choice.p); err != nil {
					t.Fatalf("seed=%d hungry: %v", seed, err)
				}
				for k := range overtake {
					if k[0] == choice.p {
						delete(overtake, k)
					}
				}
			case "eat":
				if err := c.StartEating(choice.p); err != nil {
					t.Fatalf("seed=%d eat: %v", seed, err)
				}
				eatCount[choice.p]++
				// 超越计数：仅当饥饿邻居 q 的请求已经送达本进程
				//（本进程此时持有该边令牌），或净叉已在发往 q 的途中时，
				// 才计为“在 q 进餐前本进程又吃了一次”。
				for _, q := range neighbors(edges, choice.p) {
					if states[q] == Hungry && requestDeliveredTo(c, q, choice.p) {
						overtake[[2]int{q, choice.p}]++
					}
				}
			case "finish":
				if err := c.FinishEating(choice.p); err != nil {
					t.Fatalf("seed=%d finish: %v", seed, err)
				}
				for k := range overtake {
					if k[0] == choice.p {
						delete(overtake, k)
					}
				}
			}
		}

		for k, n := range overtake {
			if n > maxOvertake {
				maxOvertake = n
			}
			if n > 1 {
				t.Fatalf("seed=%d step=%d: neighbor p%d overtook hungry p%d %d times (>1)",
					seed, step, k[1], k[0], n)
			}
		}
		states, _ = c.Snapshot()
	}

	// 公平排空：随机交错结束后，确定性地把公平性补齐——投递全部在途消息
	//（每条信道仍按 FIFO），让每个收齐叉的饥饿者立即进餐并结束。
	// CM 协议下该过程必然终止：净叉只可能被饥饿者持有（思考者手中的叉
	// 恒脏，收到请求即洗净让出），而“请求送达后至多被超越一次”保证
	// 饥饿链不会无限延续。排空后仍饥饿即真正的死锁/饿死。
	starved = fairDrain(t, c, procs, seed)
	return eatCount, maxOvertake, starved
}

// fairDrain 投递全部消息并推进所有可推进的进餐，返回排空后仍饥饿的进程。
func fairDrain(t *testing.T, c *Coordinator, procs []int, seed int64) []int {
	t.Helper()
	const bound = 100000
	for iter := 0; iter < bound; iter++ {
		if v := c.CheckInvariants(); len(v) != 0 {
			t.Fatalf("seed=%d drain invariants: %v", seed, v)
		}
		progress := false
		// 1) 排空所有信道（各自 FIFO；不同信道间按进程编号顺序，保证确定性）。
		for {
			pending := c.Pending()
			if len(pending) == 0 {
				break
			}
			if err := c.Deliver(pending[0]); err != nil {
				t.Fatalf("seed=%d drain deliver: %v", seed, err)
			}
			progress = true
		}
		// 2) 按固定顺序推进本地状态：进餐者结束、可吃的饥饿者进餐。
		states, _ := c.Snapshot()
		for _, p := range procs {
			switch states[p] {
			case Eating:
				if err := c.FinishEating(p); err != nil {
					t.Fatalf("seed=%d drain finish: %v", seed, err)
				}
				progress = true
			case Hungry:
				if canEat(c, p) {
					if err := c.StartEating(p); err != nil {
						t.Fatalf("seed=%d drain eat: %v", seed, err)
					}
					progress = true
				}
			}
		}
		if !progress {
			break
		}
	}
	var left []int
	states, _ := c.Snapshot()
	for _, p := range procs {
		if states[p] == Hungry {
			left = append(left, p)
		}
	}
	return left
}

// canEat 判断饥饿进程是否已收齐邻边叉。
func canEat(c *Coordinator, p int) bool {
	_, ev := c.Snapshot()
	for k, e := range ev {
		if (k[0] == p || k[1] == p) && e.ForkAt != p {
			return false
		}
	}
	return true
}

// requestDeliveredTo 判断饥饿 p 针对邻居 h 的请求是否已送达 h：
// 即该边令牌当前在 h 手中（h 收到请求后令牌留在 h 处），或净叉已在
// 发往 p 的途中（h 已让出，不可能再“先吃”）。
func requestDeliveredTo(c *Coordinator, p, h int) bool {
	_, ev := c.Snapshot()
	for k, e := range ev {
		if k[0] != p && k[1] != p {
			continue
		}
		if e.TokenAt == h {
			return true
		}
	}
	return false
}

// requestIsOutstanding 判断饥饿 p 是否仍有未满足的邻边：
// 某邻边上叉不在手（无论令牌位置），即 p 尚不能进餐。
func requestIsOutstanding(c *Coordinator, p int) bool {
	_, ev := c.Snapshot()
	for k, e := range ev {
		if (k[0] == p || k[1] == p) && e.ForkAt != p {
			return true
		}
	}
	return false
}

func neighbors(edges [][2]int, p int) []int {
	var out []int
	for _, e := range edges {
		key, _ := canonEdge(e[0], e[1])
		if key[0] == p {
			out = append(out, key[1])
		} else if key[1] == p {
			out = append(out, key[0])
		}
	}
	return out
}

func ringTopology(n int) ([]int, [][2]int) {
	procs := make([]int, n)
	var edges [][2]int
	for i := 0; i < n; i++ {
		procs[i] = i
		edges = append(edges, [2]int{i, (i + 1) % n})
	}
	return procs, edges
}

func completeTopology(n int) ([]int, [][2]int) {
	procs := make([]int, n)
	var edges [][2]int
	for i := 0; i < n; i++ {
		procs[i] = i
		for j := i + 1; j < n; j++ {
			edges = append(edges, [2]int{i, j})
		}
	}
	return procs, edges
}
