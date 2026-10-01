package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestMajorityAndValidation(t *testing.T) {
	installer := NewInstaller([]string{"a", "b", "c", "d"})

	err := installer.BeginChange([]string{"a", "b", "e"})
	if err != ErrNotOldMajority {
		t.Fatalf("two survivors from four: got %v want %v; basis=2*2<=4", err, ErrNotOldMajority)
	}
	t.Logf("input old=[a b c d] proposed=[a b e]; output err=%v; basis=survivors=2 oldCount=4", err)

	if err := installer.BeginChange([]string{"a", "b", "c", "e"}); err != nil {
		t.Fatalf("three survivors from four: %v; basis=2*3>4", err)
	}
	t.Log("input proposed=[a b c e]; output=change accepted; basis=survivors=3 oldCount=4")

	if err := installer.BeginChange([]string{"a"}); err != ErrAlreadyInChange {
		t.Fatalf("begin during change: got %v want %v; basis=already-changing has priority", err, ErrAlreadyInChange)
	}

	two := NewInstaller([]string{"a", "b"})
	if err := two.BeginChange([]string{"a", "c"}); err != ErrNotOldMajority {
		t.Fatalf("one survivor from two: got %v want %v; basis=2*1<=2", err, ErrNotOldMajority)
	}

	disjoint := NewInstaller([]string{"a", "b", "c"})
	if err := disjoint.BeginChange([]string{"x", "y"}); err != ErrNotOldMajority {
		t.Fatalf("zero survivors: got %v want %v; basis=completely disjoint has no old majority", err, ErrNotOldMajority)
	}

	invalid := NewInstaller([]string{"a", "b", "c"})
	cases := []struct {
		name    string
		members []string
		want    error
		basis   string
	}{
		{"empty", nil, ErrEmptyMembers, "empty is first stable-state check"},
		{"duplicate before empty id", []string{"a", "", "a"}, ErrDuplicateMember, "first duplicate precedes empty id"},
		{"empty id before same set", []string{"a", "b", ""}, ErrEmptyMemberID, "empty id precedes equality"},
		{"same set", []string{"b", "a", "c"}, ErrSameMembers, "equality is checked before old majority"},
	}
	for _, tc := range cases {
		err := invalid.BeginChange(tc.members)
		if err != tc.want {
			t.Fatalf("%s: got %v want %v; basis=%s", tc.name, err, tc.want, tc.basis)
		}
		t.Logf("input=%v output=%v basis=%s", tc.members, err, tc.basis)
	}
}

