package numa

import (
	"sync"
	"testing"
)

func hints(hs ...Hint) *[]Hint {
	h := append([]Hint(nil), hs...)
	return &h
}

func reason(err error) RejectReason {
	if e, ok := err.(*RejectError); ok {
		return e.Reason
	}
	return ""
}

func eqCPUs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustAdmit(t *testing.T, m *Manager, id string, req int64, ps []Provider, mask uint16, cpus []int64) *AdmitResult {
	t.Helper()
	r, err := m.Admit(id, req, ps)
	if err != nil {
		t.Fatalf("Admit(%s): %v", id, err)
	}
	if r.Mask != mask || !eqCPUs(r.CPUs, cpus) {
		t.Fatalf("Admit(%s): got mask=%b cpus=%v, want mask=%b cpus=%v", id, r.Mask, r.CPUs, mask, cpus)
	}
	return r
}

// 规格中的两段例子：restricted 下内置提示随空闲变化并溢出。
func TestSpecExample(t *testing.T) {
	m, err := NewManager(2, []int64{4, 4}, PolicyRestricted)
	if err != nil {
		t.Fatal(err)
	}
	mustAdmit(t, m, "a", 3, nil, 0b01, []int64{3, 0})
	mustAdmit(t, m, "b", 3, nil, 0b10, []int64{0, 3})
	mustAdmit(t, m, "c", 2, nil, 0b11, []int64{1, 1})

	m2, _ := NewManager(2, []int64{4, 4}, PolicyRestricted)
	mustAdmit(t, m2, "x", 3, nil, 0b01, []int64{3, 0})
	_, err = m2.Admit("y", 3, []Provider{hints(Hint{Mask: 0b01, Preferred: true})})
	if reason(err) != ReasonHintNotSatisfied {
		t.Fatalf("got %v, want hint-not-satisfied", err)
	}

	m3, _ := NewManager(2, []int64{4, 4}, PolicyBestEffort)
	mustAdmit(t, m3, "x", 3, nil, 0b01, []int64{3, 0})
	r := mustAdmit(t, m3, "y", 3, []Provider{hints(Hint{Mask: 0b01, Preferred: true})}, 0b01, []int64{1, 2})
	if r.Preferred {
		t.Fatalf("best hint must be non-preferred")
	}
}

// 无偏好 -> (全,true)；空列表 -> (全,false)。
func TestNoPreferenceVsEmpty(t *testing.T) {
	m, _ := NewManager(2, []int64{4, 4}, PolicyRestricted)
	r, err := m.Admit("a", 3, []Provider{nil})
	if err != nil || r.Mask != 0b01 || !r.Preferred {
		t.Fatalf("no-preference: r=%v err=%v", r, err)
	}

	m2, _ := NewManager(2, []int64{4, 4}, PolicyRestricted)
	_, err = m2.Admit("a", 3, []Provider{hints()})
	if reason(err) != ReasonHintNotSatisfied {
		t.Fatalf("empty list: got %v", err)
	}
	m3, _ := NewManager(2, []int64{4, 4}, PolicyBestEffort)
	r, err = m3.Admit("a", 3, []Provider{hints()})
	if err != nil || r.Preferred || r.Mask != 0b01 {
		t.Fatalf("empty list best-effort: r=%v err=%v", r, err)
	}
}

