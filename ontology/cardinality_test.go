package ontology

import (
	"context"
	"testing"
)

func setupPairStore(t *testing.T, fwd, back Cardinality) (*Store, string, string, string) {
	t.Helper()
	s := NewStore()
	must(t, s.RegisterObjectType(NewObjectType("Person", "人")))
	must(t, s.RegisterObjectType(NewObjectType("Org", "组织")))
	lt := NewLinkType("employs", "Person", "Org", fwd, back, []string{"role"})
	must(t, s.RegisterLinkType(lt))
	ctx := context.Background()
	_, err := s.CreateObject(ctx, "p", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "o", "Org")
	must(t, err)
	return s, "p", "o", "employs"
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantCode(t *testing.T, err error, code DecisionCode) {
	t.Helper()
	de, ok := AsDecisionError(err)
	if !ok {
		t.Fatalf("expected DecisionError, got %T: %v", err, err)
	}
	if de.Code() != code {
		t.Fatalf("expected code %s, got %s (%v)", code, de.Code(), err)
	}
	if len(de.Basis()) == 0 {
		t.Fatalf("error of code %s carries no decision basis", code)
	}
}

func createOK(t *testing.T, s *Store, in CreateLinkInput) *Link {
	t.Helper()
	l, err := s.CreateLink(context.Background(), in)
	if err != nil {
		t.Fatalf("create %+v: unexpected error: %v", in, err)
	}
	return l
}

// 双方向上限在 {0,1,不限} 上的 3x3 组合边界。
func TestCardinalityBoundaryMatrix(t *testing.T) {
	caps := []struct {
		name string
		cap  Cardinality
	}{
		{"zero", AtMost(0)},
		{"one", AtMost(1)},
		{"unlimited", Unlimited()},
	}

	for _, fwd := range caps {
		for _, back := range caps {
			t.Run("fwd="+fwd.name+"_back="+back.name, func(t *testing.T) {
				s, p, o, ltID := setupPairStore(t, fwd.cap, back.cap)
				ctx := context.Background()

				fwdLink, fwdErr := s.CreateLink(ctx, CreateLinkInput{
					LinkTypeID: ltID, Direction: Forward, TailID: p, HeadID: o,
					Discriminator: map[string]string{"role": "fwd"},
				})
				if fwdErr != nil {
					wantCode(t, fwdErr, CodeCardinalityFull)
					if fwd.name != "zero" {
						t.Fatalf("forward create rejected in non-zero case: %v", fwdErr)
					}
				}

				backLink, backErr := s.CreateLink(ctx, CreateLinkInput{
					LinkTypeID: ltID, Direction: Backward, TailID: o, HeadID: p,
					Discriminator: map[string]string{"role": "back"},
				})
				if backErr != nil {
					wantCode(t, backErr, CodeCardinalityFull)
					if back.name != "zero" {
						t.Fatalf("backward create rejected in non-zero case: %v", backErr)
					}
				}

				gotFwd, err := s.CountLinks(ctx, ltID, p, Forward)
				must(t, err)
				gotBack, err := s.CountLinks(ctx, ltID, o, Backward)
				must(t, err)

				wantFwd := 1
				if fwd.name == "zero" {
					wantFwd = 0
				}
				wantBack := 1
				if back.name == "zero" {
					wantBack = 0
				}
				if gotFwd != wantFwd || gotBack != wantBack {
					t.Fatalf("counts fwd=%d(want %d) back=%d(want %d)", gotFwd, wantFwd, gotBack, wantBack)
				}

				// 链接集合数量与计数一致。
				active := s.ActiveLinks(ltID)
				if len(active) != wantFwd+wantBack {
					t.Fatalf("active links=%d want %d", len(active), wantFwd+wantBack)
				}

				// 拒绝不能改变计数；对 cap=0/1 的方向再压一次，必须拒绝。
				if fwd.name != "unlimited" {
					_, err := s.CreateLink(ctx, CreateLinkInput{
						LinkTypeID: ltID, Direction: Forward, TailID: p, HeadID: o,
						Discriminator: map[string]string{"role": "fwd-extra"},
					})
					wantCode(t, err, CodeCardinalityFull)
					got, _ := s.CountLinks(ctx, ltID, p, Forward)
					if got != wantFwd {
						t.Fatalf("rejected forward create changed count: %d -> %d", wantFwd, got)
					}
				}
				if back.name != "unlimited" {
					_, err := s.CreateLink(ctx, CreateLinkInput{
						LinkTypeID: ltID, Direction: Backward, TailID: o, HeadID: p,
						Discriminator: map[string]string{"role": "back-extra"},
					})
					wantCode(t, err, CodeCardinalityFull)
					got, _ := s.CountLinks(ctx, ltID, o, Backward)
					if got != wantBack {
						t.Fatalf("rejected backward create changed count: %d -> %d", wantBack, got)
					}
				}

				// 一个方向的拒绝不得影响另一方向的判断：两侧计数独立。
				if fwdErr == nil {
					_ = fwdLink
				}
				if backErr == nil {
					_ = backLink
				}
			})
		}
	}
}

// cap=1：撤销后名额立即释放，可再建；两个方向互不干扰。
func TestCardinalityReleaseAfterDelete(t *testing.T) {
	s, p, o, ltID := setupPairStore(t, AtMost(1), AtMost(1))
	ctx := context.Background()
	first := createOK(t, s, CreateLinkInput{
		LinkTypeID: ltID, Direction: Forward, TailID: p, HeadID: o,
		Discriminator: map[string]string{"role": "a"},
	})
	// 反向独立占用一个名额。
	back := createOK(t, s, CreateLinkInput{
		LinkTypeID: ltID, Direction: Backward, TailID: o, HeadID: p,
		Discriminator: map[string]string{"role": "b"},
	})

	if _, err := s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: ltID, Direction: Forward, TailID: p, HeadID: o,
		Discriminator: map[string]string{"role": "c"},
	}); err == nil {
		t.Fatal("expected cardinality rejection")
	}

	must(t, s.DeleteLink(ctx, first.ID()))
	// 反向计数不受正向撤销影响。
	backCount, _ := s.CountLinks(ctx, ltID, o, Backward)
	if backCount != 1 {
		t.Fatalf("backward count changed after forward delete: %d", backCount)
	}
	// 名额已释放，可再建一条。
	createOK(t, s, CreateLinkInput{
		LinkTypeID: ltID, Direction: Forward, TailID: p, HeadID: o,
		Discriminator: map[string]string{"role": "c"},
	})
	must(t, s.DeleteLink(ctx, back.ID()))
}

// 基数按“尾实例”分别计算：不同尾实例不互相挤占。
func TestCardinalityPerTailInstance(t *testing.T) {
	s := NewStore()
	must(t, s.RegisterObjectType(NewObjectType("Person", "人")))
	must(t, s.RegisterObjectType(NewObjectType("Org", "组织")))
	must(t, s.RegisterLinkType(NewLinkType("employs", "Person", "Org", AtMost(1), Unlimited(), []string{"role"})))
	ctx := context.Background()
	_, err := s.CreateObject(ctx, "p1", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "p2", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "o1", "Org")
	must(t, err)
	createOK(t, s, CreateLinkInput{LinkTypeID: "employs", Direction: Forward, TailID: "p1", HeadID: "o1", Discriminator: map[string]string{"role": "x"}})
	createOK(t, s, CreateLinkInput{LinkTypeID: "employs", Direction: Forward, TailID: "p2", HeadID: "o1", Discriminator: map[string]string{"role": "x"}})
	c1, _ := s.CountLinks(ctx, "employs", "p1", Forward)
	c2, _ := s.CountLinks(ctx, "employs", "p2", Forward)
	if c1 != 1 || c2 != 1 {
		t.Fatalf("per-tail counts wrong: %d %d", c1, c2)
	}
}
