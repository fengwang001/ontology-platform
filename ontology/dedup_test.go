package ontology

import (
	"context"
	"testing"
)

func dedupStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	must(t, s.RegisterObjectType(NewObjectType("Person", "人")))
	must(t, s.RegisterObjectType(NewObjectType("Org", "组织")))
	// 两个区分属性，覆盖多属性组合。
	must(t, s.RegisterLinkType(NewLinkType("employs", "Person", "Org", Unlimited(), Unlimited(), []string{"role", "since"})))
	// 第二个链接类型，作用于同一对实例，用于验证类型间独立性。
	must(t, s.RegisterLinkType(NewLinkType("audits", "Person", "Org", Unlimited(), Unlimited(), []string{"role"})))
	ctx := context.Background()
	_, err := s.CreateObject(ctx, "p", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "o", "Org")
	must(t, err)
	return s
}

// 区分属性组合完全相同 => 重复；重复判定不消耗基数名额。
func TestDuplicateDoesNotConsumeQuota(t *testing.T) {
	s := NewStore()
	must(t, s.RegisterObjectType(NewObjectType("Person", "人")))
	must(t, s.RegisterObjectType(NewObjectType("Org", "组织")))
	must(t, s.RegisterLinkType(NewLinkType("employs", "Person", "Org", AtMost(1), AtMost(1), []string{"role"})))
	ctx := context.Background()
	_, err := s.CreateObject(ctx, "p", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "o", "Org")
	must(t, err)

	first := createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev"},
	})
	_, err = s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev"},
	})
	wantCode(t, err, CodeDuplicateLink)

	// 计数仍然是 1，且不同组合仍因 cap=1 被基数拒绝
	// （证明重复请求没有“补占”名额，也没有额外加计数）。
	count, _ := s.CountLinks(ctx, "employs", "p", Forward)
	if count != 1 {
		t.Fatalf("duplicate changed count: %d", count)
	}
	_, err = s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "lead"},
	})
	wantCode(t, err, CodeCardinalityFull)
	if len(s.ActiveLinks("employs")) != 1 {
		t.Fatalf("rejected requests altered active set: %d", len(s.ActiveLinks("employs")))
	}
	if first.ID() == "" {
		t.Fatal("accepted link has empty id")
	}
}

// 属性声明顺序/提供顺序不同但组合相同 => 仍判重复；任一值不同 => 新链接。
func TestDiscriminatorEquivalenceAndDifference(t *testing.T) {
	s := dedupStore(t)
	ctx := context.Background()
	createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev", "since": "2024"},
	})
	_, err := s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"since": "2024", "role": "dev"}, // 顺序不同
	})
	wantCode(t, err, CodeDuplicateLink)

	// 缺失一个区分属性 => 组合不同（空值 != 有值），应被视为新链接。
	other := createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev"},
	})
	third := createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev", "since": "2025"},
	})
	if other.ID() == third.ID() {
		t.Fatal("different discriminator tuples must yield different links")
	}
}

// 已撤销取值的空位可重新占用；新链接是不同实例且不继承派生状态。
func TestRevokedTupleReusableWithoutDerivedState(t *testing.T) {
	s := dedupStore(t)
	ctx := context.Background()
	old := createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev", "since": "2024"},
	})
	must(t, s.AnnotateLink(ctx, old.ID(), "ticket", "ONTO-1"))
	if v, ok := s.Annotation(old.ID(), "ticket"); !ok || v != "ONTO-1" {
		t.Fatalf("annotation not stored: %q %v", v, ok)
	}

	must(t, s.DeleteLink(ctx, old.ID()))

	// 同组合重占成功，且 ID 不同。
	fresh := createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"since": "2024", "role": "dev"},
	})
	if fresh.ID() == old.ID() {
		t.Fatalf("reoccupied link reused revoked link id %q", old.ID())
	}
	if v, ok := s.Annotation(fresh.ID(), "ticket"); ok {
		t.Fatalf("new link inherited revoked link derived state: %q", v)
	}
	if _, ok := s.Annotation(old.ID(), "ticket"); ok {
		t.Fatal("revoked link annotations still observable")
	}

	// 已撤销与未撤销取值混合时的重复判定：
	// 未撤销的组合仍报重复；已撤销组合不再报重复而是新建。
	live := createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "pm", "since": "2024"},
	})
	_, err := s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "pm", "since": "2024"},
	})
	wantCode(t, err, CodeDuplicateLink)

	// 先撤销 fresh，再占同一组合：依然得到全新 ID（与 old、fresh 都不同）。
	must(t, s.DeleteLink(ctx, fresh.ID()))
	retry := createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev", "since": "2024"}, // old 的组合
	})
	if retry.ID() == old.ID() || retry.ID() == fresh.ID() {
		t.Fatal("recreate after revoke must always mint a new instance id")
	}
	_ = live
}

// 跨链接类型：基数与重复判定相互独立，同一区分属性值不互相抑制。
func TestLinkTypesIndependent(t *testing.T) {
	s := dedupStore(t)
	ctx := context.Background()
	in := func(lt string) CreateLinkInput {
		return CreateLinkInput{
			LinkTypeID: lt, Direction: Forward, TailID: "p", HeadID: "o",
			Discriminator: map[string]string{"role": "dev"},
		}
	}
	a1 := createOK(t, s, in("employs"))
	a2 := createOK(t, s, in("audits"))

	// 各自类型内重复判重。
	if _, err := s.CreateLink(ctx, in("employs")); err == nil || AsDecisionErrorMust(err) != CodeDuplicateLink {
		t.Fatalf("employs duplicate expected, got %v", err)
	}
	if _, err := s.CreateLink(ctx, in("audits")); err == nil || AsDecisionErrorMust(err) != CodeDuplicateLink {
		t.Fatalf("audits duplicate expected, got %v", err)
	}

	// 撤销一个链接类型不影响另一个类型的计数与在库集合。
	must(t, s.DeleteLink(ctx, a1.ID()))
	if c, _ := s.CountLinks(ctx, "audits", "p", Forward); c != 1 {
		t.Fatalf("audits count affected by employs revoke: %d", c)
	}
	if got := s.ActiveLinks("audits"); len(got) != 1 || got[0].ID() != a2.ID() {
		t.Fatalf("audits active set affected by other type: %+v", got)
	}
	if c, _ := s.CountLinks(ctx, "employs", "p", Forward); c != 0 {
		t.Fatalf("employs count after own revoke: %d", c)
	}
	// 共享取值在 employs 内已释放，可再建；audits 仍然报自己的重复。
	createOK(t, s, in("employs"))
	if _, err := s.CreateLink(ctx, in("audits")); err == nil {
		t.Fatal("audits duplicate must still be detected independently")
	}
}

func AsDecisionErrorMust(err error) DecisionCode {
	de, ok := AsDecisionError(err)
	if !ok {
		return ""
	}
	return de.Code()
}