// Z 的相对窄化：只有位数等于 Z 的 allPref 候选 preferred。
func TestRelativeNarrowingZ(t *testing.T) {
	// free=[1,1,1,1]，req=2：单位掩码均不可行，Zc=2，六个两位掩码 preferred。
	// 外部固定 (0011,true)：与内置 0011 交 0011（allPref,2位），
	// 与内置 0101 交 0001（allPref,1位），故 Z=1——
	// 直接匹配的 0011 虽 allPref 也不 preferred，最优 0001,preferred。
	m, _ := NewManager(4, []int64{1, 1, 1, 1}, PolicyBestEffort)
	r, err := m.Admit("b", 2, []Provider{hints(Hint{Mask: 0b0011, Preferred: true})})
	if err != nil || !r.Preferred || r.Mask != 0b0001 {
		t.Fatalf("r=%v err=%v", r, err)
	}

	// 并列场景：外部 (1100,true)，与 0110 交 0100、与 1010 交 1000，
	// 均 allPref 一位，Z=1，取数值小 0100。
	m2, _ := NewManager(4, []int64{1, 1, 1, 1}, PolicyBestEffort)
	r2, err := m2.Admit("c", 2, []Provider{hints(Hint{Mask: 0b1100, Preferred: true})})
	if err != nil || !r2.Preferred || r2.Mask != 0b0100 {
		t.Fatalf("r2=%v err=%v", r2, err)
	}
}

// 位数并列时取数值最小掩码。
func TestTieSmallestMask(t *testing.T) {
	// n=3,cap=[2,2,2]：预置占 node2 2 个，free=[2,2,0]，req=3。
	// 内置可行两位掩码仅 011（node0+node1=4），Zc=2，提示 (011,true)。
	// 外部给 (011,true) 与 (111,true)：候选 011（2位,allPref）、
	// 与 111 交 011（2位）——构造两位并列改用两个外部提供者各给提示，
	// 使内置 011 与 (011) 交 011；再用第二个外部给 (110,true) 使另一路为空。
	// 直接制造两位掩码并列：外部提供者给 [(011,true),(111,true)]，
	// 内置再提供两个 Zc=2 的掩码需要 free 对称：改回 cap=[2,2,2]，
	// req=3，外部单提示 111(true)：候选即内置 011/101/110，
	// 三个 allPref 两位掩码并列，取数值最小 011。
	m, _ := NewManager(3, []int64{2, 2, 2}, PolicyRestricted)
	r, err := m.Admit("a", 3, []Provider{hints(Hint{Mask: 0b111, Preferred: true})})
	if err != nil || r.Mask != 0b011 || !r.Preferred {
		t.Fatalf("r=%v err=%v", r, err)
	}
}

// 所有掩码相与为空 -> 无候选 -> (全,false)，restricted 拒绝。
func TestAllIntersectionEmpty(t *testing.T) {
	// single-numa-node 下全掩码 11 被归一丢弃：free=[0,4] 时内置只剩 (10,true)，
	// 外部坚持 01，相与为空 -> 无候选 -> (全,false) -> 拒绝。
	m0, _ := NewManager(2, []int64{4, 4}, PolicySingleNUMANode)
	mustAdmit(t, m0, "x", 4, nil, 0b01, []int64{4, 0})
	_, err0 := m0.Admit("a", 3, []Provider{hints(Hint{Mask: 0b01, Preferred: true})})
	if reason(err0) != ReasonHintNotSatisfied {
		t.Fatalf("single-numa empty candidates: %v", err0)
	}

	// restricted：full 掩码恒可行故候选不会全空，但最优 (01,false) 仍拒绝。
	m, _ := NewManager(2, []int64{4, 4}, PolicyRestricted)
	mustAdmit(t, m, "x", 4, nil, 0b01, []int64{4, 0})
	// free=[0,4]：内置 (10,true)(11,false)，外部坚持 01。
	_, err := m.Admit("a", 3, []Provider{hints(Hint{Mask: 0b01, Preferred: true})})
	if reason(err) != ReasonHintNotSatisfied {
		t.Fatalf("got %v", err)
	}
	// best-effort 下取最优 (01,false)，掩码内容量不足溢出为 [1,2]（规格第二例）。
	m3, _ := NewManager(2, []int64{4, 4}, PolicyBestEffort)
	mustAdmit(t, m3, "x", 4, nil, 0b01, []int64{4, 0})
	// free=[0,4] 下 node0 无空闲，掩码 01 内取 0，溢出 node1=3。
	r := mustAdmit(t, m3, "a", 3, []Provider{hints(Hint{Mask: 0b01, Preferred: true})}, 0b01, []int64{0, 3})
	if r.Preferred {
		t.Fatal("want non-preferred mask")
	}
}

