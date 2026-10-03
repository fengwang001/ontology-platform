package model

// Out 返回每个节点按加入顺序排列的出边目标；In 同理给出入边来源。
func (g *Graph) Out() [][]int {
	out := make([][]int, g.N+1)
	for _, e := range g.Edges {
		out[e.U] = append(out[e.U], e.V)
	}
	return out
}

func (g *Graph) In() [][]int {
	in := make([][]int, g.N+1)
	for _, e := range g.Edges {
		in[e.V] = append(in[e.V], e.U)
	}
	return in
}

// Validate 按 ErrStructure、ErrCycle、ErrReach 次序校验；全部通过返回 nil。
func (g *Graph) Validate() error {
	if err := g.checkStructure(); err != nil {
		return err
	}
	out := g.Out()
	if err := checkCycle(g.N, out); err != nil {
		return err
	}
	return checkReach(g.N, g.Kinds, out, g.In())
}

func (g *Graph) checkStructure() error {
	if g.N < 1 || g.N > 64 || len(g.Kinds) != g.N+1 {
		return ErrStructure
	}
	for _, k := range g.Kinds[1:] {
		if k < Start || k > End {
			return ErrStructure
		}
	}
	starts := 0
	for v := 1; v <= g.N; v++ {
		if g.Kinds[v] == Start {
			starts++
		}
	}
	if starts != 1 {
		return ErrStructure
	}
	seen := map[[2]int]bool{}
	for _, e := range g.Edges {
		if e.U < 1 || e.U > g.N || e.V < 1 || e.V > g.N || e.U == e.V {
			return ErrStructure
		}
		if seen[[2]int{e.U, e.V}] {
			return ErrStructure
		}
		seen[[2]int{e.U, e.V}] = true
	}
	out := g.Out()
	in := g.In()
	for v := 1; v <= g.N; v++ {
		ni, no := len(in[v]), len(out[v])
		switch g.Kinds[v] {
		case Start:
			if ni != 0 || no != 1 {
				return ErrStructure
			}
		case Task:
			if ni < 1 || no != 1 {
				return ErrStructure
			}
		case AndSplit, XorSplit, OrSplit:
			if ni != 1 || no < 2 {
				return ErrStructure
			}
		case AndJoin, OrJoin:
			if ni < 2 || no != 1 {
				return ErrStructure
			}
		case End:
			if ni < 1 || no != 0 {
				return ErrStructure
			}
		}
	}
	return nil
}

func checkCycle(n int, out [][]int) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]byte, n+1)
	var visit func(v int) bool
	visit = func(v int) bool {
		color[v] = gray
		for _, w := range out[v] {
			if color[w] == gray || (color[w] == white && visit(w)) {
				return true
			}
		}
		color[v] = black
		return false
	}
	for v := 1; v <= n; v++ {
		if color[v] == white && visit(v) {
			return ErrCycle
		}
	}
	return nil
}

func checkReach(n int, kinds []NodeType, out, in [][]int) error {
	start := -1
	for v := 1; v <= n; v++ {
		if kinds[v] == Start {
			start = v
		}
	}
	fwd := make([]bool, n+1)
	stack := []int{start}
	fwd[start] = true
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, w := range out[v] {
			if !fwd[w] {
				fwd[w] = true
				stack = append(stack, w)
			}
		}
	}
	endCanReach := make([]bool, n+1)
	for v := 1; v <= n; v++ {
		if kinds[v] == End {
			stack = append(stack, v)
			endCanReach[v] = true
		}
	}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, u := range in[v] {
			if !endCanReach[u] {
				endCanReach[u] = true
				stack = append(stack, u)
			}
		}
	}
	for v := 1; v <= n; v++ {
		if !fwd[v] || !endCanReach[v] {
			return ErrReach
		}
	}
	return nil
}
