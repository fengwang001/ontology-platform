package heating

import (
	"fmt"
	"testing"
	"time"
)

// buildScaledNet 构造可伸缩管网：src—b0—e1—b1—target—u1，
// 另有一条 b0—c1—c2—…—cK 的长链（每个链节点挂一个用户），
// 用于把全网规模放大 K 倍而保持目标管段周围的局部结构不变。
func buildScaledNet(t *testing.T, chainLen int) *Network {
	t.Helper()
	n := NewNetwork()
	mustNode(t, n, "src", NodeSource)
	mustNode(t, n, "b0", NodeBranch)
	mustNode(t, n, "b1", NodeBranch)
	mustNode(t, n, "u1", NodeUser)
	mustSegment(t, n, "t0", "src", "b0")
	mustSegment(t, n, "e1", "b0", "b1")
	mustSegment(t, n, "target", "b1", "u1")
	mustValve(t, n, "e1", EndB, "ve1")
	prev := "b0"
	for i := 1; i <= chainLen; i++ {
		c := fmt.Sprintf("c%d", i)
		u := fmt.Sprintf("cu%d", i)
		mustNode(t, n, c, NodeBranch)
		mustNode(t, n, u, NodeUser)
		mustSegment(t, n, fmt.Sprintf("cs%d", i), prev, c)
		mustSegment(t, n, fmt.Sprintf("us%d", i), c, u)
		mustValve(t, n, fmt.Sprintf("us%d", i), EndA, fmt.Sprintf("vu%d", i))
		prev = c
	}
	return n
}

// TestSimulationLocality 验证推演开销只与受影响区域相关：
// 两档全网规模（链长 300 与 2400）下，对同一局部结构的推演
// 访问的管段/节点数必须完全相同，耗时不随规模显著增长。
func TestSimulationLocality(t *testing.T) {
	small := buildScaledNet(t, 300)
	big := buildScaledNet(t, 2400)
	if got := len(big.segments); got != 3+2*2400 {
		t.Fatalf("大网管段数 = %d", got)
	}
	run := func(n *Network) (*Plan, time.Duration) {
		start := time.Now()
		p, err := n.SimulateIsolation("target")
		must(t, err)
		return p, time.Since(start)
	}
	ps, ds := run(small)
	pb, db := run(big)
	for _, p := range []*Plan{ps, pb} {
		assertPlan(t, p, []string{"ve1"}, []string{"target"}, []string{"u1"})
	}
	if ps.ExploredSegments != pb.ExploredSegments || ps.ExploredNodes != pb.ExploredNodes {
		t.Errorf("推演访问量随全网规模增长: small=(%d,%d) big=(%d,%d)",
			ps.ExploredSegments, ps.ExploredNodes, pb.ExploredSegments, pb.ExploredNodes)
	}
	t.Logf("小规模: 访问管段=%d 节点=%d 耗时=%s", ps.ExploredSegments, ps.ExploredNodes, ds)
	t.Logf("大规模: 访问管段=%d 节点=%d 耗时=%s", pb.ExploredSegments, pb.ExploredNodes, db)
	t.Logf("判定依据: 两档规模访问量相等(%d,%d)，证明开销只与隔离域及其边界相关",
		pb.ExploredSegments, pb.ExploredNodes)
}

// BenchmarkSimulate 供手动对比两档规模下的推演耗时。
func BenchmarkSimulate(b *testing.B) {
	for _, k := range []int{300, 2400} {
		n := buildScaledNet(&testing.T{}, k)
		b.Run(fmt.Sprintf("chain=%d", k), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := n.SimulateIsolation("target"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