func TestAgreedCatchUpsAndOrdering(t *testing.T) {
	installer := NewInstaller([]string{"gone", "s1", "s2"})
	if err := installer.BeginChange([]string{"n", "s1", "s2"}); err != nil {
		t.Fatal(err)
	}

	reports := map[string]map[string]int{
		"s1": {"gone": 2, "s1": 1, "s2": 3},
		"s2": {"gone": 0, "s1": 4, "s2": 1},
	}
	for _, survivor := range []string{"s2", "s1"} {
		if err := installer.Report(survivor, reports[survivor]); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := installer.Complete()
	if err != nil {
		t.Fatal(err)
	}

	wantAgreed := map[string]int{"gone": 2, "s1": 4, "s2": 3}
	if !reflect.DeepEqual(plan.Agreed, wantAgreed) {
		t.Fatalf("agreed=%v want=%v; basis=maximum, not minimum/intersection", plan.Agreed, wantAgreed)
	}
	wantCatchUps := map[string][]Message{
		"s1": {{Sender: "s1", Seq: 2}, {Sender: "s1", Seq: 3}, {Sender: "s1", Seq: 4}},
		"s2": {
			{Sender: "gone", Seq: 1},
			{Sender: "gone", Seq: 2},
			{Sender: "s2", Seq: 2},
			{Sender: "s2", Seq: 3},
		},
	}
	if !reflect.DeepEqual(plan.CatchUps, wantCatchUps) {
		t.Fatalf("catchups=%v want=%v; basis=sender lexicographic then seq ascending", plan.CatchUps, wantCatchUps)
	}
	if _, exists := plan.CatchUps["n"]; exists {
		t.Fatalf("new joiner has catch-up %v; want no entry", plan.CatchUps["n"])
	}
	if plan.ViewID != 2 || !reflect.DeepEqual(plan.Members, []string{"n", "s1", "s2"}) {
		t.Fatalf("plan header=%d %v want view 2 [n s1 s2]", plan.ViewID, plan.Members)
	}
	t.Logf("input reports=%v output agreed=%v catchups=%v; basis=max and departed old sender retained", reports, plan.Agreed, plan.CatchUps)
}

func TestReportValidationAndAbort(t *testing.T) {
	installer := NewInstaller([]string{"a", "b", "c"})

	if err := installer.Report("a", nil); err != ErrNotInChange {
		t.Fatalf("stable report: got %v want %v", err, ErrNotInChange)
	}
	if _, err := installer.Complete(); err != ErrNotInChange {
		t.Fatalf("stable complete: got %v want %v; basis=complete checks change state first", err, ErrNotInChange)
	}
	if err := installer.Abort(); err != ErrNotInChange {
		t.Fatalf("stable abort: got %v want %v", err, ErrNotInChange)
	}

	if err := installer.BeginChange([]string{"a", "b", "n"}); err != nil {
		t.Fatal(err)
	}
	if err := installer.Report("n", map[string]int{"z": 1}); err != ErrNotSurvivor {
		t.Fatalf("new joiner report: got %v want %v; basis=survivor checked before senders", err, ErrNotSurvivor)
	}
	if err := installer.Report("a", map[string]int{"z": 1}); err != ErrUnknownSender {
		t.Fatalf("unknown sender: got %v want %v", err, ErrUnknownSender)
	}
	if err := installer.Report("a", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := installer.Report("a", map[string]int{"a": 2}); err != ErrDuplicateReport {
		t.Fatalf("duplicate report: got %v want %v; basis=first report remains", err, ErrDuplicateReport)
	}
	if _, err := installer.Complete(); err != ErrReportPending {
		t.Fatalf("complete with missing report: got %v want %v", err, ErrReportPending)
	}

	if err := installer.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := installer.Abort(); err != ErrNotInChange {
		t.Fatalf("second abort: got %v want %v", err, ErrNotInChange)
	}
	if err := installer.BeginChange([]string{"a", "b", "c", "n"}); err != nil {
		t.Fatalf("change after abort: %v; basis=abort retains view and members", err)
	}
	t.Log("input=partial reports then abort; output=change restarted; basis=view stays 1 and reports clear")
}

func TestNextViewAndReportOrderReplay(t *testing.T) {
	first := NewInstaller([]string{"a", "b", "c"})
	firstReports := map[string]map[string]int{
		"a": {"a": 2},
		"b": {"a": 1, "b": 3},
	}
	firstPlan := completeScenario(t, first, []string{"a", "b", "n"}, firstReports, []string{"a", "b"})

	reordered := NewInstaller([]string{"a", "b", "c"})
	reorderedPlan := completeScenario(t, reordered, []string{"a", "b", "n"}, firstReports, []string{"b", "a"})
	if !reflect.DeepEqual(firstPlan, reorderedPlan) {
		t.Fatalf("report arrival order changed plan:\n%#v\n%#v", firstPlan, reorderedPlan)
	}
	t.Logf("input order=[a,b] and [b,a]; output identical=%v; basis=agreed uses max", reflect.DeepEqual(firstPlan, reorderedPlan))

	if err := first.BeginChange([]string{"n", "b", "x"}); err != nil {
		t.Fatalf("next change uses installed members: %v", err)
	}
	if err := first.Report("a", nil); err != ErrNotSurvivor {
		t.Fatalf("departed old member: got %v want %v", err, ErrNotSurvivor)
	}
	if err := first.Report("x", nil); err != ErrNotSurvivor {
		t.Fatalf("new joiner during pending next change: got %v want %v", err, ErrNotSurvivor)
	}
	if err := first.Report("n", map[string]int{"c": 1}); err != ErrUnknownSender {
		t.Fatalf("previous-view sender: got %v want %v; basis=senders reset to installed view", err, ErrUnknownSender)
	}

	for _, member := range []string{"b", "n"} {
		if err := first.Report(member, nil); err != nil {
			t.Fatal(err)
		}
	}
	secondPlan, err := first.Complete()
	if err != nil {
		t.Fatal(err)
	}
	if secondPlan.ViewID != 3 {
		t.Fatalf("second installed view=%d want=3", secondPlan.ViewID)
	}
	if !reflect.DeepEqual(secondPlan.Agreed, map[string]int{"a": 0, "b": 0, "n": 0}) {
		t.Fatalf("new-view agreed=%v want all current members at 0", secondPlan.Agreed)
	}
}

func TestReturnedPlanHasNoInternalAliases(t *testing.T) {
	installer := NewInstaller([]string{"a", "b"})
	inputCounts := map[string]int{"a": 1}
	if err := installer.BeginChange([]string{"a", "b", "n"}); err != nil {
		t.Fatal(err)
	}
	if err := installer.Report("a", inputCounts); err != nil {
		t.Fatal(err)
	}
	if err := installer.Report("b", nil); err != nil {
		t.Fatal(err)
	}
	plan, err := installer.Complete()
	if err != nil {
		t.Fatal(err)
	}

	plan.Members[0] = "mutated"
	plan.Agreed["a"] = 99
	plan.CatchUps["b"][0] = Message{Sender: "mutated", Seq: 99}
	inputCounts["a"] = 77

	if installer.members[0] == "mutated" {
		t.Fatal("returned Members slice aliases internal members")
	}
	if err := installer.BeginChange([]string{"a", "b", "n", "x"}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"a", "b", "n"} {
		if err := installer.Report(member, nil); err != nil {
			t.Fatal(err)
		}
	}
	next, err := installer.Complete()
	if err != nil {
		t.Fatal(err)
	}
	if next.ViewID != 3 || !reflect.DeepEqual(next.Agreed, map[string]int{"a": 0, "b": 0, "n": 0}) {
		t.Fatalf("state changed through returned-plan aliases: %#v", next)
	}
}

func TestConcurrentCompleteOnlyOneSucceeds(t *testing.T) {
	installer := NewInstaller([]string{"a", "b", "c"})
	if err := installer.BeginChange([]string{"a", "b", "c", "n"}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"a", "b", "c"} {
		if err := installer.Report(member, nil); err != nil {
			t.Fatal(err)
		}
	}

	const attempts = 16
	var wg sync.WaitGroup
	results := make(chan error, attempts)
	wg.Add(attempts)
	for range attempts {
		go func() {
			defer wg.Done()
			_, err := installer.Complete()
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if err != ErrNotInChange {
			t.Fatalf("losing Complete got %v want %v", err, ErrNotInChange)
		}
	}
	if successes != 1 {
		t.Fatalf("successful Completes=%d want=1; basis=one linear serialization", successes)
	}
	t.Logf("input=%d concurrent Completes; output successes=%d; basis=mutex winner installs view", attempts, successes)
}

func TestRandomScenariosAgainstNaiveUnion(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iteration := 0; iteration < 100; iteration++ {
		oldMembers := make([]string, 0, 2+rng.Intn(5))
		for index := range cap(oldMembers) {
			oldMembers = append(oldMembers, fmt.Sprintf("old-%d-%d", iteration, index))
		}

		requiredSurvivors := len(oldMembers)/2 + 1
		survivorCount := requiredSurvivors + rng.Intn(len(oldMembers)-requiredSurvivors+1)
		rng.Shuffle(len(oldMembers), func(i, j int) {
			oldMembers[i], oldMembers[j] = oldMembers[j], oldMembers[i]
		})
		survivors := append([]string(nil), oldMembers[:survivorCount]...)
		nextMembers := append(append([]string(nil), survivors...), fmt.Sprintf("new-%d", iteration))

		sort.Strings(oldMembers)
		sort.Strings(survivors)
		sort.Strings(nextMembers)

		reports := make(map[string]map[string]int, len(survivors))
		for survivorIndex, survivor := range survivors {
			counts := map[string]int{}
			for _, sender := range oldMembers {
				if rng.Intn(3) != 0 {
					counts[sender] = rng.Intn(5)
				}
			}
			reports[survivor] = counts
			_ = survivorIndex
		}

		installer := NewInstaller(oldMembers)
		plan := completeScenario(t, installer, nextMembers, reports, survivors)
		expected := naiveUnionPlan(oldMembers, nextMembers, survivors, reports)

		if !reflect.DeepEqual(plan, expected) {
			t.Fatalf("iteration %d mismatch:\ninput old=%v next=%v reports=%v\ngot=%#v\nwant=%#v\nbasis=union per sender then per-survivor difference",
				iteration, oldMembers, nextMembers, reports, plan, expected)
		}
		t.Logf("iteration=%d input old=%v next=%v reports=%v output view=%d agreed=%v catchups=%v; basis=matches naive per-message union",
			iteration, oldMembers, nextMembers, reports, plan.ViewID, plan.Agreed, plan.CatchUps)
	}
}

func naiveUnionPlan(oldMembers, nextMembers, survivors []string, reports map[string]map[string]int) *Plan {
	agreed := make(map[string]int, len(oldMembers))
	union := make(map[string]map[Message]struct{}, len(oldMembers))

	for _, sender := range oldMembers {
		union[sender] = make(map[Message]struct{})
		maxSeq := 0
		for _, survivor := range survivors {
			count := reports[survivor][sender]
			if count > maxSeq {
				maxSeq = count
			}
			for seq := 1; seq <= count; seq++ {
				union[sender][Message{Sender: sender, Seq: seq}] = struct{}{}
			}
		}
		agreed[sender] = maxSeq
	}

	catchUps := make(map[string][]Message, len(survivors))
	for _, survivor := range survivors {
		messages := make([]Message, 0)
		for _, sender := range oldMembers {
			for seq := 1; seq <= agreed[sender]; seq++ {
				message := Message{Sender: sender, Seq: seq}
				if _, delivered := union[sender][message]; delivered && seq > reports[survivor][sender] {
					messages = append(messages, message)
				}
			}
		}
		catchUps[survivor] = messages
	}

	return &Plan{
		ViewID:   2,
		Members:  append([]string(nil), nextMembers...),
		CatchUps: catchUps,
		Agreed:   agreed,
	}
}

func completeScenario(t *testing.T, installer *Installer, next []string, reports map[string]map[string]int, order []string) *Plan {
	t.Helper()
	return mustComplete(t, installer, next, reports, order)
}

func mustComplete(t *testing.T, installer *Installer, next []string, reports map[string]map[string]int, order []string) *Plan {
	t.Helper()
	if err := installer.BeginChange(next); err != nil {
		t.Fatal(err)
	}
	for _, member := range order {
		if err := installer.Report(member, reports[member]); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := installer.Complete()
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
