package ontology

import (
	"context"
	"strings"
	"testing"
)

// 每次判定都要记录输入、判定依据与结果。
func TestAuditRecordsInputsBasisAndResults(t *testing.T) {
	s := NewStore()
	must(t, s.RegisterObjectType(NewObjectType("Person", "人")))
	must(t, s.RegisterObjectType(NewObjectType("Org", "组织")))
	must(t, s.RegisterLinkType(NewLinkType("employs", "Person", "Org", AtMost(0), Unlimited(), []string{"role"})))
	ctx := context.Background()
	_, err := s.CreateObject(ctx, "p", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "o", "Org")
	must(t, err)

	_, _ = s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev"},
	})
	if _, err := s.CreateLink(ctx, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "ghost",
		Discriminator: map[string]string{"role": "dev"},
	}); err == nil {
		t.Fatal("expected object_not_found")
	}

	log := s.AuditLog()
	if len(log) != 2 {
		t.Fatalf("audit entries=%d want 2", len(log))
	}

	r0 := log[0]
	if r0.Seq != 1 || r0.Result != CodeCardinalityFull || r0.Operation != "create_link" {
		t.Fatalf("record0 wrong: %+v", r0)
	}
	if !strings.Contains(r0.Input, `tail="p"`) || !strings.Contains(r0.Input, "role=dev") {
		t.Fatalf("record0 missing input: %q", r0.Input)
	}
	if len(r0.Basis) == 0 || !joinedContains(r0.Basis, "cap") {
		t.Fatalf("record0 missing cardinality basis: %v", r0.Basis)
	}

	r1 := log[1]
	if r1.Seq != 2 || r1.Result != CodeObjectNotFound {
		t.Fatalf("record1 wrong: %+v", r1)
	}
	if !joinedContains(r1.Basis, "precedence") {
		t.Fatalf("record1 missing precedence basis: %v", r1.Basis)
	}

	// 审计副本与内部状态隔离。
	log[0].Result = CodeAccepted
	again := s.AuditLog()
	if again[0].Result != CodeCardinalityFull {
		t.Fatal("audit log was mutated through returned copy")
	}
}

// 成功创建与删除也在审计中，Seq 严格全序递增。
func TestAuditAcceptedPath(t *testing.T) {
	s := NewStore()
	must(t, s.RegisterObjectType(NewObjectType("Person", "人")))
	must(t, s.RegisterObjectType(NewObjectType("Org", "组织")))
	must(t, s.RegisterLinkType(NewLinkType("employs", "Person", "Org", Unlimited(), Unlimited(), []string{"role"})))
	ctx := context.Background()
	_, err := s.CreateObject(ctx, "p", "Person")
	must(t, err)
	_, err = s.CreateObject(ctx, "o", "Org")
	must(t, err)

	l := createOK(t, s, CreateLinkInput{
		LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
		Discriminator: map[string]string{"role": "dev"},
	})
	must(t, s.DeleteLink(ctx, l.ID()))

	log := s.AuditLog()
	if len(log) != 2 || log[0].Result != CodeAccepted || log[1].Result != CodeAccepted {
		t.Fatalf("unexpected audit: %+v", log)
	}
	if log[0].LinkID != l.ID() {
		t.Fatalf("accepted record missing new link id: %+v", log[0])
	}
	if log[0].Seq >= log[1].Seq {
		t.Fatalf("seq not strictly increasing: %d %d", log[0].Seq, log[1].Seq)
	}
}

func joinedContains(parts []string, sub string) bool {
	for _, p := range parts {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}
