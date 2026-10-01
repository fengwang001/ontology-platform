package ontology

import (
	"errors"
	"testing"
)

func mustRegister(t *testing.T, manager *Manager, id, parent string) {
	t.Helper()
	if err := manager.Register(id, parent); err != nil {
		t.Fatalf("Register(%q, %q): %v", id, parent, err)
	}
}

func TestLockConversionSAndIXBecomeSIX(t *testing.T) {
	manager := NewManager()
	mustRegister(t, manager, "root", "")
	mustRegister(t, manager, "child", "root")

	if err := manager.Lock(1, "root", IX); err != nil {
		t.Fatal(err)
	}
	if err := manager.Lock(1, "root", S); err != nil {
		t.Fatal(err)
	}

	mode, held, err := manager.Held(1, "root")
	if err != nil || !held || mode != SIX {
		t.Fatalf("Held = %q, %v, %v; want SIX", mode, held, err)
	}
}

func TestSDoesNotSatisfyIXIntentAndJoinTargetDrivesCheck(t *testing.T) {
	manager := NewManager()
	mustRegister(t, manager, "root", "")
	mustRegister(t, manager, "child", "root")
	mustRegister(t, manager, "root-is", "")
	mustRegister(t, manager, "child-is", "root-is")

	if err := manager.Lock(1, "root", S); err != nil {
		t.Fatal(err)
	}
	if err := manager.Lock(1, "child", IX); !errors.Is(err, ErrMissingIntent) {
		t.Fatalf("S ancestor for IX = %v, want ErrMissingIntent", err)
	}
	if err := manager.Lock(1, "child", S); err != nil {
		t.Fatal(err)
	}
	err := manager.Lock(1, "child", IX)
	if !errors.Is(err, ErrMissingIntent) {
		t.Fatalf("Lock = %v, want ErrMissingIntent", err)
	}
	var missing *MissingIntentError
	if !errors.As(err, &missing) || missing.Ancestor != "root" || missing.Required != IX {
		t.Fatalf("missing intent = %#v, want root/IX", err)
	}

	mode, held, queryErr := manager.Held(1, "child")
	if queryErr != nil || !held || mode != S {
		t.Fatalf("rejected conversion changed mode to %q held=%v err=%v", mode, held, queryErr)
	}

	if err := manager.Lock(1, "root-is", IS); err != nil {
		t.Fatal(err)
	}
	if err := manager.Lock(1, "child-is", S); err != nil {
		t.Fatal(err)
	}
	err = manager.Lock(1, "child-is", IX)
	if !errors.Is(err, ErrMissingIntent) {
		t.Fatalf("IS ancestor for converted SIX = %v, want ErrMissingIntent", err)
	}
	var convertedMissing *MissingIntentError
	if !errors.As(err, &convertedMissing) || convertedMissing.Ancestor != "root-is" || convertedMissing.Required != IX {
		t.Fatalf("converted missing intent = %#v, want root-is/IX", err)
	}
}

func TestSIXCompatibilityAndConflictDetails(t *testing.T) {
	manager := NewManager()
	mustRegister(t, manager, "root", "")

	if err := manager.Lock(1, "root", SIX); err != nil {
		t.Fatal(err)
	}
	if err := manager.Lock(2, "root", IS); err != nil {
		t.Fatalf("SIX should be compatible with IS: %v", err)
	}

	err := manager.Lock(3, "root", IX)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Lock = %v, want ErrConflict", err)
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Transaction != 1 || conflict.Mode != SIX {
		t.Fatalf("conflict = %#v, want transaction 1/SIX", err)
	}

	holders, err := manager.Holders("root")
	if err != nil {
		t.Fatal(err)
	}
	if len(holders) != 2 || holders[0].Transaction != 1 || holders[0].Mode != SIX || holders[1].Transaction != 2 || holders[1].Mode != IS {
		t.Fatalf("Holders = %#v", holders)
	}
}

func TestStrongerHeldModeMakesWeakRequestNoop(t *testing.T) {
	manager := NewManager()
	mustRegister(t, manager, "root", "")
	if err := manager.Lock(1, "root", X); err != nil {
		t.Fatal(err)
	}
	if err := manager.Lock(1, "root", IX); err != nil {
		t.Fatalf("weaker request should be a no-op: %v", err)
	}

	mode, held, err := manager.Held(1, "root")
	if err != nil || !held || mode != X {
		t.Fatalf("Held = %q, %v, %v; want X", mode, held, err)
	}
}

func TestUnlockRejectsDescendantAndReleaseAllSucceeds(t *testing.T) {
	manager := NewManager()
	mustRegister(t, manager, "root", "")
	mustRegister(t, manager, "child", "root")
	mustRegister(t, manager, "grandchild", "child")

	if err := manager.Lock(1, "root", IX); err != nil {
		t.Fatal(err)
	}
	if err := manager.Lock(1, "child", IX); err != nil {
		t.Fatal(err)
	}
	if err := manager.Lock(1, "grandchild", S); err != nil {
		t.Fatal(err)
	}

	if err := manager.Unlock(1, "root"); !errors.Is(err, ErrDescendantLocked) {
		t.Fatalf("Unlock root = %v, want ErrDescendantLocked", err)
	}
	if err := manager.ReleaseAll(1); err != nil {
		t.Fatalf("ReleaseAll: %v", err)
	}
	if holders, err := manager.Holders("root"); err != nil || len(holders) != 0 {
		t.Fatalf("root holders = %#v, %v", holders, err)
	}
	if holders, err := manager.Holders("child"); err != nil || len(holders) != 0 {
		t.Fatalf("child holders = %#v, %v", holders, err)
	}
	if holders, err := manager.Holders("grandchild"); err != nil || len(holders) != 0 {
		t.Fatalf("grandchild holders = %#v, %v", holders, err)
	}
}

func TestRejectedLockDoesNotChangeState(t *testing.T) {
	manager := NewManager()
	mustRegister(t, manager, "root", "")
	mustRegister(t, manager, "child", "root")
	mustRegister(t, manager, "other", "")
	if err := manager.Lock(1, "root", IS); err != nil {
		t.Fatal(err)
	}

	err := manager.Lock(1, "child", IX)
	if !errors.Is(err, ErrMissingIntent) {
		t.Fatalf("Lock child = %v, want missing intent", err)
	}
	if mode, held, err := manager.Held(1, "child"); err != nil || held || mode != "" {
		t.Fatalf("rejected child state = %q/%v/%v", mode, held, err)
	}

	if err := manager.Lock(2, "other", X); err != nil {
		t.Fatal(err)
	}
	err = manager.Lock(3, "other", IS)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Lock = %v, want conflict", err)
	}
	if mode, held, queryErr := manager.Held(3, "other"); queryErr != nil || held || mode != "" {
		t.Fatalf("rejected txn state = %q/%v/%v", mode, held, queryErr)
	}
}
