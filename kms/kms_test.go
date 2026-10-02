package kms

import (
	"errors"
	"reflect"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErr(t *testing.T, err error, code ErrCode, reason string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v (%q), got nil", code, reason)
	}
	var ke *Error
	if !errors.As(err, &ke) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if ke.Code != code {
		t.Fatalf("expected code %v, got %v (%v)", code, ke.Code, ke)
	}
	if reason != "" && ke.Reason != reason {
		t.Fatalf("expected reason %q, got %q", reason, ke.Reason)
	}
	return ke
}

func mustCreate(t *testing.T, m *Manager, id string, p, now int64) int64 {
	t.Helper()
	gen, err := m.Create(id, p, now)
	mustOK(t, err)
	return gen
}

func mustDescribe(t *testing.T, m *Manager, id string, now int64) KeyView {
	t.Helper()
	view, err := m.Describe(id, now)
	mustOK(t, err)
	return view
}

func checkView(t *testing.T, view KeyView, state State, gen int64, versions []Version, d, deleteAt int64) {
	t.Helper()
	if view.State != state {
		t.Fatalf("view state = %v, want %v", view.State, state)
	}
	if view.Gen != gen {
		t.Fatalf("view gen = %d, want %d", view.Gen, gen)
	}
	if !reflect.DeepEqual(view.Versions, versions) {
		t.Fatalf("view versions = %+v, want %+v", view.Versions, versions)
	}
	if view.NextRotation != d {
		t.Fatalf("view d = %d, want %d", view.NextRotation, d)
	}
	if view.DeleteAt != deleteAt {
		t.Fatalf("view deleteAt = %d, want %d", view.DeleteAt, deleteAt)
	}
}

// TestSpecWalkthrough replays the primary example from the specification.
func TestSpecWalkthrough(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)

	if gen := mustCreate(t, m, "k", 60, 0); gen != 1 {
		t.Fatalf("gen = %d, want 1", gen)
	}
	checkView(t, mustDescribe(t, m, "k", 0), Enabled, 1,
		[]Version{{1, 0}}, 60, 0)
	mustCreate(t, m, "j", 0, 0)

	// Encrypt(k,215): c = (215-60)/60+1 = 3, versions 2@60,3@120,4@180,
	// version 1 evicted, d=240.
	cred, err := m.Encrypt("k", 215)
	mustOK(t, err)
	if cred != (Credential{"k", 1, 4}) {
		t.Fatalf("cred = %+v, want {k 1 4}", cred)
	}
	checkView(t, mustDescribe(t, m, "k", 215), Enabled, 1,
		[]Version{{2, 60}, {3, 120}, {4, 180}}, 240, 0)

	// Disable(k,250): catch-up builds 5@240, retains 3,4,5, d=300.
	mustOK(t, m.Disable("k", 250))
	checkView(t, mustDescribe(t, m, "k", 250), Disabled, 1,
		[]Version{{3, 120}, {4, 180}, {5, 240}}, 300, 0)

	// Enable(k,300): d=300 <= 300, so d becomes 360; version 6 not built.
	mustOK(t, m.Enable("k", 300))
	checkView(t, mustDescribe(t, m, "k", 300), Enabled, 1,
		[]Version{{3, 120}, {4, 180}, {5, 240}}, 360, 0)

	// Decrypt((k,1,2),350): d=360 not due; version 2 < min retained 3.
	_, err = m.Decrypt(Credential{"k", 1, 2}, 350)
	mustErr(t, err, ErrVersionProblem, "version retired")

	// ScheduleDeletion(k,100,400): catch-up builds 6@360, retains 4,5,6,
	// d=420, Pending with deleteAt=500.
	mustOK(t, m.ScheduleDeletion("k", 100, 400))
	checkView(t, mustDescribe(t, m, "k", 400), Pending, 1,
		[]Version{{4, 180}, {5, 240}, {6, 360}}, 420, 500)

	// Decrypt((k,1,6),499): Pending rejects with its own reason.
	_, err = m.Decrypt(Credential{"k", 1, 6}, 499)
	mustErr(t, err, ErrStateDenied, "pending deletion")

	// Create(x,0,499): k and j still live, Kmax=2.
	_, err = m.Create("x", 0, 499)
	mustErr(t, err, ErrLimitExceeded, "")

	// Decrypt((k,1,6),500): deleteAt reached, k is deleted, slot freed.
	_, err = m.Decrypt(Credential{"k", 1, 6}, 500)
	mustErr(t, err, ErrKeyDeleted, "")

	// Create(k,60,500) succeeds immediately: generation 2, version from 1.
	if gen := mustCreate(t, m, "k", 60, 500); gen != 2 {
		t.Fatalf("gen = %d, want 2", gen)
	}
	checkView(t, mustDescribe(t, m, "k", 500), Enabled, 2,
		[]Version{{1, 500}}, 560, 0)

	// Old-generation credential still reports key-deleted, not success.
	_, err = m.Decrypt(Credential{"k", 1, 6}, 501)
	mustErr(t, err, ErrKeyDeleted, "")

	// A never-created id reports not-found.
	_, err = m.Decrypt(Credential{"z", 1, 1}, 501)
	mustErr(t, err, ErrNotFound, "")
}

