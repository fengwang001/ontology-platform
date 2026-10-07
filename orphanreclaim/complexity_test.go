package orphanreclaim

import "testing"

// 可验证、可复现的复杂度凭据：每次判定只核对「类型桶」，
// BucketsChecked = 独立类型数（到命中为止）+ 已核对联合组成员数，
// 与对象入边总数无关。这里把入边从 10 增长到 10 万，记录判定期望上界不变。
func TestDecisionChecksBoundedByConfigNotEdgeCount(t *testing.T) {
	r, _, _ := newTestEngine(t, testConfig(), 0)
	r.CreateObject("t")

	// 配置规模上界：1 个独立类型 + 两个长度 2 的联合组 = 5 次桶核对。
	const maxBuckets = 5

	edgeCounts := []int{10, 100, 1000, 10000, 100000}
	var prev int
	for _, n := range edgeCounts {
		for prev < n {
			src := "s" + itoa(prev)
			r.CreateObject(src)
			mustAdd(t, r, src, "t", "J1") // 始终孤儿：只有 J1 单边
			prev++
		}
		orphan, err := r.IsOrphan("t")
		if err != nil || !orphan {
			t.Fatalf("n=%d: want orphan, got %v err=%v", n, orphan, err)
		}
		got := r.LastBucketsChecked()
		if got > maxBuckets {
			t.Fatalf("n=%d: buckets checked %d exceeds config bound %d", n, got, maxBuckets)
		}
		// 复现核对：同样的配置规模下，核对次数恒定（不随边数增长）。
		if n > 10 && got > maxBuckets {
			t.Fatalf("buckets checked grew with edge count: %d", got)
		}
	}

	// 入边总数确实达到了 10 万，证明判定没有退化为逐条扫描。
	total := 0
	for _, ts := range r.Snapshot().InEdges["t"] {
		total += len(ts)
	}
	if total != 100000 {
		t.Fatalf("expected 100000 inbound edges, got %d", total)
	}
}

// 联合命中时核对次数小于「全部入边数」，且等于「独立层全核 + 第一组长度」。
func TestBucketsCheckedExactValue(t *testing.T) {
	r, c, lg := newTestEngine(t, testConfig(), 0)
	r.CreateObject("t")
	for i := 0; i < 1000; i++ {
		s := "s" + itoa(i)
		r.CreateObject(s)
		mustAdd(t, r, s, "t", "X") // 1000 条 X 入边，X 需与 Y 同时存在
	}
	c.t = 50
	r.CreateObject("y")
	mustAdd(t, r, "y", "t", "Y") // 此刻联合组 {X,Y} 满足

	last := lg.decisions[len(lg.decisions)-1]
	// 独立层核对 I(不在) 1 次；联合按代表键稳定排序，{J1,J2} 中 J1 即缺(1 次)，
	// {X,Y} 两类型都在(2 次)。合计 4 次，与 1001 条入边无关。
	// 注意：X 是本配置中字典序排在 J1 之前的代表键？实际配置组代表为 J1 与 X，
	// 排序后 {J1,J2} 先核对。
	if last.BucketsChecked != 4 {
		t.Fatalf("BucketsChecked = %d, want 4 (independent of 1001 edges)", last.BucketsChecked)
	}
	if last.Basis.Layer != "joint" {
		t.Fatalf("want joint basis, got %s", last.Basis.Layer)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
