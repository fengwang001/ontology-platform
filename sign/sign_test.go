package sign_test

import (
	"errors"
	"testing"

	"ontology/record"
	"ontology/sign"
)

func setup(t *testing.T) *record.Store {
	t.Helper()
	s := record.New(4320, 60)
	must(t, s.AddUser("r", "内科", 1, nil))
	must(t, s.AddUser("s", "内科", 2, nil))
	must(t, s.AddUser("k", "外科", 3, nil))
	must(t, s.OpenEnc("e"))
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSignInvalidatedByEdit(t *testing.T) {
	s := setup(t)
	must(t, s.Create(100, "r", "e", "d", "c"))
	must(t, sign.Sign(s, 200, "r", "d"))
	di, _ := s.DocInfo("d")
	if di.Status != record.StatusSigned {
		t.Fatalf("status=%d want signed", di.Status)
	}
	must(t, s.Edit(300, "r", "d", "c2"))
	di, _ = s.DocInfo("d")
	if di.Status != record.StatusDraft || di.VersionCount != 2 || di.SignTS != 0 {
		t.Fatalf("after edit: %+v", di)
	}
}

func TestLevel2AuthorNoCosign(t *testing.T) {
	s := setup(t)
	must(t, s.Create(100, "s", "e", "d", "c"))
	must(t, sign.Sign(s, 200, "s", "d"))
	di, _ := s.DocInfo("d")
	if di.Status != record.StatusCosigned || di.CosignBy != "" {
		t.Fatalf("level2 author sign: %+v", di)
	}
	// 已审签下 Edit 报状态不符。
	if err := s.Edit(300, "s", "d", "c2"); !errors.Is(err, record.ErrState) {
		t.Fatalf("edit cosigned = %v want state", err)
	}
}

func TestCosignReturnFlow(t *testing.T) {
	s := setup(t)
	must(t, s.Create(100, "r", "e", "d", "c"))
	// 草稿不可审签
	if err := sign.Cosign(s, 150, "s", "d"); !errors.Is(err, record.ErrState) {
		t.Fatalf("cosign draft = %v", err)
	}
	must(t, sign.Sign(s, 200, "r", "d"))
	// 作者本人不可审签
	if err := sign.Cosign(s, 250, "r", "d"); !errors.Is(err, record.ErrPermission) {
		t.Fatalf("self cosign = %v", err)
	}
	// 跨科室不可审签
	if err := sign.Cosign(s, 260, "k", "d"); !errors.Is(err, record.ErrPermission) {
		t.Fatalf("other-dept cosign = %v", err)
	}
	must(t, sign.Cosign(s, 300, "s", "d"))
	di, _ := s.DocInfo("d")
	if di.Status != record.StatusCosigned || di.CosignBy != "s" {
		t.Fatalf("cosign: %+v", di)
	}
}

func TestReturn(t *testing.T) {
	s := setup(t)
	must(t, s.Create(100, "r", "e", "d", "c"))
	must(t, sign.Sign(s, 200, "r", "d"))
	// k 无权限
	if err := sign.Return(s, 250, "k", "d"); !errors.Is(err, record.ErrPermission) {
		t.Fatalf("k return = %v want permission", err)
	}
	must(t, sign.Return(s, 300, "s", "d"))
	di, _ := s.DocInfo("d")
	if di.Status != record.StatusDraft {
		t.Fatalf("return status=%d", di.Status)
	}
}

func TestLateSignAfterSeal(t *testing.T) {
	s := setup(t)
	must(t, s.Discharge(1000, "e"))
	must(t, s.Create(1100, "r", "e", "d2", "c"))
	// 5320 的 Sign 开头自动封存，草稿仍可迟签。
	must(t, sign.Sign(s, 5320, "r", "d2"))
	di, _ := s.DocInfo("d2")
	if !di.Sealed || di.Gen != 1 || !di.SignLate || di.Status != record.StatusSigned {
		t.Fatalf("late sign: %+v", di)
	}
	// 封存后 Return 报状态不符。
	if err := sign.Return(s, 5330, "s", "d2"); !errors.Is(err, record.ErrState) {
		t.Fatalf("return sealed = %v want state", err)
	}
	// 迟签审签后移出缺陷。
	defs, err := s.Defects(5330)
	must(t, err)
	if len(defs) != 1 || defs[0] != "d2" {
		t.Fatalf("defects before cosign=%v", defs)
	}
	must(t, sign.Cosign(s, 5340, "s", "d2"))
	defs, _ = s.Defects(5340)
	if len(defs) != 0 {
		t.Fatalf("defects after cosign=%v", defs)
	}
	di, _ = s.DocInfo("d2")
	if !di.CosignLate {
		t.Fatalf("cosign not marked late: %+v", di)
	}
}