// single-numa-node：丢弃多位提示；丢弃后变空；跨节点需求拒绝。
func TestSingleNUMANodePolicy(t *testing.T) {
	m, _ := NewManager(2, []int64{4, 4}, PolicySingleNUMANode)
	_, err := m.Admit("a", 3, []Provider{hints(Hint{Mask: 0b11, Preferred: true})})
	if reason(err) != ReasonHintNotSatisfied {
		t.Fatalf("got %v", err)
	}
	r, err := m.Admit("b", 3, []Provider{hints(Hint{Mask: 0b10, Preferred: true})})
	if err != nil || r.Mask != 0b10 {
		t.Fatalf("r=%v err=%v", r, err)
	}
	_, err = m.Admit("c", 5, nil)
	if reason(err) != ReasonHintNotSatisfied {
		t.Fatalf("req=5 single-numa: got %v", err)
	}
}

// 外部提供者为零个。
func TestZeroProviders(t *testing.T) {
	m, _ := NewManager(2, []int64{4, 4}, PolicyBestEffort)
	r, err := m.Admit("a", 3, []Provider{})
	if err != nil || r.Mask != 0b01 {
		t.Fatalf("r=%v err=%v", r, err)
	}
	r2, err := m.Admit("b", 3, nil)
	if err != nil || r2.Mask != 0b10 {
		t.Fatalf("r2=%v err=%v", r2, err)
	}
}

// Release 后空闲恢复；Zc 随空闲量增大而缩小；不存在原因可区分。
func TestReleaseRestoresFree(t *testing.T) {
	m, _ := NewManager(2, []int64{4, 4}, PolicyRestricted)
	mustAdmit(t, m, "a", 3, nil, 0b01, []int64{3, 0})
	mustAdmit(t, m, "b", 3, nil, 0b10, []int64{0, 3})
	mustAdmit(t, m, "c", 2, nil, 0b11, []int64{1, 1})
	if err := m.Release("a"); err != nil {
		t.Fatal(err)
	}
	// 释放 a 后 free=[3,0]，总空闲3；req=3：Zc=1（掩码 01）。
	mustAdmit(t, m, "d", 3, nil, 0b01, []int64{3, 0})
	if err := m.Release("a"); reason(err) != ReasonContainerMissing {
		t.Fatalf("double release: %v", err)
	}
	if _, err := m.Query("zzz"); reason(err) != ReasonContainerMissing {
		t.Fatalf("query missing: %v", err)
	}
}

// 掩码内不足时溢出到掩码外，按节点号升序。
func TestSpilloverOrder(t *testing.T) {
	m, _ := NewManager(3, []int64{4, 4, 4}, PolicyBestEffort)
	mustAdmit(t, m, "p", 3, []Provider{hints(Hint{Mask: 0b001, Preferred: true})}, 0b001, []int64{3, 0, 0})
	mustAdmit(t, m, "q", 3, []Provider{hints(Hint{Mask: 0b010, Preferred: true})}, 0b010, []int64{0, 3, 0})
	// free=[1,1,4]，req=4：node2 单独可容，Zc=1，内置仅 (100,preferred)，
	// 其余可行掩码 101/110/111 非 preferred。外部坚持 (001,true)：
	// 与 100 交空丢弃，与非 preferred 掩码交 001（非 allPref），
	// 最优 (001,false)：掩码内 node0 取 1，溢出 node1=1、node2=2。
	r := mustAdmit(t, m, "s", 4, []Provider{hints(Hint{Mask: 0b001, Preferred: true})}, 0b001, []int64{1, 1, 2})
	if r.Preferred {
		t.Fatal("want non-preferred mask")
	}
}

