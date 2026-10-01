package numa

import "testing"

func hp() ProviderHints { return ProviderHints{NoPreference: true} }

func hs(h ...Hint) ProviderHints { return ProviderHints{Hints: h} }

func eqAlloc(a, b []int64) bool {
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

func mustAdmit(t *testing.T, m *Manager, id string, req int64, provs []ProviderHints, mask uint64, pref bool, alloc []int64) {
	t.Helper()
	res, err := m.Admit(id, req, provs)
	if err != nil {
		t.Fatalf("Admit(%s) unexpected error: %v", id, err)
	}
	if res.Mask != mask || res.Preferred != pref || !eqAlloc(res.Allocation, alloc) {
		t.Fatalf("Admit(%s) = mask=%b pref=%v alloc=%v, want mask=%b pref=%v alloc=%v",
			id, res.Mask, res.Preferred, res.Allocation, mask, pref, alloc)
	}
	t.Logf("ADMIT id=%s req=%d provs=%v => mask=%0*b pref=%v alloc=%v",
		id, req, provs, m.n, res.Mask, res.Preferred, res.Allocation)
}

func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if err != want {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestNoPreferenceVsEmptyList(t *testing.T) {
	m, err := NewManager(2, []int64{4, 4}, PolicyRestricted)
	if err != nil {
		t.Fatal(err)
	}
	mustAdmit(t, m, "np", 2, []ProviderHints{hp()}, 0b01, true, []int64{2, 0})

	_, err = m.Admit("empty", 2, []ProviderHints{hs()})
	mustErr(t, err, ErrHintNotSatisfied)
	t.Logf("ADMIT id=empty req=2 provs=[empty-list] => REJECT %v", err)
}

func TestZNarrowing(t *testing.T) {
	lists := [][]Hint{
		{{Mask: 0b001, Preferred: true}, {Mask: 0b011, Preferred: true}},
		{{Mask: 0b011, Preferred: true}},
	}
	got := mergeHints(lists)
	want := map[uint64]bool{0b001: true, 0b011: false}
	if len(got) != 2 {
		t.Fatalf("candidates = %v", got)
	}
	for _, c := range got {
		if c.Preferred != want[c.Mask] {
			t.Fatalf("mask=%b pref=%v, want %v: %v", c.Mask, c.Preferred, want[c.Mask], got)
		}
	}
	t.Logf("Z narrowing: in=%v => %v", lists, got)
}

func TestTieSmallerMask(t *testing.T) {
	cands := []Hint{{Mask: 0b100, Preferred: true}, {Mask: 0b010, Preferred: true}}
	best := bestHint(cands, 0b111)
	if best.Mask != 0b010 || !best.Preferred {
		t.Fatalf("best = %b,%v", best.Mask, best.Preferred)
	}
	t.Logf("tie: %v => mask=%b pref=%v", cands, best.Mask, best.Preferred)
}

// 所有掩码相与为空：无候选，最优 (full,false)，restricted 拒绝、best-effort 接受。
func TestAllANDEmpty(t *testing.T) {
	newM := func(p Policy) *Manager {
		m, err := NewManager(2, []int64{1, 4}, p)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	provs := []ProviderHints{hs(Hint{Mask: 0b01, Preferred: true})}

	m := newM(PolicyRestricted)
	_, err := m.Admit("c", 3, provs)
	mustErr(t, err, ErrHintNotSatisfied)
	t.Logf("restricted all-and-empty => REJECT %v", err)

	m = newM(PolicyBestEffort)
	mustAdmit(t, m, "c", 3, provs, 0b01, false, []int64{1, 2})
}

// single-numa-node：非单比特提示被丢弃；丢弃后为空按空列表处理。
func TestSingleNUMANodeDiscard(t *testing.T) {
	// 只有 11 可行，内置 [(11,false)] 多位被丢弃 -> [(11,false)]；
	// 外部多位提示同样丢弃 -> [(11,false)]，相与 (11,false)，拒绝。
	m, err := NewManager(2, []int64{1, 1}, PolicySingleNUMANode)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Admit("c", 2, []ProviderHints{hs(Hint{Mask: 0b11, Preferred: true})})
	mustErr(t, err, ErrHintNotSatisfied)
	t.Logf("single discard-to-empty => REJECT %v", err)

	// 无偏好归一为 (full,true)，不经过位数丢弃；与内置可行单比特相与后仍 preferred。
	m2, err := NewManager(2, []int64{4, 4}, PolicySingleNUMANode)
	if err != nil {
		t.Fatal(err)
	}
	mustAdmit(t, m2, "np", 3, []ProviderHints{hp()}, 0b01, true, []int64{3, 0})
}

// 外部提供者为零个：完全由内置提示决定。
func TestZeroExternalProviders(t *testing.T) {
	m, err := NewManager(2, []int64{4, 4}, PolicyRestricted)
	if err != nil {
		t.Fatal(err)
	}
	mustAdmit(t, m, "a", 3, nil, 0b01, true, []int64{3, 0})
	mustAdmit(t, m, "b", 3, nil, 0b10, true, []int64{0, 3})
	mustAdmit(t, m, "c", 2, nil, 0b11, true, []int64{1, 1})
}

// 内置提示随空闲量变化；Release 后恢复；Zc 随空闲量增大而减小。
func TestBuiltinEvolutionAndRelease(t *testing.T) {
	m, err := NewManager(2, []int64{4, 4}, PolicyRestricted)
	if err != nil {
		t.Fatal(err)
	}
	// req=4：01 单独即可满足（Zc=1），最优 (01,true)。
	mustAdmit(t, m, "a", 4, nil, 0b01, true, []int64{4, 0})
	// free=[0,4]，req=4：10 可行且最窄。
	mustAdmit(t, m, "b", 4, nil, 0b10, true, []int64{0, 4})
	_, err = m.Admit("c", 1, nil)
	mustErr(t, err, ErrInsufficientCapacity)
	if err := m.Release("a"); err != nil {
		t.Fatal(err)
	}
	// free=[4,0] 恢复：01 可行。
	mustAdmit(t, m, "c", 4, nil, 0b01, true, []int64{4, 0})
}

// 掩码内不足时溢出到掩码外节点，按节点号升序取。
func TestOverflowOutsideMask(t *testing.T) {
	m, err := NewManager(3, []int64{4, 4, 4}, PolicyBestEffort)
	if err != nil {
		t.Fatal(err)
	}
	// 先占掉节点 0 三单位，使 free=[1,4,4]。
	mustAdmit(t, m, "a", 3, []ProviderHints{hs(Hint{Mask: 0b001, Preferred: true})}, 0b001, true, []int64{3, 0, 0})
	// 外部坚持 001：与可行内置掩码 011 相与得 (001,true)；001 内只有 1 空闲，
	// 结果掩码为 001，不足部分溢出到掩码外节点，按节点号升序 -> [1,4,0]。
	mustAdmit(t, m, "b", 5, []ProviderHints{hs(Hint{Mask: 0b001, Preferred: true})}, 0b001, true, []int64{1, 4, 0})

	// 掩码为 010、free=[0,4,4] 时，掩码内取 4，溢出到掩码外节点 2 取 1。
	m2, _ := NewManager(3, []int64{2, 4, 4}, PolicyBestEffort)
	mustAdmit(t, m2, "a", 2, nil, 0b001, true, []int64{2, 0, 0})
	mustAdmit(t, m2, "b", 5, []ProviderHints{hs(Hint{Mask: 0b010, Preferred: false})},
		0b010, false, []int64{0, 4, 1})
}

// 组合数边界：恰为 10^5 通过，10^5+1 拒绝；n=1 时内置列表长度为 1。
func TestCombinationLimits(t *testing.T) {
	mk := func(extra int) (*Manager, []ProviderHints) {
		m, err := NewManager(1, []int64{1}, PolicyBestEffort)
		if err != nil {
			t.Fatal(err)
		}
		hints := make([]Hint, extra)
		for i := range hints {
			hints[i] = Hint{Mask: 1, Preferred: false}
		}
		return m, []ProviderHints{{Hints: hints}}
	}

	m, provs := mk(100000)
	mustAdmit(t, m, "ok", 1, provs, 1, false, []int64{1})

	m, provs = mk(100001)
	_, err := m.Admit("no", 1, provs)
	mustErr(t, err, ErrTooManyCombinations)
	t.Logf("combos=%d => REJECT %v", 100001, err)

	// 内置列表长度计入：n=2、req=1 时内置 3 条；外部 40000 条 => 120000 拒绝。
	m2, err := NewManager(2, []int64{4, 4}, PolicyBestEffort)
	if err != nil {
		t.Fatal(err)
	}
	hints := make([]Hint, 40000)
	for i := range hints {
		hints[i] = Hint{Mask: 0b11, Preferred: false}
	}
	_, err = m2.Admit("c", 1, []ProviderHints{{Hints: hints}})
	mustErr(t, err, ErrTooManyCombinations)
	// 33333*3 = 99999 通过组合检查。
	hints99 := make([]Hint, 33333)
	for i := range hints99 {
		hints99[i] = Hint{Mask: 0b11, Preferred: false}
	}
	m3, _ := NewManager(2, []int64{4, 4}, PolicyBestEffort)
	mustAdmit(t, m3, "c", 1, []ProviderHints{{Hints: hints99}}, 0b01, false, []int64{1, 0})
}

// none 策略忽略提示、不做组合检查，但仍检查容量并按全掩码分配。
func TestNonePolicy(t *testing.T) {
	m, err := NewManager(2, []int64{4, 4}, PolicyNone)
	if err != nil {
		t.Fatal(err)
	}
	hints := make([]Hint, 200000)
	for i := range hints {
		hints[i] = Hint{Mask: 0b01, Preferred: false}
	}
	mustAdmit(t, m, "a", 3, []ProviderHints{{Hints: hints}}, 0b11, true, []int64{3, 0})
	mustAdmit(t, m, "b", 3,
		[]ProviderHints{hs(Hint{Mask: 0b01, Preferred: true}), hs(Hint{Mask: 0b10, Preferred: true})},
		0b11, true, []int64{1, 2})
	_, err = m.Admit("c", 3, nil)
	mustErr(t, err, ErrInsufficientCapacity)
}

// 拒绝原因先后：参数非法 -> 已存在 -> 容量不足 -> 组合过多 -> 提示不满足。
func TestRejectOrder(t *testing.T) {
	m, err := NewManager(2, []int64{1, 1}, PolicyRestricted)
	if err != nil {
		t.Fatal(err)
	}
	mustAdmit(t, m, "dup", 1, nil, 0b01, true, []int64{1, 0})
	// 参数非法优先于已存在（掩码 0 同时非法）。
	if _, err := m.Admit("dup", 0, []ProviderHints{hs(Hint{Mask: 0})}); err != ErrInvalidArgument {
		t.Fatalf("got %v", err)
	}
	if _, err := m.Admit("dup", 1, nil); err != ErrContainerExists {
		t.Fatalf("got %v", err)
	}
	big := make([]Hint, 200000)
	for i := range big {
		big[i] = Hint{Mask: 0b11, Preferred: false}
	}
	// 容量不足优先于组合过多。
	if _, err := m.Admit("x", 2, []ProviderHints{{Hints: big}}); err != ErrInsufficientCapacity {
		t.Fatalf("got %v", err)
	}
	// 容量足够时组合过多优先于提示不满足。
	m2, _ := NewManager(2, []int64{4, 4}, PolicyRestricted)
	if _, err := m2.Admit("x", 1, []ProviderHints{{Hints: big}}); err != ErrTooManyCombinations {
		t.Fatalf("got %v", err)
	}
	if _, err := m2.Admit("y", 1, []ProviderHints{hs(Hint{Mask: 0b100})}); err != ErrInvalidArgument {
		t.Fatalf("got %v", err)
	}
	if err := m.Release("missing"); err != ErrReleaseNotFound {
		t.Fatalf("got %v", err)
	}
	if _, err := m.Query("missing"); err != ErrQueryNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		n      int
		caps   []int64
		policy Policy
	}{
		{0, []int64{1}, PolicyNone},
		{9, make([]int64, 9), PolicyNone},
		{2, []int64{1}, PolicyNone},
		{2, []int64{0, 1}, PolicyNone},
		{2, []int64{1, 1_000_000_001}, PolicyNone},
		{2, []int64{1, 1}, Policy("weird")},
	}
	for _, c := range cases {
		if _, err := NewManager(c.n, c.caps, c.policy); err != ErrInvalidConfig {
			t.Fatalf("config %+v: got %v", c, err)
		}
	}
}

// 被拒绝的操作不改变任何登记状态与空闲量。
func TestRejectionLeavesStateUntouched(t *testing.T) {
	m, _ := NewManager(2, []int64{4, 4}, PolicyRestricted)
	mustAdmit(t, m, "a", 3, nil, 0b01, true, []int64{3, 0})
	before, _ := m.Query("a")

	if _, err := m.Admit("a", 1, nil); err != ErrContainerExists {
		t.Fatalf("got %v", err)
	}
	if _, err := m.Admit("b", 6, nil); err != ErrInsufficientCapacity {
		t.Fatalf("got %v", err)
	}
	if _, err := m.Admit("b", 1, []ProviderHints{hs(Hint{Mask: 0b01})}); err != ErrHintNotSatisfied {
		t.Fatalf("got %v", err)
	}
	after, _ := m.Query("a")
	if after.Mask != before.Mask || !eqAlloc(after.Allocation, before.Allocation) {
		t.Fatalf("state changed: before=%+v after=%+v", before, after)
	}
	if _, err := m.Admit("b", 5, nil); err != nil {
		t.Fatalf("free pool altered: %v", err)
	}
}
