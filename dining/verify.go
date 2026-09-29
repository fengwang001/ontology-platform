package dining

import (
	"fmt"
	"sort"
)

// Violation 描述一条被破坏的不变量及判定依据。
type Violation struct {
	Name   string
	Detail string
}

func (v Violation) String() string { return v.Name + ": " + v.Detail }

// EdgeView 是一条边的可观察快照（测试与判定日志使用）。
type EdgeView struct {
	A, B     int
	ForkAt   int  // -1 表示在途
	Dirty    bool // 仅在叉被持有时有意义
	TokenAt  int  // -1 表示在途
	Deferred [2]bool
}

// Snapshot 返回全部进程状态与边状态的一致快照。
func (c *Coordinator) Snapshot() (map[int]State, map[[2]int]EdgeView) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pv := make(map[int]State, len(c.procs))
	for p, s := range c.procs {
		pv[p] = s
	}
	ev := make(map[[2]int]EdgeView, len(c.edges))
	for k, e := range c.edges {
		ev[k] = EdgeView{
			A:        e.a,
			B:        e.b,
			ForkAt:   e.forkAt,
			Dirty:    e.dirty,
			TokenAt:  e.tokenAt,
			Deferred: [2]bool{e.deferA, e.deferB},
		}
	}
	return pv, ev
}

// Pending 返回网络中待注入投递的快照。
func (c *Coordinator) Pending() []Delivery {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.net.Pending()
}

// CheckInvariants 在当前快照上校验全部安全性质：
//  1. 每条边的叉与令牌各恰好一份（持有或在途）；
//  2. 相邻进程从不同时进餐；
//  3. 由叉的位置与脏净导出的优先关系始终无环；
//  4. 思考者持脏叉不得暂存请求（规则一致性）。
func (c *Coordinator) CheckInvariants() []Violation {
	c.mu.Lock()
	defer c.mu.Unlock()

	var violations []Violation

	pending := c.net.Pending()
	inFlightFork := map[[2]int]int{}
	inFlightToken := map[[2]int]int{}
	for _, d := range pending {
		key := d.Msg.Edge
		switch d.Msg.Type {
		case Fork:
			inFlightFork[key]++
		case Request:
			inFlightToken[key]++
		}
	}

	keys := make([][2]int, 0, len(c.edges))
	for k := range c.edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})

	// pred[u][v] 表示 u 优先于 v（u 在该资源上先吃）。
	pred := map[int]map[int]bool{}
	addPred := func(u, v int) {
		if pred[u] == nil {
			pred[u] = map[int]bool{}
		}
		pred[u][v] = true
	}

	for _, k := range keys {
		e := c.edges[k]

		// 不变量 1：叉恰好一份。
		forkCount := inFlightFork[k]
		if e.forkAt != -1 {
			forkCount++
		}
		if forkCount != 1 {
			violations = append(violations, Violation{
				Name: "fork-uniqueness",
				Detail: fmt.Sprintf("edge%v has %d forks (want exactly 1)",
					k, forkCount),
			})
		}
		// 不变量 1：令牌恰好一份。
		tokenCount := inFlightToken[k]
		if e.tokenAt != -1 {
			tokenCount++
		}
		if tokenCount != 1 {
			violations = append(violations, Violation{
				Name: "token-uniqueness",
				Detail: fmt.Sprintf("edge%v has %d tokens (want exactly 1)",
					k, tokenCount),
			})
		}

		// 不变量 2：相邻进程从不同时进餐。
		if c.procs[e.a] == Eating && c.procs[e.b] == Eating {
			violations = append(violations, Violation{
				Name: "mutual-exclusion",
				Detail: fmt.Sprintf("neighbors %d and %d are both eating",
					e.a, e.b),
			})
		}

		// 不变量 3：由叉的位置与脏净导出优先关系（箭头 u->v 表示 u 优先于 v）。
		// Chandy–Misra 标准定向：
		//   - 脏叉：持有者必须应请求立即洗净让出，故请求方（对端）优先，
		//     箭头 对端 -> 持有者；
		//   - 净叉被持有：持有者优先（持有者在再次让出前可先吃），
		//     箭头 持有者 -> 对端；
		//   - 净叉在途：接收方一经交付即成为优先持有者，
		//     箭头 接收端 -> 发送端。
		// 初始时叉为脏且全在小端，所有箭头 大端 -> 小端，按编号单调故
		// 初始无环；脏叉每次应请求被洗净让出时，该边箭头恰好反向一次
		//（大端->小端 变为 小端->大端），且只发生在持有者自愿让出时，
		// CM 证明该翻转序列不可能使全图成环。
		switch {
		case e.forkAt == -1:
			// 净叉在途：接收端 -> 发送端。
			for _, d := range pending {
				if d.Msg.Edge == k && d.Msg.Type == Fork {
					addPred(d.To, d.From)
				}
			}
		case e.dirty:
			addPred(other(e, e.forkAt), e.forkAt)
		default:
			addPred(e.forkAt, other(e, e.forkAt))
		}

		// 不变量 4：思考者持脏叉必须立刻让出，不允许暂存。
		for _, p := range [2]int{e.a, e.b} {
			if e.deferred(p) && c.procs[p] == Thinking &&
				holdsFork(e, p) && e.dirty {
				violations = append(violations, Violation{
					Name: "defer-consistency",
					Detail: fmt.Sprintf(
						"edge%v: thinking dirty-fork holder %d must not defer",
						k, p),
				})
			}
		}
	}

	// Kahn 拓扑排序判定优先图无环。
	indeg := map[int]int{}
	nodes := map[int]bool{}
	for u, vs := range pred {
		nodes[u] = true
		for v := range vs {
			nodes[v] = true
			indeg[v]++
		}
	}
	var queue []int
	for n := range nodes {
		if indeg[n] == 0 {
			queue = append(queue, n)
		}
	}
	visited := 0
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		visited++
		for v := range pred[u] {
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	if visited != len(nodes) {
		violations = append(violations, Violation{
			Name: "acyclic-precedence",
			Detail: fmt.Sprintf(
				"precedence graph has a cycle (%d/%d topo-sorted)",
				visited, len(nodes)),
		})
	}
	return violations
}
