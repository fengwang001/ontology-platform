package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveGraph 是题目定义的逐点朴素实现，仅用于对拍：
// 先求两侧全祖先集合，取交集，再删除交集中“被其他元素祖先化”的提交。
type naiveGraph struct {
	parents map[string][]string
}

func (n *naiveGraph) ancestors(start string) map[string]bool {
	res := map[string]bool{start: true}
	stack := []string{start}
	for len(stack) > 0 {
		last := len(stack) - 1
		cur := stack[last]
		stack = stack[:last]
		for _, p := range n.parents[cur] {
			if !res[p] {
				res[p] = true
				stack = append(stack, p)
			}
		}
	}
	return res
}

func (n *naiveGraph) isAncestor(a, b string) bool {
	return n.ancestors(b)[a]
}

func (n *naiveGraph) mergeBases(a, b string) []string {
	caSet := map[string]bool{}
	ancA := n.ancestors(a)
	ancB := n.ancestors(b)
	for id := range ancA {
		if ancB[id] {
			caSet[id] = true
		}
	}
	var ca []string
	for id := range caSet {
		ca = append(ca, id)
	}
	sort.Strings(ca)

	var bases []string
	for _, x := range ca {
		maximal := true
		for _, y := range ca {
			if x != y && n.isAncestor(x, y) {
				maximal = false
				break
			}
		}
		if maximal {
			bases = append(bases, x)
		}
	}
	sort.Strings(bases)
	return bases
}

// randomDAG 按登记顺序构造合法 DAG：每个提交随机选择 0..min(3,已有数) 个
// 已登记且互不相同的父；以固定概率制造多根，覆盖不连通情形。
func randomDAG(t *testing.T, rng *rand.Rand) (*Graph, *naiveGraph, []string) {
	t.Helper()
	g := NewGraph()
	ng := &naiveGraph{parents: map[string][]string{}}
	var ids []string

	n := 5 + rng.Intn(46)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%03d", i)
		var parents []string
		if i > 0 && rng.Float64() > 0.12 {
			maxK := 3
			if i < maxK {
				maxK = i
			}
			k := 1 + rng.Intn(maxK)
			picked := map[int]bool{}
			for len(picked) < k {
				picked[rng.Intn(i)] = true
			}
			for idx := range picked {
				parents = append(parents, ids[idx])
			}
			sort.Strings(parents)
		}
		if err := g.Commit(id, parents); err != nil {
			t.Fatalf("随机图登记 %s 父=%v 失败: %v", id, parents, err)
		}
		ng.parents[id] = append([]string(nil), parents...)
		ids = append(ids, id)
	}
	return g, ng, ids
}

// TestDifferentialAgainstNaive 在 2000 组随机 DAG 上对拍 IsAncestor 与
// MergeBases，并在日志中打印输入、输出与判定依据。
func TestDifferentialAgainstNaive(t *testing.T) {
	const cases = 2000
	rng := rand.New(rand.NewSource(20261001))

	for c := 0; c < cases; c++ {
		g, ng, ids := randomDAG(t, rng)
		x := ids[rng.Intn(len(ids))]
		y := ids[rng.Intn(len(ids))]

		gotAncestor, err := g.IsAncestor(x, y)
		if err != nil {
			t.Fatalf("case %d IsAncestor(%s,%s) 意外错误: %v", c, x, y, err)
		}
		wantAncestor := ng.isAncestor(x, y)

		got, err := g.MergeBases(x, y)
		if err != nil {
			t.Fatalf("case %d MergeBases(%s,%s) 意外错误: %v", c, x, y, err)
		}
		want := ng.mergeBases(x, y)

		basis := "公共祖先交集的极大元"
		if gotAncestor {
			basis = fmt.Sprintf("%s 是 %s 的祖先，按定义合并基恰为 {%s}", x, y, x)
		} else if len(want) == 0 {
			basis = "两侧祖先集合无交集（不连通），结果为空集"
		}

		if gotAncestor != wantAncestor {
			t.Fatalf("case %d IsAncestor(%s,%s)=%v 朴素=%v", c, x, y, gotAncestor, wantAncestor)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("case %d 输入=(%s,%s)\n快速实现=%v\n朴素实现=%v\n判定依据=%s",
				c, x, y, got, want, basis)
		}

		// 每组打印输入、输出与判定依据；-test.v 时可见。
		t.Logf("case %04d 输入=(%s,%s) 输出=%v IsAncestor=%v 依据=%s",
			c, x, y, got, gotAncestor, basis)
	}
}
