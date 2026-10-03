package budget

import (
	"errors"
	"testing"

	"ontology/cluster"
)

func TestGrantAndReadExactTenant(t *testing.T) {
	service := setupBudget(t)

	if err := service.Grant("alice", "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if err := service.Grant("alice", "tenant-a"); err != nil {
		t.Fatalf("idempotent grant failed: %v", err)
	}

	infos, overflow, err := service.Templates("alice", "tenant-a")
	t.Logf("input caller=alice tenant=tenant-a output=%d templates overflow=%d err=%v decision=exact caller-tenant grant", len(infos), overflow, err)
	if err != nil || len(infos) != 1 || infos[0].Text != "x a b c" || infos[0].Count != 1 {
		t.Fatalf("authorized read failed: %+v overflow=%d err=%v", infos, overflow, err)
	}

	_, _, err = service.Templates("alice", "tenant-b")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("other tenant auth must not imply access: %v", err)
	}
	_, _, err = service.Templates("bob", "tenant-a")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("other caller must not access tenant-a: %v", err)
	}

	if err := service.Grant("bob", "tenant-missing"); err != nil {
		t.Fatal(err)
	}
	_, _, err = service.Templates("bob", "tenant-missing")
	if !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("authorization precedes existence: %v", err)
	}
}

func TestValidationAndRejectOrder(t *testing.T) {
	service := setupBudget(t)

	if err := service.Grant("", "tenant-a"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Grant invalid caller: %v", err)
	}
	if err := service.Grant("alice", ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Grant invalid tenant: %v", err)
	}

	_, _, err := service.Templates("", "tenant-a")
	t.Logf("input empty caller output=err decision=invalid argument before authorization")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	_, _, err = service.Templates("stranger", "")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid tenant precedes authorization: %v", err)
	}
	_, _, err = service.Templates("stranger", "tenant-missing")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unauthorized precedes tenant missing: %v", err)
	}
}

func setupBudget(t *testing.T) *Service {
	t.Helper()
	merger, err := cluster.New(50, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		if _, err := merger.Ingest(tenant, "x a b c"); err != nil {
			t.Fatal(err)
		}
	}
	service := New(merger)
	return service
}
