package cluster

import (
	"strings"
	"testing"
)

func check(t *testing.T, m *Merger, sim *naiveSim, tenant, msg string) Result {
	t.Helper()
	got, err := m.Ingest(tenant, msg)
	if err != nil {
		t.Fatalf("Ingest(%q,%q) 意外错误: %v", tenant, msg, err)
	}
	id, created, overflow, reason := sim.ingest(tenant, msg)
	t.Logf("输入 tenant=%q msg=%q | 输出 {ID:%d Created:%v Overflow:%v Eq:%d} | 判定: %s",
		tenant, msg, got.ID, got.Created, got.Overflow, got.Eq, reason)
	if got.ID != id || got.Created != created || got.Overflow != overflow {
		t.Fatalf("与朴素模拟不一致: got={%d,%v,%v} sim={%d,%v,%v} msg=%q",
			got.ID, got.Created, got.Overflow, id, created, overflow, msg)
	}
	return got
}

func textOf(t *testing.T, m *Merger, tenant string) []string {
	t.Helper()
	infos, overflow, err := m.Snapshot(tenant)
	if err != nil {
		t.Fatal(err)
	}
	if overflow != 0 {
		t.Fatalf("该用例不应出现溢出, got %d", overflow)
	}
	texts := make([]string, len(infos))
	for i, info := range infos {
		texts[i] = join(info.Text)
	}
	return texts
}

func TestSpecExample(t *testing.T) {
	m, _ := New(50, 10, 10)
	sim := newNaive(50, 10)
	const tn = "t1"
	check(t, m, sim, tn, "open file a.txt ok")
	check(t, m, sim, tn, "open file b.txt ok")
	check(t, m, sim, tn, "open dir c.txt fail")
	check(t, m, sim, tn, "open 7 files ok")
	check(t, m, sim, tn, "close file a.txt ok")

	infos, overflow, _ := m.Snapshot(tn)
	if len(infos) != 2 || overflow != 0 {
		t.Fatalf("模板数=%d overflow=%d, 期望 2,0", len(infos), overflow)
	}
	want0 := []string{"open <*> <*> <*>", "close file a.txt ok"}
	if join(infos[0].Text) != want0[0] || join(infos[1].Text) != want0[1] {
		t.Fatalf("文本=%v, 期望 %v", texts(infos), want0)
	}
	if infos[0].Count != 4 || infos[1].Count != 1 {
		t.Fatalf("计数=%d,%d 期望 4,1", infos[0].Count, infos[1].Count)
	}
	t.Logf("最终模板: %v，判定: 与规格逐步示例一致", texts(infos))
}

func texts(infos []TemplateInfo) []string {
	out := make([]string, len(infos))
	for i, x := range infos {
		out[i] = join(x.Text)
	}
	return out
}

func TestThresholdExactAndOffByOne(t *testing.T) {
	// n=4, θ=50 需 eq>=2（恰等命中）；θ=51 需 eq>=3（差1不命中）。
	for _, tc := range []struct {
		theta   int
		matched bool
	}{{50, true}, {51, false}} {
		m, _ := New(tc.theta, 10, 10)
		m.Ingest("t", "a b c d")
		r, _ := m.Ingest("t", "a b x y") // eq=2
		if r.Matched != tc.matched {
			t.Fatalf("theta=%d Matched=%v 期望 %v", tc.theta, r.Matched, tc.matched)
		}
		t.Logf("θ=%d: a b x y eq=2, 2*100 ? 4*%d => Matched=%v", tc.theta, tc.theta, r.Matched)
	}
}

