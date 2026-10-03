package srp

import "fmt"

// naiveSim 是完全按题目规则另写的朴素参考实现，刻意不复用 srp 的任何代码：
// 层级每次现算、天花板每次现算、判定顺序逐字照抄题目优先级。
type naiveSim struct {
	res   map[string]int            // id -> N
	avail map[string]int            // id -> avail
	taskD map[string]int            // task -> D
	taskM map[string]map[string]int // task -> resource -> μ
	held  map[string]map[string]int // job -> resource -> held
	jobT  map[string]string         // job -> task
	stack []string                  // job ids，底在下
	short int
}

func newNaive() *naiveSim {
	return &naiveSim{
		res:   map[string]int{},
		avail: map[string]int{},
		taskD: map[string]int{},
		taskM: map[string]map[string]int{},
		held:  map[string]map[string]int{},
		jobT:  map[string]string{},
	}
}

type naiveResult struct {
	ok     bool
	reason Reason
	// 查询结果
	intVal int
	stackV []string
}

func (n *naiveSim) level(t string) int {
	distinct := map[int]bool{}
	for _, d := range n.taskD {
		if d > n.taskD[t] {
			distinct[d] = true
		}
	}
	return len(distinct) + 1
}

func (n *naiveSim) ceil(r string) int {
	c := 0
	for t := range n.taskD {
		mu := 0
		if m := n.taskM[t]; m != nil {
			mu = m[r]
		}
		if mu > n.avail[r] {
			if pi := n.level(t); pi > c {
				c = pi
			}
		}
	}
	return c
}

func (n *naiveSim) sysCeil() int {
	c := 0
	for r := range n.res {
		if v := n.ceil(r); v > c {
			c = v
		}
	}
	return c
}

func (n *naiveSim) reject(r Reason) naiveResult { return naiveResult{ok: false, reason: r} }

// op 是统一的一步操作，两种实现执行同一份序列。
type op struct {
	kind string
	a, b string
	n    int
	mus  []Mu
}

func (o op) String() string {
	switch o.kind {
	case "Declare":
		return fmt.Sprintf("DeclareResource(%q,%d)", o.a, o.n)
	case "Add":
		return fmt.Sprintf("AddTask(%q,D=%d,mus=%v)", o.a, o.n, o.mus)
	case "Remove":
		return fmt.Sprintf("RemoveTask(%q)", o.a)
	case "Start":
		return fmt.Sprintf("Start(%q,%q)", o.a, o.b)
	case "Acquire", "Release":
		return fmt.Sprintf("%s(%q,%q,%d)", o.kind, o.a, o.b, o.n)
	case "Finish":
		return fmt.Sprintf("Finish(%q)", o.a)
	case "QCeil":
		return fmt.Sprintf("Ceil(%q)", o.a)
	case "QAvail":
		return fmt.Sprintf("Avail(%q)", o.a)
	case "QLevel":
		return fmt.Sprintf("Level(%q)", o.a)
	case "QSys", "QStack":
		return o.kind + "()"
	}
	return o.kind
}

