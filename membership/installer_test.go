package membership

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func mustInstaller(t *testing.T, members []string) *Installer {
	t.Helper()
	installer, err := NewInstaller(members)
	if err != nil {
		t.Fatalf("NewInstaller(%v): %v", members, err)
	}
	return installer
}

func mustBegin(t *testing.T, installer *Installer, members []string) {
	t.Helper()
	if err := installer.BeginChange(members); err != nil {
		t.Fatalf("BeginChange(%v): %v", members, err)
	}
}

func mustReport(t *testing.T, installer *Installer, member string, counts map[string]uint64) {
	t.Helper()
	if err := installer.Report(member, counts); err != nil {
		t.Fatalf("Report(%q, %v): %v", member, counts, err)
	}
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func TestOldViewMajority(t *testing.T) {
	installer := mustInstaller(t, []string{"a", "b", "c", "d"})
	err := installer.BeginChange([]string{"a", "b", "e"})
	assertErrorIs(t, err, ErrInsufficientQuorum)
	t.Logf("input old=[a b c d] candidate=[a b e], output=%v, criterion=2*2 is not greater than 4", err)

	mustBegin(t, installer, []string{"a", "b", "c", "e"})
	mustReport(t, installer, "a", nil)
	mustReport(t, installer, "b", nil)
	mustReport(t, installer, "c", nil)
	plan, err := installer.Complete()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if plan.ViewID != 2 {
		t.Fatalf("ViewID = %d, want 2", plan.ViewID)
	}
	t.Logf("input old=[a b c d] candidate=[a b c e], output=%+v, criterion=2*3 > 4", plan)

	installer = mustInstaller(t, []string{"a", "b"})
	err = installer.BeginChange([]string{"a", "c"})
	assertErrorIs(t, err, ErrInsufficientQuorum)
	t.Logf("input old=[a b] candidate=[a c], output=%v, criterion=2*1 is not greater than 2", err)
}

func TestRejectDisjointMembership(t *testing.T) {
	installer := mustInstaller(t, []string{"a", "b", "c", "d"})
	err := installer.BeginChange([]string{"e", "f", "g"})
	assertErrorIs(t, err, ErrInsufficientQuorum)
	t.Logf("input candidate=[e f g], output=%v, criterion=no old-view survivor", err)
}

func TestDeadSenderMessagesAreCatchUpForAllSurvivors(t *testing.T) {
	installer := mustInstaller(t, []string{"a", "b", "c", "d"})
	mustBegin(t, installer, []string{"a", "b", "c", "e"})
	mustReport(t, installer, "a", map[string]uint64{"d": 2})
	mustReport(t, installer, "b", nil)
	mustReport(t, installer, "c", nil)
	plan, err := installer.Complete()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	want := &Plan{
		ViewID:     2,
		NewMembers: []string{"a", "b", "c", "e"},
		CatchUp: map[string][]Message{
			"a": {},
			"b": {{Sender: "d", Sequence: 1}, {Sender: "d", Sequence: 2}},
			"c": {{Sender: "d", Sequence: 1}, {Sender: "d", Sequence: 2}},
		},
		Agreed: map[string]uint64{"a": 0, "b": 0, "c": 0, "d": 2},
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan = %+v, want %+v", plan, want)
	}
	t.Logf("input a delivered d:2 while d departed, output=%+v, criterion=union contains d1,d2", plan)
}

func TestAgreedUsesMaximum(t *testing.T) {
	installer := mustInstaller(t, []string{"a", "b", "c"})
	mustBegin(t, installer, []string{"a", "b", "c", "n"})
	mustReport(t, installer, "a", map[string]uint64{"a": 1, "b": 1})
	mustReport(t, installer, "b", map[string]uint64{"a": 3, "b": 3})
	mustReport(t, installer, "c", map[string]uint64{"a": 2, "b": 2})
	plan, err := installer.Complete()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	wantAgreed := map[string]uint64{"a": 3, "b": 3, "c": 0}
	if !reflect.DeepEqual(plan.Agreed, wantAgreed) {
		t.Fatalf("Agreed = %v, want %v", plan.Agreed, wantAgreed)
	}
	wantCatchUp := []Message{
		{Sender: "a", Sequence: 2},
		{Sender: "a", Sequence: 3},
		{Sender: "b", Sequence: 2},
		{Sender: "b", Sequence: 3},
	}
	if got := plan.CatchUp["a"]; !reflect.DeepEqual(got, wantCatchUp) {
		t.Fatalf("a catch-up = %v, want %v", got, wantCatchUp)
	}
	t.Logf("input reports a:1/1 b:3/3 c:2/2, output agreed=%v catchup[a]=%v, criterion=maximum and sender then sequence order", plan.Agreed, plan.CatchUp["a"])
}

func TestNewJoinerCannotReportAndHasNoCatchUp(t *testing.T) {
	installer := mustInstaller(t, []string{"a", "b", "c"})
	mustBegin(t, installer, []string{"a", "b", "c", "n"})
	assertErrorIs(t, installer.Report("n", nil), ErrNotSurvivor)
	mustReport(t, installer, "a", map[string]uint64{"a": 2})
	mustReport(t, installer, "b", nil)
	mustReport(t, installer, "c", nil)
	plan, err := installer.Complete()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := plan.CatchUp["n"]; ok {
		t.Fatalf("new joiner n has catch-up %v", plan.CatchUp["n"])
	}
	want := []Message{{Sender: "a", Sequence: 1}, {Sender: "a", Sequence: 2}}
	if got := plan.CatchUp["b"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("b catch-up = %v, want %v", got, want)
	}
	t.Logf("input reporter=n is a joiner, output=%v then plan=%+v, criterion=joiners are not in CatchUp", ErrNotSurvivor, plan)
}

func TestAbortAllowsRestartAndErrorPriorities(t *testing.T) {
	installer := mustInstaller(t, []string{"a", "b", "c"})
	mustBegin(t, installer, []string{"a", "b", "c", "n"})
	if err := installer.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	assertErrorIs(t, installer.Abort(), ErrNotChanging)
	assertErrorIs(t, installer.BeginChange([]string{"a", "b", "c"}), ErrSameMembership)
	mustBegin(t, installer, []string{"a", "b", "n"})
	assertErrorIs(t, installer.BeginChange([]string{"a"}), ErrAlreadyChanging)
	mustReport(t, installer, "a", nil)
	assertErrorIs(t, installer.Report("a", nil), ErrDuplicateReport)
	assertErrorIs(t, installer.Report("x", nil), ErrNotSurvivor)
	assertErrorIs(t, installer.Report("b", map[string]uint64{"x": 1}), ErrUnknownSender)
	mustReport(t, installer, "b", nil)
	if _, err := installer.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	assertErrorIs(t, installer.BeginChange(nil), ErrEmptyMembers)
	assertErrorIs(t, installer.BeginChange([]string{"a", "a"}), ErrDuplicateMember)
	assertErrorIs(t, installer.BeginChange([]string{"a", ""}), ErrEmptyMemberID)
	t.Log("input abort/reject sequence, output=restarted and installed, criterion=documented first-error priority")
}

func TestNextChangeUsesNewViewMembership(t *testing.T) {
	installer := mustInstaller(t, []string{"a", "b", "c", "d"})
	mustBegin(t, installer, []string{"a", "b", "c", "e"})
	mustReport(t, installer, "a", nil)
	mustReport(t, installer, "b", nil)
	mustReport(t, installer, "c", nil)
	if _, err := installer.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	err := installer.BeginChange([]string{"a", "e", "f", "g"})
	assertErrorIs(t, err, ErrInsufficientQuorum)
	t.Logf("input installed view=[a b c e] candidate=[a e f g], output=%v, criterion=2 survivors are not a majority of 4 old-view members", err)

	mustBegin(t, installer, []string{"a", "b", "e", "f"})
	mustReport(t, installer, "a", nil)
	mustReport(t, installer, "b", nil)
	mustReport(t, installer, "e", nil)
	plan, err := installer.Complete()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if plan.ViewID != 3 || !reflect.DeepEqual(plan.NewMembers, []string{"a", "b", "e", "f"}) {
		t.Fatalf("unexpected next plan: %+v", plan)
	}
	t.Logf("output=%+v, criterion=quorum and reset counts use installed membership", plan)
}

func TestConcurrentCompleteExactlyOneSucceeds(t *testing.T) {
	installer := mustInstaller(t, []string{"a", "b", "c"})
	mustBegin(t, installer, []string{"a", "b", "c", "n"})
	mustReport(t, installer, "a", nil)
	mustReport(t, installer, "b", nil)
	mustReport(t, installer, "c", nil)

	const attempts = 16
	var wait sync.WaitGroup
	errs := make(chan error, attempts)
	start := make(chan struct{})
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := installer.Complete()
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			assertErrorIs(t, err, ErrNotChanging)
		}
	}
	if successes != 1 {
		t.Fatalf("successful Complete calls = %d, want 1", successes)
	}
	t.Logf("input=%d concurrent Complete calls, output=%d success, criterion=mutex serializes the state transition", attempts, successes)
}

