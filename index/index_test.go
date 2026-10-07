package index

import (
	"math/rand"
	"testing"
)

// naiveQuery 是独立的朴素参考实现：在可见前缀内线性扫描。
func naiveQuery(sys, biz []int64, seq []int64, sysQ, bizQ int64) (int64, bool) {
	bestBiz := int64(-1) << 62
	found := false
	bestSeq := int64(0)
	for i := range sys {
		if sys[i] > sysQ {
			break
		}
		if biz[i] <= bizQ && biz[i] >= bestBiz {
			// biz[i] == bestBiz 时后者系统时间更晚，覆盖前者。
			bestBiz = biz[i]
			bestSeq = seq[i]
			found = true
		}
	}
	return bestSeq, found
}

func TestChainMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	c := NewChain()
	var sys, biz, seq []int64
	for i := int64(1); i <= 2000; i++ {
		b := rng.Int63n(300)
		s := int64(len(sys) + 1)
		c.Add(b, s, i)
		sys = append(sys, s)
		biz = append(biz, b)
		seq = append(seq, i)
	}
	for q := 0; q < 5000; q++ {
		sysQ := rng.Int63n(int64(len(sys)) + 2)
		bizQ := rng.Int63n(320) - 10
		gotSeq, gotOK := c.Query(sysQ, bizQ)
		wantSeq, wantOK := naiveQuery(sys, biz, seq, sysQ, bizQ)
		if gotOK != wantOK || gotSeq != wantSeq {
			t.Fatalf("Query(%d,%d) = (%d,%v), want (%d,%v)",
				sysQ, bizQ, gotSeq, gotOK, wantSeq, wantOK)
		}
	}
}

func TestChainEqualBizStartOverride(t *testing.T) {
	c := NewChain()
	c.Add(10, 1, 1) // 业务起点 10，系统时间 1
	c.Add(10, 2, 2) // 业务起点相同，系统时间更晚，覆盖可见性
	c.Add(20, 3, 3)
	// 快照保留：sysQ=1 时仍看到第一条。
	if seq, ok := c.Query(1, 10); !ok || seq != 1 {
		t.Fatalf("sysQ=1 应命中 seq=1, got %d,%v", seq, ok)
	}
	// sysQ=2 起被第二条覆盖。
	if seq, ok := c.Query(2, 10); !ok || seq != 2 {
		t.Fatalf("sysQ=2 应命中 seq=2, got %d,%v", seq, ok)
	}
	// 两条记录都保留：Len 为 3。
	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3", c.Len())
	}
}

func TestChainPrefixIsolation(t *testing.T) {
	c := NewChain()
	c.Add(10, 1, 1)
	c.Add(20, 2, 2) // 截断 [10,20)
	// 未来提交不影响过去的系统时间视图：sysQ=1 时 bizQ=25 仍由 seq=1 覆盖。
	if seq, ok := c.Query(1, 25); !ok || seq != 1 {
		t.Fatalf("sysQ=1,bizQ=25 应命中 seq=1, got %d,%v", seq, ok)
	}
	// sysQ=2 时 bizQ=25 由 seq=2 覆盖。
	if seq, ok := c.Query(2, 25); !ok || seq != 2 {
		t.Fatalf("sysQ=2,bizQ=25 应命中 seq=2, got %d,%v", seq, ok)
	}
	// sysQ 早于任何提交。
	if _, ok := c.Query(0, 10); ok {
		t.Fatal("sysQ=0 应无覆盖版本")
	}
	// bizQ 早于任何业务起点。
	if _, ok := c.Query(2, 5); ok {
		t.Fatal("bizQ=5 应无覆盖版本")
	}
}

// TestChainQueryVisitedBound 以访问节点数证明查询开销为 O(log n)，
// 不随版本总数线性增长。
func TestChainQueryVisitedBound(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	c := NewChain()
	const n = 1 << 18
	for i := int64(1); i <= n; i++ {
		c.Add(rng.Int63n(1<<40), i, i)
	}
	// 随机 treap 的期望高度约 3*log2(n)，取 6*log2(n)+10 为宽松上界。
	const bound = 6*18 + 10
	worst := 0
	for q := 0; q < 20000; q++ {
		c.Query(rng.Int63n(n)+1, rng.Int63n(1<<40))
		if v := c.LastQueryVisited(); v > worst {
			worst = v
		}
	}
	t.Logf("n=%d, 2 万次随机查询最大访问节点数=%d, 上界=%d", n, worst, bound)
	if worst > bound {
		t.Fatalf("访问节点数 %d 超过对数上界 %d", worst, bound)
	}
	if worst >= n/100 {
		t.Fatalf("访问节点数 %d 与版本总数 %d 呈线性关系", worst, n)
	}
}
