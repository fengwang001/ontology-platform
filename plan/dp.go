package plan

import (
	"errors"
	"math"
	"sort"

	"ontology/catalog"
	"ontology/cost"
)

var (
	// ErrEmpty：目录中没有表，无法构造计划。
	ErrEmpty = errors.New("no tables to plan")
	// ErrLimit：非空子集数超过 MaxSubsets，拒绝继续分配内存。
	ErrLimit = errors.New("join table limit exceeded")
)

// MaxSubsets 是 DP 保留的非空子集条目硬上限。
const MaxSubsets = 4096

// RelTol 是代价判等的相对误差阈值，依据见 DESIGN.md 第 1 节。
const RelTol = 1e-12

// Counters 记录枚举工作量，用于复杂度上界断言。
type Counters struct {
	Subsets    int // 考察过的子集数
	Partitions int // 考察过的划分数
	Cartesians int // 实际采用的笛卡尔积候选数
}

// Result 是选择结果。
type Result struct {
	Root     *Node
	Counters Counters
	EstStats cost.Counters
}

// Choose 对目录中的全部表做按子集动态规划，返回代价最低的计划。
func Choose(c *catalog.Catalog) (*Result, error) {
	tables := append([]*catalog.Table(nil), c.Tables()...)
	sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })
	n := len(tables)
	if n == 0 {
		return nil, ErrEmpty
	}
	if uint64(1)<<uint(n)-1 > MaxSubsets {
		return nil, ErrLimit
	}

	est := cost.NewEstimator(c)
	best := make(map[uint64]*Node)
	all := uint64(1)<<uint(n) - 1
	ctr := Counters{}

	for i, t := range tables {
		mask := uint64(1) << uint(i)
		best[mask] = &Node{
			Kind: Scan, Table: t, Mask: mask,
			Names:    []string{t.Name},
			LeafSeq:  []string{t.Name},
			Card:     est.LeafCardinality(t),
			Cost:     cost.ScanCost(t.Rows),
			Warnings: catalog.TableWarnings(t),
		}
	}

	for mask := uint64(1); mask <= all; mask++ {
		if mask&(mask-1) == 0 {
			continue
		}
		ctr.Subsets++
		minBit := mask & -mask
		for sub := (mask - 1) & mask; sub != 0; sub = (sub - 1) & mask {
			if sub&minBit == 0 {
				continue
			}
			other := mask ^ sub
			l, r := best[sub], best[other]
			if l == nil || r == nil {
				continue
			}
			ctr.Partitions++
			cand := buildJoin(est, mask, l, r, all)
			if cand == nil {
				continue
			}
			if cand.Cartesian {
				ctr.Cartesians++
			}
			prev := best[mask]
			if prev == nil || better(cand, prev) {
				best[mask] = cand
			}
		}
	}

	return &Result{Root: best[all], Counters: ctr, EstStats: est.Counters}, nil
}

func buildJoin(est *cost.Estimator, mask uint64, l, r *Node, all uint64) *Node {
	sels, warns, predCount := est.Cross(l.Names, r.Names)
	cart := predCount == 0
	if cart && mask != all {
		return nil
	}
	card := cost.JoinCardinality(l.Card, r.Card, sels)
	jc := cost.JoinCost(l.Cost, r.Cost, l.Card, r.Card)
	names := unionNames(l.Names, r.Names)
	node := &Node{
		Kind: Join, Left: l, Right: r, Cartesian: cart,
		Independent: predCount >= 2,
		Mask:        mask, Names: names,
		LeafSeq: append(append([]string(nil), l.LeafSeq...), r.LeafSeq...),
		Card:    card, Cost: jc,
		Warnings: mergeWarnings(l.Warnings, r.Warnings, warns),
	}
	return node
}

func unionNames(a, b []string) []string {
	out := append([]string(nil), a...)
	out = append(out, b...)
	sort.Strings(out)
	return out
}

func mergeWarnings(groups ...[]catalog.Warning) []catalog.Warning {
	seen := map[string]bool{}
	var out []catalog.Warning
	for _, g := range groups {
		for _, w := range g {
			if !seen[w.Text] {
				seen[w.Text] = true
				out = append(out, w)
			}
		}
	}
	return out
}

// better 在代价容差内更优返回 true；同代价时叶子名字序列字典序更小者胜。
func better(a, b *Node) bool {
	scale := math.Max(math.Max(math.Abs(a.Cost), math.Abs(b.Cost)), 1)
	if math.Abs(a.Cost-b.Cost) <= RelTol*scale {
		return compareSeq(a.LeafSeq, b.LeafSeq) < 0
	}
	return a.Cost < b.Cost
}

func compareSeq(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}
