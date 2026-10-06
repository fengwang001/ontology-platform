package refstore

import "errors"

var (
	ErrEmptyCommitID   = errors.New("提交标识为空")
	ErrDuplicateCommit = errors.New("提交已存在")
	ErrUnknownParent   = errors.New("父提交不存在")
)

type commit struct {
	parents []string
	gen     uint64 // 代数：1 + 父提交代数的最大值
}

// Graph 是只增不改的提交图。对象一旦加入不可修改。
type Graph struct {
	commits map[string]commit
	// LastVisited 记录最近一次可达性查询访问的提交数，用于性能验证。
	LastVisited int
}

func NewGraph() *Graph { return &Graph{commits: make(map[string]commit)} }

// Add 追加一个提交。父提交必须已存在，标识不得重复。
func (g *Graph) Add(id string, parents ...string) error {
	if id == "" {
		return ErrEmptyCommitID
	}
	if _, ok := g.commits[id]; ok {
		return ErrDuplicateCommit
	}
	var gen uint64 = 1
	for _, p := range parents {
		pc, ok := g.commits[p]
		if !ok {
			return ErrUnknownParent
		}
		if pc.gen+1 > gen {
			gen = pc.gen + 1
		}
	}
	g.commits[id] = commit{parents: parents, gen: gen}
	return nil
}

// Has 报告提交是否存在。
func (g *Graph) Has(id string) bool {
	_, ok := g.commits[id]
	return ok
}

// Size 返回提交总数。
func (g *Graph) Size() int { return len(g.commits) }

// IsDescendantOrEqual 报告 newID 是否为 oldID 的后代（含相等）。
// 沿 newID 的祖先方向搜索 oldID，用代数剪枝：代数小于 oldID 的提交
// 不可能是 oldID 的后代路径上的点，直接跳过。访问的提交数只取决于
// 两点之间的提交，与图中无关提交的数量无关。
func (g *Graph) IsDescendantOrEqual(oldID, newID string) bool {
	g.LastVisited = 0
	if oldID == newID {
		return true
	}
	oldC, ok := g.commits[oldID]
	if !ok {
		return false
	}
	newC, ok := g.commits[newID]
	if !ok || newC.gen < oldC.gen {
		return false
	}
	seen := map[string]bool{newID: true}
	stack := []string{newID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		g.LastVisited++
		for _, p := range g.commits[id].parents {
			if p == oldID {
				return true
			}
			if seen[p] {
				continue
			}
			if g.commits[p].gen < oldC.gen {
				continue // 代数剪枝：该方向不可能到达 oldID
			}
			seen[p] = true
			stack = append(stack, p)
		}
	}
	return false
}