func TestReportOrderAndReplayDeterminism(t *testing.T) {
	reports := map[string]map[string]uint64{
		"a": {"a": 2, "c": 1},
		"b": {"a": 1, "b": 2},
		"c": {"c": 3},
	}
	firstOrder := []string{"c", "a", "b"}
	secondOrder := []string{"b", "c", "a"}
	first := completeWithReportOrder(t, reports, firstOrder)
	second := completeWithReportOrder(t, reports, secondOrder)
	identical := reflect.DeepEqual(first, second)
	if !identical {
		t.Fatalf("plans differ by report order: %+v vs %+v", first, second)
	}

	first.NewMembers[0] = "mutated"
	first.Agreed["a"] = 99
	first.CatchUp["a"] = append(first.CatchUp["a"], Message{Sender: "z", Sequence: 1})
	third := completeWithReportOrder(t, reports, secondOrder)
	if !reflect.DeepEqual(third, second) {
		t.Fatalf("returned plan aliases internal state: replay got %+v, want %+v", third, second)
	}
	t.Logf("input orders=%v,%v, output identical=%t, criterion=maps collect all reports and returned plan is cloned", firstOrder, secondOrder, identical)
}

func TestRejectedOperationsDoNotChangeStateOrAliasInput(t *testing.T) {
	installer := mustInstaller(t, []string{"a", "b", "c"})
	assertErrorIs(t, installer.Report("a", nil), ErrNotChanging)
	_, err := installer.Complete()
	assertErrorIs(t, err, ErrNotChanging)
	assertErrorIs(t, installer.Abort(), ErrNotChanging)
	mustBegin(t, installer, []string{"a", "b", "n"})
	assertErrorIs(t, installer.BeginChange([]string{"a"}), ErrAlreadyChanging)
	assertErrorIs(t, installer.BeginChange(nil), ErrAlreadyChanging)
	mustReport(t, installer, "a", nil)
	_, err = installer.Complete()
	assertErrorIs(t, err, ErrIncompleteReports)
	counts := map[string]uint64{"a": 2}
	mustReport(t, installer, "b", counts)
	counts["a"] = 99
	counts["x"] = 5
	assertErrorIs(t, installer.Report("x", counts), ErrNotSurvivor)
	assertErrorIs(t, installer.Abort(), nil)
	mustBegin(t, installer, []string{"a", "b", "n"})
	assertErrorIs(t, installer.Report("a", map[string]uint64{"x": 1}), ErrUnknownSender)
	acceptedCounts := map[string]uint64{"a": 1}
	assertErrorIs(t, installer.Report("a", acceptedCounts), nil)
	acceptedCounts["a"] = 99
	mustReport(t, installer, "b", nil)
	plan, err := installer.Complete()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if plan.Agreed["a"] != 1 {
		t.Fatalf("Agreed[a] = %d, want 1: rejected input mutated state", plan.Agreed["a"])
	}
	t.Logf("input rejected operations and post-Report map mutation, output agreed=%v, criterion=previous change aborted and Report copies input", plan.Agreed)
}

