package incremental

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func addDecl(id DeclID, signature, implementation string) Edit {
	return Edit{Type: AddDeclaration, ID: id, SignatureSource: signature, ImplementationSource: implementation}
}

func setSignature(id DeclID, signature string) Edit {
	return Edit{Type: SetSignature, ID: id, SignatureSource: signature}
}

func setImplementation(id DeclID, implementation string) Edit {
	return Edit{Type: SetImplementation, ID: id, ImplementationSource: implementation}
}

func TestImplementationDependencyDoesNotPropagate(t *testing.T) {
	checker := newScriptChecker()
	logger := &logRecorder{}
	scheduler := NewScheduler(checker, WithLogger(logger))
	mustApply(t, scheduler, addDecl("a", script("sigA"), script("implA")))
	mustApply(t, scheduler, addDecl("b", script("sigB"), script("implB", "a")))

	outcome := mustApply(t, scheduler, setImplementation("a", script("sigA2impl")))
	assertIDs(t, outcome.Rechecked, "a")
	if checker.implementationCalls["b"] != 1 {
		t.Fatalf("implementation b was rechecked %d times", checker.implementationCalls["b"])
	}
}

func TestSignatureDependencyPropagatesAndEarlyStops(t *testing.T) {
	checker := newScriptChecker()
	scheduler := NewScheduler(checker)
	mustApply(t, scheduler, addDecl("a", script("sa"), script("ia")))
	mustApply(t, scheduler, addDecl("b", script("sb", "a"), script("ib")))
	mustApply(t, scheduler, addDecl("c", script("sc", "b"), script("ic")))

	outcome := mustApply(t, scheduler, setSignature("a", script("sa2")))
	assertIDs(t, outcome.Rechecked, "a", "b", "c")
	b := mustSignature(t, scheduler, "b")
	c := mustSignature(t, scheduler, "c")

	mustApply(t, scheduler, setSignature("b", script("SAME", "a")))
	mustApply(t, scheduler, setSignature("a", script("sa3")))
	b = mustSignature(t, scheduler, "b")
	c = mustSignature(t, scheduler, "c")
	outcome = mustApply(t, scheduler, setSignature("a", script("sa4")))
	assertIDs(t, outcome.Rechecked, "a", "b")
	assertIDs(t, outcome.EarlyStopped, "b")
	b2 := mustSignature(t, scheduler, "b")
	c2 := mustSignature(t, scheduler, "c")
	if b2.Version != b.Version || c2.Version != c.Version {
		t.Fatalf("early stop preserved downstream versions: b=%d->%d c=%d->%d", b.Version, b2.Version, c.Version, c2.Version)
	}
}

func TestCyclicGroupAtomicErrorState(t *testing.T) {
	checker := newScriptChecker()
	logger := &logRecorder{}
	scheduler := NewScheduler(checker, WithLogger(logger))
	mustApply(t, scheduler, addDecl("a", script("sa"), script("ia")))
	mustApply(t, scheduler, addDecl("b", script("sb"), script("ib")))

	oa := mustApply(t, scheduler, setSignature("a", script("sa", "b")))
	ob := mustApply(t, scheduler, setSignature("b", script("sb", "a")))
	_ = logger
	_ = oa
	_ = ob
	a := mustSignature(t, scheduler, "a")
	b := mustSignature(t, scheduler, "b")
	if a.ErrorText == "" || b.ErrorText == "" {
		t.Fatalf("cyclic group members must both be errors: %#v %#v", a, b)
	}
	if a.Signature.Value != b.Signature.Value {
		t.Fatalf("group must adopt one error signature: %q != %q", a.Value, b.Value)
	}
	if a.Basis["a"].Version != a.Version || a.Basis["b"].Version != b.Version {
		t.Fatalf("basis versions must match group results: %#v", a.Basis)
	}
}