// TestSpecCancelBranch replays the example up to Pending, then cancels.
func TestSpecCancelBranch(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)
	mustCreate(t, m, "j", 0, 0)
	_, err = m.Encrypt("k", 215)
	mustOK(t, err)
	mustOK(t, m.Disable("k", 250))
	mustOK(t, m.Enable("k", 300))
	mustOK(t, m.ScheduleDeletion("k", 100, 400))

	// CancelDeletion(k,499): back to Disabled, not Enabled; d stays 420.
	mustOK(t, m.CancelDeletion("k", 499))
	checkView(t, mustDescribe(t, m, "k", 499), Disabled, 1,
		[]Version{{4, 180}, {5, 240}, {6, 360}}, 420, 500)

	// Enable(k,600): d=420 <= 600, so d becomes 660; no catch-up of the
	// rotations missed while Disabled/Pending.
	mustOK(t, m.Enable("k", 600))
	checkView(t, mustDescribe(t, m, "k", 600), Enabled, 1,
		[]Version{{4, 180}, {5, 240}, {6, 360}}, 660, 500)
}

// TestSpecVersionCheckBranch replays the example up to Encrypt(k,215) and
// exercises version-not-exist vs success at the boundary.
func TestSpecVersionCheckBranch(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)
	mustCreate(t, m, "j", 0, 0)
	cred, err := m.Encrypt("k", 215)
	mustOK(t, err)
	if cred != (Credential{"k", 1, 4}) {
		t.Fatalf("cred = %+v, want {k 1 4}", cred)
	}

	// Decrypt((k,1,5),245): catch-up builds 5@240, d=300; 5 is the max.
	isMax, err := m.Decrypt(Credential{"k", 1, 5}, 245)
	mustOK(t, err)
	if !isMax {
		t.Fatalf("Decrypt((k,1,5),245) = false, want true")
	}
	checkView(t, mustDescribe(t, m, "k", 245), Enabled, 1,
		[]Version{{3, 120}, {4, 180}, {5, 240}}, 300, 0)

	// Decrypt((k,1,6),245): version 6 > max 5.
	_, err = m.Decrypt(Credential{"k", 1, 6}, 245)
	mustErr(t, err, ErrVersionProblem, "version does not exist")
}

// TestEnableDeadlineBoundary covers Enable with d == now versus now == d-1.
func TestEnableDeadlineBoundary(t *testing.T) {
	build := func(t *testing.T) *Manager {
		m, err := NewManager(3, 100, 1000, 2)
		mustOK(t, err)
		mustCreate(t, m, "k", 60, 0)
		_, err = m.Encrypt("k", 215)
		mustOK(t, err)
		mustOK(t, m.Disable("k", 250)) // d=300, Disabled
		return m
	}

	// d == now: d moves to now+P, no catch-up of version 6.
	mA := build(t)
	mustOK(t, mA.Enable("k", 300))
	checkView(t, mustDescribe(t, mA, "k", 300), Enabled, 1,
		[]Version{{3, 120}, {4, 180}, {5, 240}}, 360, 0)

	// now == d-1: d unchanged; a later catch-up builds version 6 at 300.
	mB := build(t)
	mustOK(t, mB.Enable("k", 299))
	checkView(t, mustDescribe(t, mB, "k", 299), Enabled, 1,
		[]Version{{3, 120}, {4, 180}, {5, 240}}, 300, 0)
	cred, err := mB.Encrypt("k", 301)
	mustOK(t, err)
	if cred != (Credential{"k", 1, 6}) {
		t.Fatalf("cred = %+v, want {k 1 6}", cred)
	}
	checkView(t, mustDescribe(t, mB, "k", 301), Enabled, 1,
		[]Version{{4, 180}, {5, 240}, {6, 300}}, 360, 0)
}