func completeWithReportOrder(t *testing.T, reports map[string]map[string]uint64, order []string) *Plan {
	t.Helper()
	installer := mustInstaller(t, []string{"a", "b", "c"})
	mustBegin(t, installer, []string{"a", "b", "c", "n"})
	for _, member := range order {
		mustReport(t, installer, member, reports[member])
	}
	plan, err := installer.Complete()
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return plan
}

func TestRandomScenariosAgainstMessageSetUnion(t *testing.T) {
	random := rand.New(rand.NewSource(1085))
	current := []string{"a", "b", "c", "d"}

	for iteration := range 60 {
		candidate, survivorReports := randomChange(random, current, iteration)
		t.Logf("random input iteration=%d old=%v candidate=%v reports=%v", iteration, current, candidate, survivorReports)

		want := naivePlan(current, candidate, survivorReports)
		installer := mustInstaller(t, current)
		mustBegin(t, installer, candidate)
		survivors := make([]string, 0, len(survivorReports))
		for survivor := range survivorReports {
			survivors = append(survivors, survivor)
		}
		random.Shuffle(len(survivors), func(left, right int) {
			survivors[left], survivors[right] = survivors[right], survivors[left]
		})
		for _, survivor := range survivors {
			mustReport(t, installer, survivor, survivorReports[survivor])
		}
		got, err := installer.Complete()
		if err != nil {
			t.Fatalf("iteration %d Complete: %v", iteration, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d plan = %+v, want %+v", iteration, got, want)
		}
		t.Logf("random output iteration=%d plan=%+v, judgment=DeepEqual against per-message union", iteration, got)
	}
}

func randomChange(random *rand.Rand, current []string, iteration int) ([]string, map[string]map[string]uint64) {
	shuffled := append([]string(nil), current...)
	random.Shuffle(len(shuffled), func(left, right int) {
		shuffled[left], shuffled[right] = shuffled[right], shuffled[left]
	})
	minimumSurvivors := len(current)/2 + 1
	survivorCount := minimumSurvivors + random.Intn(len(current)-minimumSurvivors+1)
	candidate := append([]string(nil), shuffled[:survivorCount]...)
	joinerCount := random.Intn(3)
	if survivorCount == len(current) {
		joinerCount++
	}
	for index := range joinerCount {
		candidate = append(candidate, fmt.Sprintf("n%d_%d", iteration, index))
	}

	survivorReports := make(map[string]map[string]uint64)
	oldSet := make(map[string]struct{}, len(current))
	for _, member := range current {
		oldSet[member] = struct{}{}
	}
	for _, member := range candidate {
		if _, ok := oldSet[member]; !ok {
			continue
		}
		counts := make(map[string]uint64)
		for _, sender := range current {
			if random.Intn(10) < 6 {
				counts[sender] = uint64(random.Intn(5))
			}
		}
		survivorReports[member] = counts
	}
	return candidate, survivorReports
}

func naivePlan(oldMembers, candidate []string, reports map[string]map[string]uint64) *Plan {
	senders := append([]string(nil), oldMembers...)
	sort.Strings(senders)
	newMembers := append([]string(nil), candidate...)
	sort.Strings(newMembers)

	union := make(map[string]map[uint64]struct{})
	for _, sender := range senders {
		union[sender] = make(map[uint64]struct{})
	}
	for _, counts := range reports {
		for _, sender := range senders {
			for sequence := uint64(1); sequence <= counts[sender]; sequence++ {
				union[sender][sequence] = struct{}{}
			}
		}
	}

	agreed := make(map[string]uint64, len(senders))
	for _, sender := range senders {
		agreed[sender] = uint64(len(union[sender]))
	}

	catchUp := make(map[string][]Message)
	for survivor, counts := range reports {
		messages := make([]Message, 0)
		for _, sender := range senders {
			for sequence := counts[sender]; sequence < agreed[sender]; sequence++ {
				messages = append(messages, Message{Sender: sender, Sequence: sequence + 1})
			}
		}
		catchUp[survivor] = messages
	}

	return &Plan{
		ViewID:     2,
		NewMembers: newMembers,
		CatchUp:    catchUp,
		Agreed:     agreed,
	}
}
