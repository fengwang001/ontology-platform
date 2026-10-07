package ontology

import (
	"context"
	"testing"
)

// 四类失败分别暴露不同错误码。
func TestFailureCodesDistinct(t *testing.T) {
	codes := map[DecisionCode]bool{
		CodeObjectNotFound:     true,
		CodeLinkTypeNotAllowed: true,
		CodeDuplicateLink:      true,
		CodeCardinalityFull:    true,
	}
	seen := map[DecisionCode]bool{}
	for c := range codes {
		seen[c] = false
	}
	_ = seen
	if len(codes) != 4 {
		t.Fatalf("failure codes merged: %v", codes)
	}
}

// 方向不适用：Person->Person、反向用错端点类型、未知链接类型。
func TestLinkTypeNotApplicable(t *testing.T) {
	s := NewStore()
	must(t, s.RegisterObjectType(NewObjectType("Person", "人")))
	must(t, s.RegisterObjectType(NewObjectType("Org", "组织")))
	must(t, s.RegisterLinkType(NewLinkType("employs", "Person", "Org", Unlimited(), Unlimited(), []string{"role"})))
	ctx := context.Background()
	_, err := s.CreateObject(ctx, "p1", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "p2", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "o1", "Org")
	must(t, err)

	// Person -> Person 不允许。
	_, err = s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p1", HeadID: "p2",
		Discriminator: map[string]string{"role": "x"},
	})
	wantCode(t, err, CodeLinkTypeNotAllowed)

	// 反向要求 Org -> Person，用 Person -> Org 即不允许。
	_, err = s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Backward, TailID: "p1", HeadID: "o1",
		Discriminator: map[string]string{"role": "x"},
	})
	wantCode(t, err, CodeLinkTypeNotAllowed)

	// 正确的反向应当成功。
	createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Backward, TailID: "o1", HeadID: "p1",
		Discriminator: map[string]string{"role": "x"},
	})

	// 未知链接类型。
	_, err = s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "nope", Direction: Forward, TailID: "p1", HeadID: "o1",
		Discriminator: map[string]string{"role": "x"},
	})
	wantCode(t, err, CodeLinkTypeNotFound)
}

// 对象逻辑删除后创建一律 object_not_found，且优先级高于基数与重复。
func TestObjectDeletedTakesPrecedence(t *testing.T) {
	s := NewStore()
	must(t, s.RegisterObjectType(NewObjectType("Person", "人")))
	must(t, s.RegisterObjectType(NewObjectType("Org", "组织")))
	must(t, s.RegisterLinkType(NewLinkType("employs", "Person", "Org", AtMost(1), Unlimited(), []string{"role"})))
	ctx := context.Background()
	_, err := s.CreateObject(ctx, "p", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "o", "Org")
	must(t, err)
	existing := createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev"},
	})

	must(t, s.DeleteObject(ctx, "p"))

	// 同时满足：对象已删除 + 与在库链接重复 + 基数已满。
	// 必须只返回 object_not_found。
	_, err = s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev"},
	})
	wantCode(t, err, CodeObjectNotFound)

	// 头实例不存在同样优先。
	_, err = s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "ghost",
		Discriminator: map[string]string{"role": "dev"},
	})
	wantCode(t, err, CodeObjectNotFound)

	// 已有链接与计数不受逻辑删除影响（删除对象不级联撤销链接）。
	if c, _ := s.CountLinks(ctx, "employs", "p", Forward); c != 1 {
		t.Fatalf("object deletion changed link counts: %d", c)
	}
	if len(s.ActiveLinks("employs")) != 1 {
		t.Fatal("object deletion changed active link set")
	}
	_ = existing
}

// 稳定优先级：同样输入多次，错误码一致。
func TestStableFailurePrecedence(t *testing.T) {
	s := NewStore()
	must(t, s.RegisterObjectType(NewObjectType("Person", "人")))
	must(t, s.RegisterObjectType(NewObjectType("Org", "组织")))
	must(t, s.RegisterLinkType(NewLinkType("employs", "Person", "Org", AtMost(1), Unlimited(), []string{"role"})))
	ctx := context.Background()
	_, err := s.CreateObject(ctx, "p", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "o", "Org")
	must(t, err)
	createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev"},
	})

	// 重复与基数同时成立时，实现选择“重复优先”（固定且稳定）。
	for i := 0; i < 5; i++ {
		_, err := s.CreateLink(ctx, CreateLinkInput{
			LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
			Discriminator: map[string]string{"role": "dev"},
		})
		wantCode(t, err, CodeDuplicateLink)
	}
}

// 任何被拒绝的创建/删除对后续查询无可观察影响。
func TestRejectedRequestsAreInvisible(t *testing.T) {
	s := NewStore()
	must(t, s.RegisterObjectType(NewObjectType("Person", "人")))
	must(t, s.RegisterObjectType(NewObjectType("Org", "组织")))
	must(t, s.RegisterLinkType(NewLinkType("employs", "Person", "Org", AtMost(1), Unlimited(), []string{"role"})))
	ctx := context.Background()
	_, err := s.CreateObject(ctx, "p", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "o", "Org")
	must(t, err)
	createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev"},
	})

	snapshotCounts := func() []int {
		f, _ := s.CountLinks(ctx, "employs", "p", Forward)
		b, _ := s.CountLinks(ctx, "employs", "o", Backward)
		return []int{f, b}
	}
	before := snapshotCounts()
	beforeSet := len(s.ActiveLinks("employs"))

	// 基数拒绝。
	if _, err := s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "lead"},
	}); err == nil {
		t.Fatal("expected rejection")
	}
	// 重复拒绝。
	if _, err := s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev"},
	}); err == nil {
		t.Fatal("expected rejection")
	}
	// 删除不存在的链接。
	if err := s.DeleteLink(ctx, "link-999"); err == nil {
		t.Fatal("expected link not found")
	}

	after := snapshotCounts()
	afterSet := len(s.ActiveLinks("employs"))
	if before[0] != after[0] || before[1] != after[1] || beforeSet != afterSet {
		t.Fatalf("observable state changed: counts %v -> %v, set %d -> %d", before, after, beforeSet, afterSet)
	}
}
