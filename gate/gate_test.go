package gate

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/owners"
)

func exampleGate(t *testing.T) *Gate {
	t.Helper()
	g, err := New(Config{
		N:             2,
		DismissStale:  true,
		RequireOwners: true,
		Strict:        true,
		Required:      []string{"build", "test"},
	}, []owners.Rule{
		{Pattern: "*", Owners: []string{"alice"}},
		{Pattern: "docs/", Owners: nil},
		{Pattern: "*.go", Owners: []string{"bob", "carol"}},
		{Pattern: "api/", Owners: []string{"dave"}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, u := range []string{"bob", "carol", "dave", "erin"} {
		if err := g.SetPerm(u, PermWrite); err != nil {
			t.Fatalf("SetPerm(%s): %v", u, err)
		}
	}
	return g
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustBlock(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want errors.Is(%v)", err, want)
	}
}

// openAndMerge opens a fully green request by bob and merges it.
func openAndMerge(t *testing.T, g *Gate) int {
	t.Helper()
	id, err := g.Open("bob", []string{"main.go"}, false)
	mustOK(t, err)
	mustOK(t, g.Review(id, "carol", VerdictApprove))
	mustOK(t, g.Review(id, "dave", VerdictApprove))
	mustOK(t, g.Report(id, "build", 1, StatusSuccess))
	mustOK(t, g.Report(id, "test", 1, StatusSuccess))
	mustOK(t, g.Merge(id, "carol"))
	return id
}

// TestExampleWalkthrough replays the worked example from the spec.
func TestExampleWalkthrough(t *testing.T) {
	g := exampleGate(t)
	id, err := g.Open("bob", []string{"main.go", "api/x.go", "docs/a.md"}, false)
	mustOK(t, err)

	// carol covers main.go (author bob removed), dave covers api/x.go.
	mustOK(t, g.Review(id, "carol", VerdictApprove))
	mustOK(t, g.Review(id, "dave", VerdictApprove))

	// A valid change request blocks; a comment does not lift it.
	mustOK(t, g.Review(id, "erin", VerdictRequestChanges))
	mustBlock(t, g.Mergeable(id), ErrChangesRequested)
	mustOK(t, g.Review(id, "erin", VerdictComment))
	mustBlock(t, g.Mergeable(id), ErrChangesRequested)

	// Dropping erin to none disarms the verdict; restoring rearms it.
	mustOK(t, g.SetPerm("erin", PermNone))
	mustBlock(t, g.Mergeable(id), ErrCheckPending)
	mustOK(t, g.SetPerm("erin", PermWrite))
	mustBlock(t, g.Mergeable(id), ErrChangesRequested)

	// Only a fresh approve clears the block.
	mustOK(t, g.Review(id, "erin", VerdictApprove))
	mustBlock(t, g.Mergeable(id), ErrCheckPending)

	// Failure is reported before an earlier-listed pending check.
	mustOK(t, g.Report(id, "build", 1, StatusPending))
	mustOK(t, g.Report(id, "test", 1, StatusFailure))
	err = g.Mergeable(id)
	mustBlock(t, err, ErrCheckFailed)
	if !strings.Contains(err.Error(), "test") {
		t.Fatalf("failure reason must name test, got %v", err)
	}

	// Both green: mergeable.
	mustOK(t, g.Report(id, "build", 1, StatusSuccess))
	mustOK(t, g.Report(id, "test", 1, StatusSuccess))
	mustOK(t, g.Mergeable(id))

	// Another request merges first: T becomes 1 and the base is stale.
	openAndMerge(t, g)
	if got := g.Version(); got != 1 {
		t.Fatalf("T = %d, want 1", got)
	}
	mustBlock(t, g.Mergeable(id), ErrStaleBase)

	// UpdateBranch keeps approvals but checks must be re-reported.
	mustOK(t, g.UpdateBranch(id))
	err = g.Mergeable(id)
	mustBlock(t, err, ErrCheckPending)
	if !strings.Contains(err.Error(), "build") {
		t.Fatalf("pending reason must name build, got %v", err)
	}

	// Late results for the old head do not count.
	mustOK(t, g.Report(id, "build", 1, StatusSuccess))
	mustOK(t, g.Report(id, "test", 1, StatusSuccess))
	mustBlock(t, g.Mergeable(id), ErrCheckPending)

	// Fresh results on head 2 unblock the merge.
	mustOK(t, g.Report(id, "build", 2, StatusSuccess))
	mustOK(t, g.Report(id, "test", 2, StatusSuccess))
	mustOK(t, g.Mergeable(id))
	mustOK(t, g.Merge(id, "carol"))
	if got := g.Version(); got != 2 {
		t.Fatalf("T = %d, want 2", got)
	}
	mustBlock(t, g.Mergeable(id), ErrMerged)
	mustBlock(t, g.Merge(id, "carol"), ErrMerged)
}

// TestPushDismissesApprovalsOnly verifies Push clears approve verdicts
// but never change requests.
func TestPushDismissesApprovalsOnly(t *testing.T) {
	g := exampleGate(t)
	mustOK(t, g.SetPerm("root", PermAdmin))
	id, err := g.Open("bob", []string{"main.go"}, false)
	mustOK(t, err)
	mustOK(t, g.Review(id, "carol", VerdictApprove))
	mustOK(t, g.Review(id, "dave", VerdictApprove))
	mustOK(t, g.Review(id, "erin", VerdictRequestChanges))

	mustOK(t, g.Push(id, []string{"main.go"}))
	// erin's change request survived the push.
	mustBlock(t, g.Mergeable(id), ErrChangesRequested)
	// Dismiss it: the cleared approvals show instead.
	mustOK(t, g.Dismiss(id, "erin", "root"))
	mustBlock(t, g.Mergeable(id), ErrApprovals)
}

// TestPushKeepsVerdictsWithoutDismissStale verifies that with
// DismissStale off a push keeps approve verdicts.
func TestPushKeepsVerdictsWithoutDismissStale(t *testing.T) {
	g, err := New(Config{N: 1}, nil)
	mustOK(t, err)
	mustOK(t, g.SetPerm("carol", PermWrite))
	id, err := g.Open("bob", []string{"a"}, false)
	mustOK(t, err)
	mustOK(t, g.Review(id, "carol", VerdictApprove))
	mustOK(t, g.Push(id, []string{"b"}))
	mustOK(t, g.Mergeable(id))
}

// TestAuthorSoleOwnerExempt: a file whose only owner is the author is
// exempt from the owner-approval requirement.
func TestAuthorSoleOwnerExempt(t *testing.T) {
	g, err := New(Config{N: 0, RequireOwners: true},
		[]owners.Rule{{Pattern: "*", Owners: []string{"alice"}}})
	mustOK(t, err)
	mustOK(t, g.SetPerm("alice", PermWrite))
	id, err := g.Open("alice", []string{"x.txt"}, false)
	mustOK(t, err)
	mustOK(t, g.Mergeable(id))
	mustOK(t, g.Merge(id, "alice"))
}

// TestUpdateBranchKeepsApprovals verifies verdicts survive a base
// fast-forward while the base would otherwise go stale.
func TestUpdateBranchKeepsApprovals(t *testing.T) {
	g, err := New(Config{N: 1, Strict: true}, nil)
	mustOK(t, err)
	for _, u := range []string{"bob", "carol"} {
		mustOK(t, g.SetPerm(u, PermWrite))
	}
	id1, err := g.Open("bob", []string{"a"}, false)
	mustOK(t, err)
	mustOK(t, g.Review(id1, "carol", VerdictApprove))
	id2, err := g.Open("carol", []string{"b"}, false)
	mustOK(t, err)
	mustOK(t, g.Review(id2, "bob", VerdictApprove))
	mustOK(t, g.Merge(id2, "bob"))

	mustBlock(t, g.Mergeable(id1), ErrStaleBase)
	mustOK(t, g.UpdateBranch(id1))
	mustOK(t, g.Mergeable(id1)) // approval kept, no checks required
	mustOK(t, g.Merge(id1, "carol"))
	if got := g.Version(); got != 2 {
		t.Fatalf("T = %d, want 2", got)
	}
}

// TestPermChangeInvalidatesApproval verifies verdicts are counted by
// the reviewer's permission at decision time.
func TestPermChangeInvalidatesApproval(t *testing.T) {
	g, err := New(Config{N: 1}, nil)
	mustOK(t, err)
	id, err := g.Open("bob", []string{"a"}, false)
	mustOK(t, err)
	mustOK(t, g.Review(id, "carol", VerdictApprove)) // carol is none
	mustBlock(t, g.Mergeable(id), ErrApprovals)
	mustOK(t, g.SetPerm("carol", PermWrite))
	mustOK(t, g.Mergeable(id))
	mustOK(t, g.SetPerm("carol", PermNone))
	mustBlock(t, g.Mergeable(id), ErrApprovals)
}

// TestOldHeadResultsIgnored verifies archived results of older heads
// never participate in decisions.
func TestOldHeadResultsIgnored(t *testing.T) {
	g, err := New(Config{N: 0, Required: []string{"build"}}, nil)
	mustOK(t, err)
	id, err := g.Open("bob", []string{"a"}, false)
	mustOK(t, err)
	mustOK(t, g.Report(id, "build", 1, StatusSuccess))
	mustOK(t, g.Mergeable(id))
	mustOK(t, g.Push(id, []string{"a"})) // head becomes 2
	mustBlock(t, g.Mergeable(id), ErrCheckPending)
	mustOK(t, g.Report(id, "build", 1, StatusSuccess)) // late, archived
	mustBlock(t, g.Mergeable(id), ErrCheckPending)
	mustOK(t, g.Report(id, "build", 2, StatusSuccess))
	mustOK(t, g.Mergeable(id))
}

// TestOwnerApprovalReportsMinPath verifies the failing file with the
// smallest path in byte order is reported.
func TestOwnerApprovalReportsMinPath(t *testing.T) {
	g := exampleGate(t)
	id, err := g.Open("bob", []string{"z.go", "a.go", "m.go"}, false)
	mustOK(t, err)
	// Two valid approvals, but none from carol, the remaining owner.
	mustOK(t, g.Review(id, "dave", VerdictApprove))
	mustOK(t, g.Review(id, "erin", VerdictApprove))
	err = g.Mergeable(id)
	mustBlock(t, err, ErrOwnerApproval)
	if !strings.Contains(err.Error(), "a.go") {
		t.Fatalf("owner reason must name a.go, got %v", err)
	}
}

// TestCheckStatusesPass verifies neutral and skipped count as passed.
func TestCheckStatusesPass(t *testing.T) {
	g, err := New(Config{N: 0, Required: []string{"build", "test"}}, nil)
	mustOK(t, err)
	id, err := g.Open("bob", []string{"a"}, false)
	mustOK(t, err)
	mustOK(t, g.Report(id, "build", 1, StatusNeutral))
	mustOK(t, g.Report(id, "test", 1, StatusSkipped))
	mustOK(t, g.Mergeable(id))
}

// TestBlockOrder verifies the fixed order of merge block reasons.
func TestBlockOrder(t *testing.T) {
	open := func(t *testing.T, g *Gate, files []string, draft bool) int {
		t.Helper()
		id, err := g.Open("bob", files, draft)
		mustOK(t, err)
		return id
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, g *Gate) int
		want  error
	}{
		{"merged beats all", func(t *testing.T, g *Gate) int {
			return openAndMerge(t, g)
		}, ErrMerged},
		{"draft beats changes", func(t *testing.T, g *Gate) int {
			id := open(t, g, []string{"main.go"}, true)
			mustOK(t, g.Review(id, "erin", VerdictRequestChanges))
			return id
		}, ErrDraft},
		{"changes beat approvals", func(t *testing.T, g *Gate) int {
			id := open(t, g, []string{"main.go"}, false)
			mustOK(t, g.Review(id, "erin", VerdictRequestChanges))
			return id
		}, ErrChangesRequested},
		{"approvals beat owner", func(t *testing.T, g *Gate) int {
			return open(t, g, []string{"main.go"}, false)
		}, ErrApprovals},
		{"owner beats check failure", func(t *testing.T, g *Gate) int {
			id := open(t, g, []string{"main.go", "api/x.go"}, false)
			mustOK(t, g.Review(id, "carol", VerdictApprove))
			mustOK(t, g.Review(id, "erin", VerdictApprove))
			mustOK(t, g.Report(id, "test", 1, StatusFailure))
			return id
		}, ErrOwnerApproval},
		{"check failure beats pending", func(t *testing.T, g *Gate) int {
			id := open(t, g, []string{"main.go"}, false)
			mustOK(t, g.Review(id, "carol", VerdictApprove))
			mustOK(t, g.Review(id, "dave", VerdictApprove))
			mustOK(t, g.Report(id, "test", 1, StatusFailure))
			return id
		}, ErrCheckFailed},
		{"pending beats stale base", func(t *testing.T, g *Gate) int {
			id := open(t, g, []string{"main.go"}, false)
			mustOK(t, g.Review(id, "carol", VerdictApprove))
			mustOK(t, g.Review(id, "dave", VerdictApprove))
			openAndMerge(t, g) // T becomes 1, id's base is stale
			return id
		}, ErrCheckPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := exampleGate(t)
			id := tc.setup(t, g)
			mustBlock(t, g.Mergeable(id), tc.want)
		})
	}
}

// TestRejectionOrder verifies the fixed order of rejection causes and
// that rejected operations change no state.
func TestRejectionOrder(t *testing.T) {
	open := func(t *testing.T, g *Gate) int {
		t.Helper()
		id, err := g.Open("bob", []string{"main.go"}, false)
		mustOK(t, err)
		return id
	}
	merged := func(t *testing.T, g *Gate) int {
		t.Helper()
		return openAndMerge(t, g)
	}
	none := func(t *testing.T, g *Gate) int { return 0 }
	cases := []struct {
		name  string
		setup func(t *testing.T, g *Gate) int
		op    func(g *Gate, id int) error
		want  error
	}{
		{"open without files", none, func(g *Gate, id int) error {
			_, err := g.Open("bob", nil, false)
			return err
		}, ErrInvalidParam},
		{"open with duplicated files", none, func(g *Gate, id int) error {
			_, err := g.Open("bob", []string{"a", "a"}, false)
			return err
		}, ErrInvalidParam},
		{"push invalid files before notfound", none, func(g *Gate, id int) error {
			return g.Push(999, nil)
		}, ErrInvalidParam},
		{"push duplicated files before notfound", none, func(g *Gate, id int) error {
			return g.Push(999, []string{"a", "a"})
		}, ErrInvalidParam},
		{"push notfound", none, func(g *Gate, id int) error {
			return g.Push(999, []string{"a"})
		}, ErrNotFound},
		{"merge notfound", none, func(g *Gate, id int) error {
			return g.Merge(999, "bob")
		}, ErrNotFound},
		{"mergeable notfound", none, func(g *Gate, id int) error {
			return g.Mergeable(999)
		}, ErrNotFound},
		{"merge permission before blocks", open, func(g *Gate, id int) error {
			return g.Merge(id, "nobody")
		}, ErrPermission},
		{"dismiss permission", open, func(g *Gate, id int) error {
			return g.Dismiss(id, "carol", "bob")
		}, ErrPermission},
		{"review on merged", merged, func(g *Gate, id int) error {
			return g.Review(id, "carol", VerdictApprove)
		}, ErrState},
		{"state before self on merged", merged, func(g *Gate, id int) error {
			return g.Review(id, "bob", VerdictApprove)
		}, ErrState},
		{"self approve", open, func(g *Gate, id int) error {
			return g.Review(id, "bob", VerdictApprove)
		}, ErrSelfReview},
		{"self request changes", open, func(g *Gate, id int) error {
			return g.Review(id, "bob", VerdictRequestChanges)
		}, ErrSelfReview},
		{"author comment allowed", open, func(g *Gate, id int) error {
			return g.Review(id, "bob", VerdictComment)
		}, nil},
		{"dismiss without verdict", open, func(g *Gate, id int) error {
			return g.Dismiss(id, "carol", "root")
		}, ErrState},
		{"report future head", open, func(g *Gate, id int) error {
			return g.Report(id, "build", 2, StatusSuccess)
		}, ErrInvalidParam},
		{"report invalid status", open, func(g *Gate, id int) error {
			return g.Report(id, "build", 1, Status(9))
		}, ErrInvalidParam},
		{"report invalid status before notfound", none, func(g *Gate, id int) error {
			return g.Report(999, "build", 1, Status(9))
		}, ErrInvalidParam},
		{"review invalid verdict before notfound", none, func(g *Gate, id int) error {
			return g.Review(999, "carol", Verdict(9))
		}, ErrInvalidParam},
		{"updatebranch up to date", open, func(g *Gate, id int) error {
			return g.UpdateBranch(id)
		}, ErrState},
		{"merge blocked", open, func(g *Gate, id int) error {
			return g.Merge(id, "carol")
		}, ErrApprovals},
		{"push on merged", merged, func(g *Gate, id int) error {
			return g.Push(id, []string{"main.go"})
		}, ErrState},
		{"ready on merged", merged, func(g *Gate, id int) error {
			return g.Ready(id)
		}, ErrState},
		{"updatebranch on merged", merged, func(g *Gate, id int) error {
			return g.UpdateBranch(id)
		}, ErrState},
		{"dismiss on merged", merged, func(g *Gate, id int) error {
			return g.Dismiss(id, "carol", "root")
		}, ErrState},
		{"report on merged", merged, func(g *Gate, id int) error {
			return g.Report(id, "build", 1, StatusSuccess)
		}, ErrState},
		{"setperm invalid level", none, func(g *Gate, id int) error {
			return g.SetPerm("x", Perm(9))
		}, ErrInvalidParam},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := exampleGate(t)
			mustOK(t, g.SetPerm("root", PermAdmin))
			id := tc.setup(t, g)
			before := g.Version()
			err := tc.op(g, id)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want errors.Is(%v)", err, tc.want)
			}
			if err != nil && g.Version() != before {
				t.Fatalf("rejected operation changed T from %d to %d", before, g.Version())
			}
		})
	}
}

