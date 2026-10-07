package submod

import (
	"fmt"
	"testing"
)

// buildWideTree 构造一个超级仓库：N 个根级挂载点（互不相关的分支），
// 外加一条深度 3 的挂载链 deep/x/y。返回协调器与深链路径。
func buildWideTree(t testing.TB, n int) (*Coordinator, string) {
	t.Helper()
	d := mustRepo(t, "D", cm("d1", nil, nil))
	b := mustRepo(t, "B", cm("b1", nil, Table{"y": {Repo: "D", Commit: "d1"}}))
	a := mustRepo(t, "A", cm("a1", nil, Table{"x": {Repo: "B", Commit: "b1"}}))
	tab := Table{"deep": {Repo: "A", Commit: "a1"}}
	for i := 0; i < n; i++ {
		tab[fmt.Sprintf("r%05d", i)] = Record{Repo: "D", Commit: "d1"}
	}
	super := mustRepo(t, "S", cm("s0", nil, tab))
	if err := super.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	c, err := NewCoordinator(map[string]*Repo{"S": super, "A": a, "B": b, "D": d}, "S", "main")
	if err != nil {
		t.Fatal(err)
	}
	return c, "deep/x/y"
}

// TestStatusAtVisitsIndependentOfMountCount 证明单个挂载点的状态判定
// 开销（记录解析步数）不随超级仓库中挂载点总数增长。
func TestStatusAtVisitsIndependentOfMountCount(t *testing.T) {
	var visits []uint64
	for _, n := range []int{10, 1000, 100000} {
		c, deep := buildWideTree(t, n)
		c.ResetVisits()
		st, err := c.StatusAt(deep)
		if err != nil {
			t.Fatal(err)
		}
		v := c.Visits()
		t.Logf("输入=挂载点总数%d 实际输出=状态%s/解析步数%d 判定依据=步数只等于挂载链深度3",
			n+1, st, v)
		if st != StatusMissing {
			t.Fatalf("期望缺失，得到 %s", st)
		}
		visits = append(visits, v)
	}
	if visits[0] != visits[1] || visits[1] != visits[2] {
		t.Fatalf("解析步数随挂载点总数增长: %v", visits)
	}
}

// TestCycleCheckIndependentOfUnrelatedBranches 证明循环挂载判定只访问
// 当前路径的祖先链，与树中无关分支的规模无关。
func TestCycleCheckIndependentOfUnrelatedBranches(t *testing.T) {
	// 无环树：N 个互不相关的宽分支 + 一条深链。
	for _, n := range []int{0, 5000} {
		c, deep := buildWideTree(t, n)
		c.ResetVisits()
		if _, err := c.StatusAt(deep); err != nil {
			t.Fatal(err)
		}
		t.Logf("输入=无关分支%d个 实际输出=解析步数%d 判定依据=循环判定只走祖先链",
			n, c.Visits())
	}
	c0, deep := buildWideTree(t, 0)
	c0.ResetVisits()
	if _, err := c0.StatusAt(deep); err != nil {
		t.Fatal(err)
	}
	base := c0.Visits()
	c1, deep1 := buildWideTree(t, 5000)
	c1.ResetVisits()
	if _, err := c1.StatusAt(deep1); err != nil {
		t.Fatal(err)
	}
	if got := c1.Visits(); got != base {
		t.Fatalf("循环判定开销随无关分支增长: %d -> %d", base, got)
	}
}

// BenchmarkStatusAt 挂载点总数变化时单点状态判定的基准（应近似平坦）。
func BenchmarkStatusAt(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		c, deep := buildWideTree(b, n)
		b.Run(fmt.Sprintf("mounts=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := c.StatusAt(deep); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkUpdateToPins 递归更新的基准（随挂载点总数线性，供参考）。
func BenchmarkUpdateToPins(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		c, _ := buildWideTree(b, n)
		b.Run(fmt.Sprintf("mounts=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := c.UpdateToPins(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
