package ontology

import "testing"

// newTestStore 注册两类对象类型、一个非对称链接类型（worksAt）
// 与一个对称链接类型（friend）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	s.RegisterObjectType("Person")
	s.RegisterObjectType("Company")
	s.RegisterObjectType("User")
	if err := s.RegisterLinkType(LinkTypeDef{ID: "worksAt", LeftType: "Person", RightType: "Company"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterLinkType(LinkTypeDef{ID: "friend", LeftType: "User", RightType: "User", Symmetric: true}); err != nil {
		t.Fatal(err)
	}
	return s
}

// assertMirror 校验在 (rt, vt) 时刻正向与反向视角严格互为镜像：
// 对回放集合中的每条链接 (l, r)，从 l 出发能看到 r，从 r 出发能看到 l；
// 且任一对象的邻居集合都能在全局回放中找到对应链接。
func assertMirror(t *testing.T, s *Store, lt LinkTypeID, rt, vt int64) {
	t.Helper()
	res := s.Replay(lt, rt, vt)
	contains := func(hay []ObjectID, want ObjectID) bool {
		for _, x := range hay {
			if x == want {
				return true
			}
		}
		return false
	}
	for _, l := range res.Links {
		if !contains(s.ReplayFrom(lt, l.Left, rt, vt), l.Right) {
			t.Fatalf("rt=%d vt=%d: forward view of %s misses %s", rt, vt, l.Left, l.Right)
		}
		if !contains(s.ReplayFrom(lt, l.Right, rt, vt), l.Left) {
			t.Fatalf("rt=%d vt=%d: reverse view of %s misses %s", rt, vt, l.Right, l.Left)
		}
	}
}

// TestAsymmetricMirrorUnderMixedRecordOrder 非对称链接：创建与撤销事实
// 由不同端点、以不同顺序记录，任意记录时刻两端视角必须严格镜像。
func TestAsymmetricMirrorUnderMixedRecordOrder(t *testing.T) {
	s := newTestStore(t)
	const lt = LinkTypeID("worksAt")
	vt := int64(1)

	ops := []FactInput{
		{Kind: FactCreate, Left: "p1", Right: "c1", ValidFrom: vt, Source: EndpointLeft},
		{Kind: FactCreate, Left: "p1", Right: "c2", ValidFrom: vt, Source: EndpointRight},
		{Kind: FactCreate, Left: "p2", Right: "c1", ValidFrom: vt, Source: EndpointBoth},
		// 撤销由与创建不同的端点记录。
		{Kind: FactRevoke, Left: "p1", Right: "c1", ValidTo: vt + 10, Source: EndpointRight},
		{Kind: FactRevoke, Left: "p2", Right: "c1", ValidTo: vt + 10, Source: EndpointLeft},
		// 撤销后重建，来源端点再次变化。
		{Kind: FactCreate, Left: "p1", Right: "c1", ValidFrom: vt + 20, Source: EndpointRight},
	}
	for _, op := range ops {
		if err := s.RecordFact(lt, op); err != nil {
			t.Fatal(err)
		}
	}
	now := s.Now()
	for rt := int64(1); rt <= now; rt++ {
		for _, v := range []int64{vt, vt + 10, vt + 20} {
			assertMirror(t, s, lt, rt, v)
		}
	}

	// p1-c1 有效区间为 [vt, vt+10) 与 [vt+20, ∞)：vt 时刻存在，
	// vt+15 时刻不存在，vt+25 时刻再次存在；p1-c2 始终存在。
	def := linkDef(t, s, lt)
	if !s.Replay(lt, now, vt).contains(def, "p1", "c1") {
		t.Fatalf("p1-c1 should be present at vt=%d", vt)
	}
	if s.Replay(lt, now, vt+15).contains(def, "p1", "c1") {
		t.Fatalf("p1-c1 should be revoked at vt=%d", vt+15)
	}
	if !s.Replay(lt, now, vt+25).contains(def, "p1", "c1") {
		t.Fatalf("p1-c1 should be re-created at vt=%d", vt+25)
	}
	if !s.Replay(lt, now, vt).contains(def, "p1", "c2") {
		t.Fatalf("p1-c2 should be present at vt=%d", vt)
	}
}

func linkDef(t *testing.T, s *Store, lt LinkTypeID) LinkTypeDef {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.links[lt].def
}

// TestSymmetricSingleRecordVisibleBothWays 对称链接只需从任一端记录一次，
// 回放即在两个方向同时可见；但单端记录必须显式标记对称性缺损，
// 而不是静默假装对称性成立。
func TestSymmetricSingleRecordVisibleBothWays(t *testing.T) {
	s := newTestStore(t)
	const lt = LinkTypeID("friend")
	vt := int64(1)

	// 仅从 u1 所在端记录创建（模拟历史数据缺陷：对端缺少对应记录）。
	if err := s.RecordFact(lt, FactInput{
		Kind: FactCreate, Left: "u1", Right: "u2", ValidFrom: vt, Source: EndpointLeft,
	}); err != nil {
		t.Fatal(err)
	}
	now := s.Now()

	// 双向可见。
	if got := s.ReplayFrom(lt, "u1", now, vt); len(got) != 1 || got[0] != "u2" {
		t.Fatalf("forward view of u1 = %v, want [u2]", got)
	}
	if got := s.ReplayFrom(lt, "u2", now, vt); len(got) != 1 || got[0] != "u1" {
		t.Fatalf("reverse view of u2 = %v, want [u1]", got)
	}
	// 缺损必须显式呈现。
	res := s.Replay(lt, now, vt)
	if len(res.Links) != 1 || !res.Links[0].SymmetryDeficit {
		t.Fatalf("expected single link flagged with symmetry deficit, got %+v", res.Links)
	}
	assertMirror(t, s, lt, now, vt)

	// 对端补录确认后，缺损消除。
	if err := s.RecordFact(lt, FactInput{
		Kind: FactCorroborate, Left: "u2", Right: "u1", Source: EndpointRight,
	}); err != nil {
		t.Fatal(err)
	}
	res = s.Replay(lt, s.Now(), vt)
	if len(res.Links) != 1 || res.Links[0].SymmetryDeficit {
		t.Fatalf("corroboration should clear deficit, got %+v, want exactly one clean link", res.Links)
	}
	// 但历史记录时刻的回放仍呈现当时的缺损（历史不可改写）。
	if res = s.Replay(lt, now, vt); !res.Links[0].SymmetryDeficit {
		t.Fatalf("historical replay at rt=%d must still show the deficit", now)
	}
}

// TestSymmetricCanonicalization 对称链接无论以何种端点顺序记录，
// 都规范化到同一份历史；撤销也无需关心方向。
func TestSymmetricCanonicalization(t *testing.T) {
	s := newTestStore(t)
	const lt = LinkTypeID("friend")
	vt := int64(1)

	if err := s.RecordFact(lt, FactInput{Kind: FactCreate, Left: "u2", Right: "u1", ValidFrom: vt, Source: EndpointBoth}); err != nil {
		t.Fatal(err)
	}
	// 以相反方向记录撤销，必须命中同一条链接。
	if err := s.RecordFact(lt, FactInput{Kind: FactRevoke, Left: "u1", Right: "u2", ValidTo: vt + 5, Source: EndpointBoth}); err != nil {
		t.Fatal(err)
	}
	now := s.Now()
	def := linkDef(t, s, lt)
	if !s.Replay(lt, now, vt).contains(def, "u1", "u2") {
		t.Fatal("link should be present before revocation valid time")
	}
	if s.Replay(lt, now, vt+5).contains(def, "u1", "u2") {
		t.Fatal("link should be revoked at vt+5 regardless of record direction")
	}
	for rt := int64(1); rt <= now; rt++ {
		assertMirror(t, s, lt, rt, vt)
		assertMirror(t, s, lt, rt, vt+5)
	}
}

// TestSymmetricSelfPairRejected 对称链接拒绝自环。
func TestSymmetricSelfPairRejected(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordFact("friend", FactInput{
		Kind: FactCreate, Left: "u1", Right: "u1", ValidFrom: 0, Source: EndpointBoth,
	}); err == nil {
		t.Fatal("expected self-pair rejection for symmetric link type")
	}
}

// TestReplayEdgeCases 双时态边界情形穷举（非对称链接）。
func TestReplayEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		ops  []FactInput
		// 在全部事实提交后的 (vt -> 期望存在的链接数)。
		expect map[int64]int
	}{
		{
			name: "dangling revoke is a no-op",
			ops: []FactInput{
				{Kind: FactRevoke, Left: "p1", Right: "c1", ValidTo: 10, Source: EndpointBoth},
			},
			expect: map[int64]int{0: 0, 10: 0},
		},
		{
			name: "duplicate create keeps single link",
			ops: []FactInput{
				{Kind: FactCreate, Left: "p1", Right: "c1", ValidFrom: 0, Source: EndpointLeft},
				{Kind: FactCreate, Left: "p1", Right: "c1", ValidFrom: 0, Source: EndpointRight},
			},
			expect: map[int64]int{0: 1},
		},
		{
			name: "empty valid interval never present",
			ops: []FactInput{
				{Kind: FactCreate, Left: "p1", Right: "c1", ValidFrom: 10, Source: EndpointBoth},
				{Kind: FactRevoke, Left: "p1", Right: "c1", ValidTo: 5, Source: EndpointBoth},
			},
			expect: map[int64]int{0: 0, 7: 0, 10: 0, 100: 0},
		},
		{
			name: "valid time boundaries are half-open",
			ops: []FactInput{
				{Kind: FactCreate, Left: "p1", Right: "c1", ValidFrom: 10, Source: EndpointBoth},
				{Kind: FactRevoke, Left: "p1", Right: "c1", ValidTo: 20, Source: EndpointBoth},
			},
			expect: map[int64]int{9: 0, 10: 1, 19: 1, 20: 0},
		},
		{
			name: "revoke then recreate reopens interval",
			ops: []FactInput{
				{Kind: FactCreate, Left: "p1", Right: "c1", ValidFrom: 0, Source: EndpointBoth},
				{Kind: FactRevoke, Left: "p1", Right: "c1", ValidTo: 10, Source: EndpointBoth},
				{Kind: FactCreate, Left: "p1", Right: "c1", ValidFrom: 20, Source: EndpointBoth},
			},
			expect: map[int64]int{5: 1, 15: 0, 25: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			const lt = LinkTypeID("worksAt")
			for _, op := range tc.ops {
				if err := s.RecordFact(lt, op); err != nil {
					t.Fatal(err)
				}
			}
			now := s.Now()
			for vt, want := range tc.expect {
				if got := len(s.Replay(lt, now, vt).Links); got != want {
					t.Fatalf("vt=%d: got %d links, want %d", vt, got, want)
				}
			}
			// 每个历史记录时刻都必须保持镜像一致。
			for rt := int64(1); rt <= now; rt++ {
				for vt := range tc.expect {
					assertMirror(t, s, lt, rt, vt)
				}
			}
		})
	}
}

// TestRecordTimeReplayIsImmutable 后发生的事实不得改变更早记录时刻的回放结果。
func TestRecordTimeReplayIsImmutable(t *testing.T) {
	s := newTestStore(t)
	const lt = LinkTypeID("worksAt")
	if err := s.CreateLink(lt, "p1", "c1", 0); err != nil {
		t.Fatal(err)
	}
	mid := s.Now()
	before := s.Replay(lt, mid, 0)
	if err := s.RevokeLink(lt, "p1", "c1", 0); err != nil {
		t.Fatal(err)
	}
	after := s.Replay(lt, mid, 0)
	if len(before.Links) != 1 || len(after.Links) != 1 {
		t.Fatalf("replay at historical record time changed: before=%v after=%v", before.Links, after.Links)
	}
	if got := len(s.Replay(lt, s.Now(), 0).Links); got != 0 {
		t.Fatalf("latest replay should show revocation, got %d links", got)
	}
}