func TestTieBreaker(t *testing.T) {
	// θ=75, n=4 需 eq>=3：并列时取更具体（通配少）再 id 小。
	m, _ := New(75, 10, 10)
	sim := newNaive(75, 10)
	check(t, m, sim, "t", "x a b c")      // id1
	check(t, m, sim, "t", "x p q c")      // eq2 不合格 → id2
	check(t, m, sim, "t", "k z q n")      // 不同叶子，两模板均不被修改
	r := check(t, m, sim, "t", "x a b d") // 对 id1 eq=3 命中，id1 末位泛化（1 个通配）；对 id2 eq=1 不合格
	if r.ID != 1 {
		t.Fatalf("该步只应命中 id1, got id=%d", r.ID)
	}
	// 此刻 id1 含 1 个通配位（位置3），id2 完全具体。
	// 下一条在位置3与二者都不同：对 id1 eq=3（通配位算相符，1 个通配），
	// 对 id2 eq=3（0 个通配），并列取更具体的 id2。
	r = check(t, m, sim, "t", "x p q n")
	if r.ID != 2 {
		t.Fatalf("更具体者应优先, got id=%d", r.ID)
	}
	ts := textOf(t, m, "t")
	t.Logf("模板文本: %v", ts)
	if !strings.Contains(ts[0], "<*>") {
		t.Fatalf("id1 应已含通配位: %v", ts)
	}
	if !strings.Contains(ts[1], "<*>") {
		t.Fatalf("id2 命中后差异位应泛化: %v", ts)
	}
}

func TestMaskedFirstWordLeaf(t *testing.T) {
	m, _ := New(100, 10, 10)
	m.Ingest("t", "9 start")
	m.Ingest("t", "8 start") // 首词均掩码 → 同叶子 (2,<*>)；θ=100 下掩码后完全相同
	infos, _, _ := m.Snapshot("t")
	if len(infos) != 1 || join(infos[0].Text) != "<*> start" || infos[0].Count != 2 {
		t.Fatalf("数字首词应进入 <*> 叶子并归并: %+v", infos)
	}
	t.Logf("数字首词进入叶子 (2,<*>)，文本=%q count=%d", join(infos[0].Text), infos[0].Count)
}

func TestOverflowDoesNotMergeApprox(t *testing.T) {
	m, _ := New(99, 1, 10)
	m.Ingest("t", "a b c d")
	r, _ := m.Ingest("t", "a x y z") // eq=1 不合格，且 Tmax=1
	if r.ID != 0 || !r.Overflow {
		t.Fatalf("应进溢出桶, got %+v", r)
	}
	infos, overflow, _ := m.Snapshot("t")
	if len(infos) != 1 || infos[0].Count != 1 || overflow != 1 {
		t.Fatalf("近似模板不得被污染: infos=%+v overflow=%d", infos, overflow)
	}
	t.Logf("溢出后原模板未被修改: %q count=%d, overflow=%d", join(infos[0].Text), infos[0].Count, overflow)
}

func TestLeafComparisonCounter(t *testing.T) {
	run := func(otherLeafCount int) int64 {
		m, _ := New(1, 100000, 10)
		// 目标叶子 (2,z)：先造 1 个模板。
		m.Ingest("t", "z 1")
		// 其余叶子用唯一首词造 otherLeafCount 个互不相干的 (2, yN) 叶子。
		for i := 0; i < otherLeafCount; i++ {
			m.Ingest("t", padY(i))
		}
		before := m.comparedCount()
		m.Ingest("t", "z 2") // 只应与 (2,z) 叶子的 1 个模板比较
		return m.comparedCount() - before
	}
	c1 := run(1)
	c2 := run(5000)
	if c1 != 1 || c2 != 1 {
		t.Fatalf("比较数应恒等于所属叶子模板数1, got 其余1叶=%d 其余5000叶=%d", c1, c2)
	}
	t.Logf("非导出计数器断言: 其余叶子 1 个与 5000 个时, 本次比较数均=%d", c1)
}

func padY(i int) string {
	// 长度 2、首词 yN 唯一，每条独占一个叶子，与 (2,z) 互不相交。
	return "y" + itoa(i) + " tail"
}

func itoa(i int) string {
	if i == 0 {
		return "z0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('a' + i%26)}, b...)
		i /= 26
	}
	return string(b)
}