func TestDeleteReaddCausesSecondInvalidation(t *testing.T) {
	checker := newScriptChecker()
	scheduler := NewScheduler(checker)
	mustApply(t, scheduler, addDecl("a", script("sa"), script("ia")))
	mustApply(t, scheduler, addDecl("b", script("sb", "a"), script("ib")))
	before := mustSignature(t, scheduler, "b")

	deleteResult, err := scheduler.Apply(Edit{Type: DeleteDeclaration, ID: "a"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	t.Logf("delete outcome=%#v rev=%v", deleteResult, scheduler.graph.signatureDependents("a"))
	missing, _ := scheduler.SignatureResult("a")
	dependent, _ := scheduler.SignatureResult("b")
	t.Logf("missing=%#v dependent=%#v calls=%d", missing, dependent, checker.signatureCalls["b"])
	if missing.Present || dependent.ErrorText == "" {
		t.Fatalf("dependent must see missing declaration as an error")
	}

	mustApply(t, scheduler, addDecl("a", script("sa-restored"), script("ia")))
	restored := mustSignature(t, scheduler, "a")
	again := mustSignature(t, scheduler, "b")
	if !restored.Present || restored.Version <= missing.Version || again.Version <= before.Version {
		t.Fatalf("readd must bump versions and invalidate dependent")
	}
}

func TestNoopEditHasNoSideEffects(t *testing.T) {
	scheduler := NewScheduler(newScriptChecker())
	mustApply(t, scheduler, addDecl("a", script("sa"), script("ia")))
	a := mustSignature(t, scheduler, "a")

	outcome := mustApply(t, scheduler, setSignature("a", script("sa")))
	if !outcome.NoOp || outcome.Stats.LastRecheckedDeclarations != 0 {
		t.Fatalf("identical edit must be no-op: %#v", outcome)
	}
	again := mustSignature(t, scheduler, "a")
	if again.Version != a.Version {
		t.Fatalf("noop incremented version %d -> %d", a.Version, again.Version)
	}
}

func TestBasisVersionMismatchRejectsReuse(t *testing.T) {
	scheduler := NewScheduler(newScriptChecker())
	mustApply(t, scheduler, addDecl("a", script("sa"), script("ia")))
	mustApply(t, scheduler, addDecl("b", script("sb", "a"), script("ib")))
	result, _ := scheduler.cache.signature("b")
	entry := result.Basis["a"]
	entry.Version++
	result.Basis["a"] = entry
	scheduler.cache.putSignature("b", result)

	if _, ok := scheduler.SignatureResult("b"); ok {
		t.Fatalf("stale basis must not be reusable")
	}
	if scheduler.Stats().ReuseCount != 0 {
		t.Fatalf("invalid reuse must not increment stats: %d", scheduler.Stats().ReuseCount)
	}
}

func TestConcurrentOperationsAreSerialEquivalent(t *testing.T) {
	scheduler := NewScheduler(newScriptChecker())
	mustApply(t, scheduler, addDecl("a", script("sa"), script("ia")))

	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(2)
		go func(i int) {
			defer wait.Done()
			_, _ = scheduler.Apply(setSignature("a", script("sa-"+itoa(i))))
		}(i)
		go func() {
			defer wait.Done()
			result, ok := scheduler.SignatureResult("a")
			if ok {
				for _, entry := range result.Basis {
					_ = entry.Version
				}
			}
		}()
	}
	wait.Wait()
}

func TestEditDuringExternalDispatchIsRejected(t *testing.T) {
	scheduler := NewScheduler(newScriptChecker())
	scheduler.dispatching = true
	_, err := scheduler.Apply(setSignature("a", script("sa")))
	if !errors.Is(err, ErrSchedulerBusy) {
		t.Fatalf("expected ErrSchedulerBusy, got %v", err)
	}
	if _, ok := scheduler.registry.get("a"); ok {
		t.Fatalf("rejected edit changed registry")
	}
}

func TestReuseStatisticsAndLogBasis(t *testing.T) {
	logger := &logRecorder{}
	scheduler := NewScheduler(newScriptChecker(), WithLogger(logger))
	mustApply(t, scheduler, addDecl("a", script("sa"), script("ia")))
	scheduler.SignatureResult("a")
	if scheduler.Stats().ReuseCount != 1 {
		t.Fatalf("expected one reuse")
	}
	found := false
	for _, event := range logger.events {
		if strings.Contains(event, "signature-basis") {
			found = true
		}
	}
	if !found {
		t.Fatalf("logger did not record output with basis: %v", logger.events)
	}
}

func TestInvalidationTouchesOnlyReverseNeighbors(t *testing.T) {
	scheduler := NewScheduler(newScriptChecker())
	mustApply(t, scheduler, addDecl("root", script("root"), script("root")))
	mustApply(t, scheduler, addDecl("dep", script("dep", "root"), script("dep")))
	for i := 0; i < 100; i++ {
		mustApply(t, scheduler, addDecl(DeclID("unrelated"+itoa(i)), script("u"), script("u")))
	}
	reverseBefore := scheduler.graph.implementationDependents("root")
	if len(reverseBefore) != 0 {
		t.Fatalf("setup should have no implementation reverse dependency")
	}
	if got := scheduler.graph.signatureDependents("root"); len(got) != 1 || got[0] != "dep" {
		t.Fatalf("invalid lookup must only traverse reverse neighbors: %v", got)
	}
}

func mustSignature(t *testing.T, scheduler *Scheduler, id DeclID) SignatureResult {
	t.Helper()
	result, ok := scheduler.SignatureResult(id)
	if !ok {
		t.Fatalf("missing cached signature %s", id)
	}
	return result
}

func assertIDs(t *testing.T, got []DeclID, want ...DeclID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