func (n *naiveSim) run(o op) naiveResult {
	switch o.kind {
	case "Declare":
		if o.a == "" || o.n < 1 || o.n > 1000 {
			return n.reject(ReasonInvalidArgument)
		}
		if _, ok := n.res[o.a]; ok {
			return n.reject(ReasonDuplicate)
		}
		if len(n.res) >= 8 {
			return n.reject(ReasonCapacityExceeded)
		}
		n.res[o.a] = o.n
		n.avail[o.a] = o.n
		return naiveResult{ok: true}
	case "Add":
		if o.a == "" || o.n < 1 || o.n > 1_000_000 || len(o.mus) > 4 {
			return n.reject(ReasonInvalidArgument)
		}
		seen := map[string]bool{}
		for _, m := range o.mus {
			if m.Resource == "" || m.Units < 1 || seen[m.Resource] {
				return n.reject(ReasonInvalidArgument)
			}
			seen[m.Resource] = true
		}
		if _, ok := n.taskD[o.a]; ok {
			return n.reject(ReasonDuplicate)
		}
		if len(n.taskD) >= 16 {
			return n.reject(ReasonCapacityExceeded)
		}
		for _, m := range o.mus {
			tot, ok := n.res[m.Resource]
			if !ok {
				return n.reject(ReasonNotFound)
			}
			if m.Units > tot {
				return n.reject(ReasonInvalidArgument)
			}
		}
		n.taskD[o.a] = o.n
		n.taskM[o.a] = map[string]int{}
		for _, m := range o.mus {
			n.taskM[o.a][m.Resource] = m.Units
		}
		return naiveResult{ok: true}
	case "Remove":
		if o.a == "" {
			return n.reject(ReasonInvalidArgument)
		}
		if _, ok := n.taskD[o.a]; !ok {
			return n.reject(ReasonNotFound)
		}
		for _, t := range n.jobT {
			if t == o.a {
				return n.reject(ReasonTaskInUse)
			}
		}
		delete(n.taskD, o.a)
		delete(n.taskM, o.a)
		return naiveResult{ok: true}
	case "Start":
		if o.a == "" || o.b == "" {
			return n.reject(ReasonInvalidArgument)
		}
		t, ok := n.taskD[o.b]
		_ = t
		if !ok {
			return n.reject(ReasonNotFound)
		}
		if _, ok := n.jobT[o.a]; ok {
			return n.reject(ReasonDuplicate)
		}
		if len(n.stack) >= 32 {
			return n.reject(ReasonCapacityExceeded)
		}
		pi := n.level(o.b)
		if len(n.stack) > 0 {
			topT := n.jobT[n.stack[len(n.stack)-1]]
			if pi <= n.level(topT) {
				return n.reject(ReasonPreemptionLevelLow)
			}
		}
		if pi <= n.sysCeil() {
			return n.reject(ReasonCeilingBlocked)
		}
		n.jobT[o.a] = o.b
		n.held[o.a] = map[string]int{}
		n.stack = append(n.stack, o.a)
		return naiveResult{ok: true}
	case "Acquire", "Release":
		if o.a == "" || o.b == "" || o.n < 1 || o.n > 1000 {
			return n.reject(ReasonInvalidArgument)
		}
		tid, ok := n.jobT[o.a]
		if !ok {
			return n.reject(ReasonNotFound)
		}
		if _, ok := n.res[o.b]; !ok {
			return n.reject(ReasonNotFound)
		}
		if n.stack[len(n.stack)-1] != o.a {
			return n.reject(ReasonNotTopOfStack)
		}
		if o.kind == "Acquire" {
			limit := n.taskM[tid][o.b]
			if n.held[o.a][o.b]+o.n > limit {
				return n.reject(ReasonOverClaim)
			}
			if o.n > n.avail[o.b] {
				n.short++
				return n.reject(ReasonUnitUnavailable)
			}
			n.held[o.a][o.b] += o.n
			n.avail[o.b] -= o.n
		} else {
			if o.n > n.held[o.a][o.b] {
				return n.reject(ReasonNotHeld)
			}
			n.held[o.a][o.b] -= o.n
			n.avail[o.b] += o.n
		}
		return naiveResult{ok: true}
	case "Finish":
		if o.a == "" {
			return n.reject(ReasonInvalidArgument)
		}
		if _, ok := n.jobT[o.a]; !ok {
			return n.reject(ReasonNotFound)
		}
		if n.stack[len(n.stack)-1] != o.a {
			return n.reject(ReasonNotTopOfStack)
		}
		for _, h := range n.held[o.a] {
			if h > 0 {
				return n.reject(ReasonStillHolding)
			}
		}
		n.stack = n.stack[:len(n.stack)-1]
		delete(n.jobT, o.a)
		delete(n.held, o.a)
		return naiveResult{ok: true}
	case "QCeil":
		if o.a == "" {
			return n.reject(ReasonInvalidArgument)
		}
		if _, ok := n.res[o.a]; !ok {
			return n.reject(ReasonNotFound)
		}
		return naiveResult{ok: true, intVal: n.ceil(o.a)}
	case "QAvail":
		if o.a == "" {
			return n.reject(ReasonInvalidArgument)
		}
		if _, ok := n.res[o.a]; !ok {
			return n.reject(ReasonNotFound)
		}
		return naiveResult{ok: true, intVal: n.avail[o.a]}
	case "QLevel":
		if o.a == "" {
			return n.reject(ReasonInvalidArgument)
		}
		if _, ok := n.taskD[o.a]; !ok {
			return n.reject(ReasonNotFound)
		}
		return naiveResult{ok: true, intVal: n.level(o.a)}
	case "QSys":
		return naiveResult{ok: true, intVal: n.sysCeil()}
	case "QStack":
		cp := append([]string(nil), n.stack...)
		return naiveResult{ok: true, stackV: cp}
	}
	return n.reject(ReasonInvalidArgument)
}