// 组合数恰为 1e5 与 1e5+1；内置归一后长度计入，提示不去重。
func TestCombinationLimit(t *testing.T) {
	mk := func(n int, mask uint16, l int) *Manager {
		caps := make([]int64, n)
		for i := range caps {
			caps[i] = 1_000_000_000
		}
		m, _ := NewManager(n, caps, PolicyBestEffort)
		return m
	}
	prov := func(mask uint16, l int) []Provider {
		hs := make([]Hint, l)
		for i := range hs {
			hs[i] = Hint{Mask: mask, Preferred: true}
		}
		h := []Hint(hs)
		return []Provider{&h}
	}
	// n=1：内置归一后只有 1 条提示。
	if _, err := mk(1, 1, 0).Admit("a", 1, prov(1, 100_000)); err != nil {
		t.Fatalf("100000 combos should pass: %v", err)
	}
	if _, e := mk(1, 1, 0).Admit("a", 1, prov(1, 100_001)); reason(e) != ReasonTooManyCombos {
		t.Fatalf("100001 combos: got %v", e)
	}
	// n=2,req=1 时内置提示 01,10,11 共 3 条：3*33333=99999 通过，3*33334=100002 拒绝。
	if _, err := mk(2, 0, 0).Admit("a", 1, prov(0b11, 33_333)); err != nil {
		t.Fatalf("99999 combos: %v", err)
	}
	if _, e := mk(2, 0, 0).Admit("a", 1, prov(0b11, 33_334)); reason(e) != ReasonTooManyCombos {
		t.Fatalf("100002 combos: got %v", e)
	}
}

// none 忽略提示、不检查组合数，结果（全,true），仍做容量检查与实际分配。
func TestNonePolicy(t *testing.T) {
	m, _ := NewManager(2, []int64{4, 4}, PolicyNone)
	hs := make([]Hint, 200_000)
	for i := range hs {
		hs[i] = Hint{Mask: 0b01, Preferred: true}
	}
	// 空列表与超量组合都被 none 忽略。
	r, err := m.Admit("a", 5, []Provider{(*[]Hint)(nil), &hs, hints()})
	if err != nil || r.Mask != 0b11 || !r.Preferred || !eqCPUs(r.CPUs, []int64{4, 1}) {
		t.Fatalf("none: r=%v err=%v", r, err)
	}
	// 容量不足先触发。
	_, err = m.Admit("b", 4, nil)
	if reason(err) != ReasonInsufficientCPU {
		t.Fatalf("none capacity: got %v", err)
	}
}

// 拒绝原因先后次序与构造配置非法。
func TestRejectOrdering(t *testing.T) {
	m, _ := NewManager(2, []int64{4, 4}, PolicyRestricted)
	mustAdmit(t, m, "a", 3, nil, 0b01, []int64{3, 0})

	if _, err := m.Admit("", 3, nil); reason(err) != ReasonInvalidArgument {
		t.Fatalf("empty id: %v", err)
	}
	if _, err := m.Admit("x", 0, nil); reason(err) != ReasonInvalidArgument {
		t.Fatalf("req low: %v", err)
	}
	if _, err := m.Admit("x", 1_000_000_000_001, nil); reason(err) != ReasonInvalidArgument {
		t.Fatalf("req high: %v", err)
	}
	if _, err := m.Admit("x", 3, []Provider{hints(Hint{Mask: 0})}); reason(err) != ReasonInvalidArgument {
		t.Fatalf("mask zero: %v", err)
	}
	if _, err := m.Admit("x", 3, []Provider{hints(Hint{Mask: 0b100})}); reason(err) != ReasonInvalidArgument {
		t.Fatalf("mask overflow: %v", err)
	}
	// 已存在先于容量/组合/提示。
	big := make([]Hint, 200_000)
	for i := range big {
		big[i] = Hint{Mask: 0b01, Preferred: true}
	}
	if _, err := m.Admit("a", 9, []Provider{&big}); reason(err) != ReasonContainerExists {
		t.Fatalf("exists first: %v", err)
	}
	// 容量不足先于组合过多。
	if _, err := m.Admit("z", 9, []Provider{&big}); reason(err) != ReasonInsufficientCPU {
		t.Fatalf("capacity before combos: %v", err)
	}
	// 组合过多先于提示不满足。
	if _, err := m.Admit("z", 3, []Provider{&big}); reason(err) != ReasonTooManyCombos {
		t.Fatalf("combos before hints: %v", err)
	}
	// 最后才是提示不满足。
	if _, err := m.Admit("z", 3, []Provider{hints(Hint{Mask: 0b01, Preferred: true})}); reason(err) != ReasonHintNotSatisfied {
		t.Fatalf("hint last: %v", err)
	}

	for _, c := range []struct {
		n      int
		caps   []int64
		policy Policy
	}{
		{0, []int64{1}, PolicyNone},
		{9, make([]int64, 9), PolicyNone},
		{2, []int64{0, 1}, PolicyNone},
		{2, []int64{1, 1_000_000_001}, PolicyNone},
		{2, []int64{1}, PolicyNone},
		{2, []int64{1, 1}, "weird"},
	} {
		if _, err := NewManager(c.n, c.caps, c.policy); reason(err) != ReasonInvalidConfig {
			t.Fatalf("config %+v: %v", c, err)
		}
	}
}

