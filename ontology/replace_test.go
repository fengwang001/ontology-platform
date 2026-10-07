package ontology

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// blockingImpl lets a test hold an in-flight call across a replacement.
type blockingImpl struct {
	name    string
	entered chan struct{}
	release chan struct{}
}

func newBlockingImpl(name string) *blockingImpl {
	return &blockingImpl{
		name:    name,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *blockingImpl) Pre(any) bool { return true }

func (b *blockingImpl) Execute(*ExecContext, any) (any, error) {
	close(b.entered)
	<-b.release
	return b.name, nil
}

func (b *blockingImpl) Post(any, any) bool { return true }

// TestReplacementInFlightKeepsOldGeneration proves an invocation that started
// before a replacement executes the pre-replacement logic to completion,
// while an invocation initiated afterwards uses the new generation.
func TestReplacementInFlightKeepsOldGeneration(t *testing.T) {
	reg, d, a := newSetup()
	_, _, _, _, concrete := hierarchy()

	old := newBlockingImpl("old")
	mustReg(t, reg, a.ID, concrete.ID, old)
	obj := &Instance{ID: "o", Type: concrete}

	var wg sync.WaitGroup
	wg.Add(1)
	var inFlightOut any
	var inFlightTrace *DispatchTrace
	go func() {
		defer wg.Done()
		inFlightOut, inFlightTrace, _ = d.Invoke(context.Background(), obj, a.ID, "x")
	}()

	<-old.entered

	// Replacement becomes effective while the old logic is still running.
	newImpl := mkImpl("new")
	// Empty probe corpus: this replacement has identical acceptance, and the
	// old generation is a blocking impl unsuitable for probe execution.
	if _, err := reg.Replace(a.ID, concrete.ID, newImpl, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// A call initiated strictly after replacement must see the new logic.
	out, newTrace := invokeOK(d, obj, a.ID, "x")
	if out != "new" {
		t.Fatalf("post-replacement call want new, got %v", out)
	}

	close(old.release)
	wg.Wait()

	if inFlightOut != "old" {
		t.Fatalf("in-flight call must finish on old logic, got %v", inFlightOut)
	}
	if inFlightTrace.GenSeq == newTrace.GenSeq {
		t.Fatal("in-flight and new calls must pin different generations")
	}
}

// TestReplacementRequiresRelaxationDeclaration proves a replacement that
// relaxes rejection without declaring scope is rejected at registration.
func TestReplacementRequiresRelaxationDeclaration(t *testing.T) {
	reg, d, a := newSetup()
	_, _, _, _, concrete := hierarchy()

	old := &funcImpl{name: "old", pre: func(in any) bool { return in.(string) != "y" }}
	mustReg(t, reg, a.ID, concrete.ID, old)

	relaxed := &funcImpl{
		name: "new",
		pre:  func(any) bool { return true },
		run:  func(_ *ExecContext, in any) (any, error) { return "same", nil },
	}
	_, err := reg.Replace(a.ID, concrete.ID, relaxed, nil, []any{"x", "y"})
	var regErr *RegistrationError
	if !errors.As(err, &regErr) || regErr.Reason != reasonRelaxRequired {
		t.Fatalf("want relax-required rejection, got %v", err)
	}

	// With a declaration covering the relaxed input, replacement succeeds.
	rel := &Relaxation{
		Description: "y now allowed",
		Covers:      func(in any) bool { return in.(string) == "y" },
	}
	if _, err := reg.Replace(a.ID, concrete.ID, relaxed, rel, []any{"x", "y"}); err != nil {
		t.Fatalf("declared replace: %v", err)
	}

	obj := &Instance{ID: "o", Type: concrete}
	out, _ := invokeOK(d, obj, a.ID, "y")
	if out != "same" {
		t.Fatalf("want new logic accept y, got %v", out)
	}
}

// TestAuditDetectsUndeclaredDifference proves behavior outside the declared
// relaxation scope is discoverable post-hoc via Audit.
func TestAuditDetectsUndeclaredDifference(t *testing.T) {
	reg, _, a := newSetup()
	_, _, _, _, concrete := hierarchy()

	old := &funcImpl{
		name: "old",
		pre:  func(in any) bool { return in.(string) != "y" },
		run:  func(_ *ExecContext, in any) (any, error) { return "same", nil },
	}
	mustReg(t, reg, a.ID, concrete.ID, old)

	// Declared scope claims only "y"; actual behavior also changes output for
	// "z", which the declaration does not cover.
	declaredOnly := &Relaxation{
		Description: "only y",
		Covers:      func(in any) bool { return in.(string) == "y" },
	}
	sneaky := &funcImpl{
		name: "new",
		pre:  func(any) bool { return true },
		run: func(_ *ExecContext, in any) (any, error) {
			if in.(string) == "z" {
				return "different-output", nil
			}
			return "same", nil
		},
	}
	// Probes used at registration time only cover x/y, so registration is
	// allowed; the undeclared "z" difference remains for audit to find.
	if _, err := reg.Replace(a.ID, concrete.ID, sneaky, declaredOnly, []any{"x", "y"}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	rep := reg.Audit([]any{"x", "y", "z"})
	if rep.FindingCount != 1 {
		t.Fatalf("want exactly 1 undeclared finding, got %d", rep.FindingCount)
	}
	f := rep.Findings[0]
	if f.Input.(string) != "z" || f.Kind != DifferenceOutputChanged || f.Declared {
		t.Fatalf("unexpected finding: %+v", f)
	}
}

// hookRevoke parks a lookup at a chosen hop so a concurrent revoke can land.
type hookRevoke struct {
	hop      int
	revoked  chan struct{}
	proceed  chan struct{}
	once     sync.Once
	revokeFn func()
}

func (h *hookRevoke) BeforeHop(_ *DispatchTrace, hopIndex int) {
	if hopIndex == h.hop {
		h.once.Do(func() {
			h.revokeFn()
			close(h.revoked)
			<-h.proceed
		})
	}
}

// TestRevocationDuringLookupFailsCall proves a revoke landing while lookup is
// in progress fails the whole invocation, even though an implementation
// exists at an ancestor.
func TestRevocationDuringLookupFailsCall(t *testing.T) {
	reg, d, a := newSetup()
	_, _, p2, _, concrete := hierarchy()
	mustReg(t, reg, a.ID, p2.ID, mkImpl("p2"))
	obj := &Instance{ID: "o", Type: concrete}

	hook := &hookRevoke{
		hop:      1, // parked while walking concrete -> p1, before hitting p2
		revoked:  make(chan struct{}),
		proceed:  make(chan struct{}),
		revokeFn: obj.Revoke,
	}
	d.SetHook(hook)

	errCh := make(chan error, 1)
	go func() {
		_, _, err := d.Invoke(context.Background(), obj, a.ID, "x")
		errCh <- err
	}()

	<-hook.revoked
	close(hook.proceed)

	err := <-errCh
	if !errors.Is(err, ErrObjectRevoked) {
		t.Fatalf("want object revoked, got %v", err)
	}

	// Revocation after execution began is out of dispatcher scope: an impl
	// that ignores revocation completes normally.
	d.SetHook(nil)
	obj2 := &Instance{ID: "o2", Type: concrete}
	reg2 := NewRegistry()
	d2 := NewDispatcher(reg2)
	reg2.DeclareAction(a)
	selfRevoking := &funcImpl{
		name: "sr",
		run: func(ctx *ExecContext, in any) (any, error) {
			ctx.Object.Revoke()
			return "done", nil // impl's own post-validation accepts
		},
	}
	mustReg(t, reg2, a.ID, concrete.ID, selfRevoking)
	out, _ := invokeOK(d2, obj2, a.ID, "x")
	if out != "done" {
		t.Fatalf("post-dispatch revoke is impl responsibility, got %v", out)
	}
}