// TestNewValidation validates constructor parameters.
func TestNewValidation(t *testing.T) {
	many := make([]string, 17)
	for i := range many {
		many[i] = fmt.Sprintf("check%d", i)
	}
	cases := []struct {
		name  string
		cfg   Config
		rules []owners.Rule
		want  error
	}{
		{"negative N", Config{N: -1}, nil, ErrInvalidParam},
		{"N too large", Config{N: 7}, nil, ErrInvalidParam},
		{"too many required", Config{Required: many}, nil, ErrInvalidParam},
		{"duplicated required", Config{Required: []string{"a", "a"}}, nil, ErrInvalidParam},
		{"empty required name", Config{Required: []string{""}}, nil, ErrInvalidParam},
		{"empty pattern", Config{}, []owners.Rule{{Pattern: ""}}, ErrInvalidParam},
		{"valid zero config", Config{}, nil, nil},
		{"valid full config", Config{N: 6, Required: []string{"a", "b"}}, []owners.Rule{{Pattern: "*"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg, tc.rules)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want errors.Is(%v)", err, tc.want)
			}
		})
	}
}

// TestTouchedBound proves a single Review or Report reads and writes at
// most 2 records regardless of the number of open requests.
func TestTouchedBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("prs=%d", n), func(t *testing.T) {
			g, err := New(Config{N: 1}, []owners.Rule{{Pattern: "*", Owners: []string{"alice"}}})
			mustOK(t, err)
			var last int
			for i := 0; i < n; i++ {
				last, err = g.Open("bob", []string{fmt.Sprintf("f%d.go", i)}, false)
				mustOK(t, err)
			}
			before := g.touched
			mustOK(t, g.Review(last, "carol", VerdictApprove))
			if d := g.touched - before; d > 2 {
				t.Errorf("Review touched %d records with %d requests, want <= 2", d, n)
			}
			before = g.touched
			mustOK(t, g.Report(last, "build", 1, StatusSuccess))
			if d := g.touched - before; d > 2 {
				t.Errorf("Report touched %d records with %d requests, want <= 2", d, n)
			}
		})
	}
}

// TestConcurrentSmoke hammers the gate from many goroutines; the race
// detector verifies serializability.
func TestConcurrentSmoke(t *testing.T) {
	g := exampleGate(t)
	var ids []int
	for i := 0; i < 4; i++ {
		id, err := g.Open("bob", []string{fmt.Sprintf("f%d.go", i)}, false)
		mustOK(t, err)
		ids = append(ids, id)
	}
	users := []string{"carol", "dave", "erin"}
	checks := []string{"build", "test"}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := ids[(w+i)%len(ids)]
				user := users[(w+i)%len(users)]
				switch i % 5 {
				case 0:
					_ = g.Review(id, user, VerdictApprove)
				case 1:
					_ = g.Review(id, user, VerdictComment)
				case 2:
					_ = g.Report(id, checks[(w+i)%len(checks)], 1, StatusSuccess)
				case 3:
					_ = g.Mergeable(id)
				case 4:
					_ = g.Ready(id)
				}
			}
		}(w)
	}
	wg.Wait()
}