// 被拒绝的操作不改变已登记容器、掩码与分配。
func TestRejectedKeepsState(t *testing.T) {
	m, _ := NewManager(2, []int64{4, 4}, PolicyRestricted)
	mustAdmit(t, m, "a", 3, nil, 0b01, []int64{3, 0})
	snap, _ := m.Query("a")
	type tc struct {
		req int64
		ps  []Provider
	}
	for _, c := range []tc{
		{6, nil}, // 容量不足
		{3, []Provider{hints(Hint{Mask: 0b01, Preferred: true})}}, // 提示不满足（free=[1,4] 规格第二例）
	} {
		if _, err := m.Admit("b", c.req, c.ps); err == nil {
			t.Fatal("want rejection")
		}
	}
	if err := m.Release("b"); reason(err) != ReasonContainerMissing {
		t.Fatalf("b must not exist: %v", err)
	}
	q, _ := m.Query("a")
	if q.Mask != snap.Mask || !eqCPUs(q.CPUs, snap.CPUs) {
		t.Fatalf("state changed: %+v vs %+v", q, snap)
	}
}

// 并发：同一 id 的 Admit 恰有一次成功；多容器不超卖。
func TestConcurrent(t *testing.T) {
	m, _ := NewManager(4, []int64{100, 100, 100, 100}, PolicyBestEffort)
	var wg sync.WaitGroup
	var ok, fail int64
	var mu sync.Mutex
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Admit("same", 10, nil)
			mu.Lock()
			if err == nil {
				ok++
			} else {
				fail++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if ok != 1 || fail != 63 {
		t.Fatalf("ok=%d fail=%d", ok, fail)
	}

	// 多 id 并发 + Release 混合压力，最终节点分配不超过 cap。
	m2, _ := NewManager(4, []int64{500, 500, 500, 500}, PolicyBestEffort)
	for round := 0; round < 4; round++ {
		for i := 0; i < 200; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				id := "c" + itoa(i)
				_, _ = m2.Admit(id, int64(1+i%7), nil)
			}(i)
		}
		wg.Wait()
		used := make([]int64, 4)
		for i := 0; i < 200; i++ {
			a, err := m2.Query("c" + itoa(i))
			if err != nil {
				t.Fatal(err)
			}
			var sum int64
			for j, v := range a.CPUs {
				used[j] += v
				sum += v
			}
			if sum != a.Req {
				t.Fatalf("alloc sum %d != req %d", sum, a.Req)
			}
		}
		for j, u := range used {
			if u > m2.caps[j] {
				t.Fatalf("node %d overcommitted %d", j, u)
			}
		}
		for i := 0; i < 200; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_ = m2.Release("c" + itoa(i))
			}(i)
		}
		wg.Wait()
	}
	for i, f := range m2.free {
		if f != m2.caps[i] {
			t.Fatalf("free not restored node %d: %d", i, f)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
