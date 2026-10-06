package refupdate

import (
	"fmt"
	"sync"
	"testing"
)

// logCase prints every test case's input, actual output and the judging basis,
// as required by the test specification.
func logCase(t *testing.T, name, input, basis string, got interface{}) {
	t.Helper()
	t.Logf("CASE %s\n  input : %s\n  expect: %s\n  got   : %#v", name, input, basis, got)
}

func TestRefNameValidation(t *testing.T) {
	valid := []RefName{
		"refs/heads/main",
		"refs/heads/feature/x-1.y",
		"refs/tags/v1.2.3",
		"refs/tags/a/x.lock2/y",
	}
	for _, name := range valid {
		if name.Kind() == KindUnknown || !name.ValidSyntax() {
			logCase(t, "valid-name", string(name), "known namespace + valid syntax", name)
			t.Fatalf("%q rejected", name)
		}
		logCase(t, "valid-name", string(name), "accepted", name.Kind())
	}

	cases := []struct {
		name      RefName
		malformed bool // outside the two namespaces
		badSyntax bool
	}{
		{"", true, true},
		{"/refs/heads/x", true, true},
		{"refs/heads/x/", false, true},
		{"refs/heads//x", false, true},
		{"refs/heads/a\u0000b", false, true},
		{"refs/heads/a\x7fb", false, true},
		{"refs/heads/.hidden", false, true},
		{"refs/heads/seg.lock", false, true},
		{"heads/x", true, false}, // no namespace
		{"refs/notes/x", true, false},
		{"refs/tags", true, false},
	}
	for _, c := range cases {
		gotMalformed := c.name.Kind() == KindUnknown
		gotBad := !c.name.ValidSyntax()
		logCase(t, "name", string(c.name),
			fmt.Sprintf("malformed=%v badSyntax=%v", c.malformed, c.badSyntax),
			fmt.Sprintf("malformed=%v badSyntax=%v", gotMalformed, gotBad))
		if gotMalformed != c.malformed || gotBad != c.badSyntax {
			t.Fatalf("Ref %q: got malformed=%v bad=%v", c.name, gotMalformed, gotBad)
		}
	}

	if !RefPrefixConflict("refs/heads/a", "refs/heads/a/b") {
		t.Fatal("expected ancestor-prefix conflict")
	}
	if RefPrefixConflict("refs/heads/ab", "refs/heads/ab") {
		t.Fatal("equal names are not a prefix conflict")
	}
	if RefPrefixConflict("refs/heads/ab", "refs/heads/ac") {
		t.Fatal("mere string prefix without separator must not conflict")
	}
	logCase(t, "prefix", "a, a/b, ab, ac", "a vs a/b conflict; ab vs ac not",
		RefPrefixConflict("refs/heads/a", "refs/heads/a/b"))
}

func testGraph() *Graph {
	g := NewGraph()
	g.AddCommit(Commit{ID: "root"})
	g.AddCommit(Commit{ID: "c1", Parents: []CommitID{"root"}})
	g.AddCommit(Commit{ID: "c2", Parents: []CommitID{"c1"}})
	g.AddCommit(Commit{ID: "c3", Parents: []CommitID{"c2"}})
	g.AddCommit(Commit{ID: "x0"})
	g.AddCommit(Commit{ID: "x1", Parents: []CommitID{"x0"}})
	return g
}

func TestGraphDescendant(t *testing.T) {
	g := testGraph()
	pairs := []struct {
		desc, anc CommitID
		want      bool
	}{
		{"c3", "c3", true},
		{"c3", "root", true},
		{"c3", "c1", true},
		{"c1", "c3", false},
		{"c1", "x0", false},
		{"x1", "root", false},
		{"missing", "root", false},
	}
	for _, p := range pairs {
		got := g.IsDescendant(p.desc, p.anc)
		logCase(t, "descendant", string(p.desc)+" >= "+string(p.anc), fmt.Sprintf("%v", p.want), got)
		if got != p.want {
			t.Fatalf("IsDescendant(%s,%s)=%v want %v", p.desc, p.anc, got, p.want)
		}
	}
}

// TestFastForwardCostIndependentOfUnrelatedCommits verifies the performance
// contract: adding many commits unrelated to the queried pair must not change
// the number of commits visited by the descendant search.
func TestFastForwardCostIndependentOfUnrelatedCommits(t *testing.T) {
	build := func(unrelated int) int {
		g := testGraph()
		for i := 0; i < unrelated; i++ {
			var parents []CommitID
			if i > 0 {
				parents = []CommitID{CommitID(fmt.Sprintf("u%d", i-1))}
			}
			g.AddCommit(Commit{ID: CommitID(fmt.Sprintf("u%d", i)), Parents: parents})
		}
		before := g.CommitsVisited
		g.IsDescendant("c3", "root")
		return g.CommitsVisited - before
	}
	small, large := build(10), build(10000)
	logCase(t, "ff-cost", "unrelated=10 vs 10000", "equal visit counts", fmt.Sprintf("%d vs %d", small, large))
	if small != large || small != 3 {
		t.Fatalf("ancestor cone expected 3 commits both times, got %d and %d", small, large)
	}
}

func TestCreateUpdateDeleteAndOldValueCombos(t *testing.T) {
	g := testGraph()
	s := NewStore(g)
	push := func(label string, ins []Instruction) PushReport {
		r := s.Push("alice", ins)
		reasons := make([]string, len(r.Items))
		for i, it := range r.Items {
			reasons[i] = it.Reason.String()
		}
		logCase(t, label, fmt.Sprintf("%+v", ins), "see got",
			fmt.Sprintf("accepted=%v reasons=%v refs=%v", r.Accepted, reasons, s.Refs()))
		return r
	}

	r := push("both-empty", []Instruction{{Ref: "refs/heads/a"}})
	if r.Items[0].Reason != RejectMalformedInstruction {
		t.Fatal("both empty must be malformed-instruction")
	}
	r = push("create-wrong-old", []Instruction{{Ref: "refs/heads/a", OldID: "ghost", NewID: "c1"}})
	if r.Items[0].Reason != RejectOldValueMismatch {
		t.Fatal("expected old-value-mismatch")
	}
	r = push("create", []Instruction{{Ref: "refs/heads/a", NewID: "c1"}})
	if !r.Accepted {
		t.Fatal("create should succeed")
	}
	r = push("create-again", []Instruction{{Ref: "refs/heads/a", NewID: "c1"}})
	if r.Items[0].Reason != RejectOldValueMismatch {
		t.Fatal("second create must mismatch")
	}
	r = push("update-wrong-old", []Instruction{{Ref: "refs/heads/a", OldID: "root", NewID: "c2"}})
	if r.Items[0].Reason != RejectOldValueMismatch {
		t.Fatal("expected mismatch")
	}
	r = push("ff-update", []Instruction{{Ref: "refs/heads/a", OldID: "c1", NewID: "c3"}})
	if !r.Accepted {
		t.Fatal("ff update should succeed")
	}
	r = push("delete-wrong-old", []Instruction{{Ref: "refs/heads/a", OldID: "c1"}})
	if r.Items[0].Reason != RejectOldValueMismatch {
		t.Fatal("expected mismatch on delete")
	}
	r = push("delete", []Instruction{{Ref: "refs/heads/a", OldID: "c3"}})
	if !r.Accepted {
		t.Fatal("delete should succeed")
	}
	r = push("missing-object", []Instruction{{Ref: "refs/heads/b", NewID: "nope"}})
	if r.Items[0].Reason != RejectObjectMissing {
		t.Fatal("expected object-missing")
	}
}

func TestFastForwardForceMatrix(t *testing.T) {
	g := testGraph()
	for _, force := range []bool{false, true} {
		s := NewStore(g)
		if !s.Push("alice", []Instruction{{Ref: "refs/heads/b", NewID: "c1"}}).Accepted {
			t.Fatal("setup failed")
		}
		r := s.Push("alice", []Instruction{{Ref: "refs/heads/b", OldID: "c1", NewID: "x1", Force: force}})
		want := RejectNonFFWithoutForce
		if force {
			want = RejectNone
		}
		logCase(t, fmt.Sprintf("nonff force=%v", force), "c1 -> x1", want.String(), r.Items[0].Reason)
		if r.Items[0].Reason != want {
			t.Fatalf("force=%v: got %s", force, r.Items[0].Reason)
		}
	}
}

func TestTagRulesAndDeleteRecreate(t *testing.T) {
	g := testGraph()
	s := NewStore(g)
	if !s.Push("alice", []Instruction{{Ref: "refs/tags/v1", NewID: "c1"}}).Accepted {
		t.Fatal("tag create failed")
	}
	r := s.Push("alice", []Instruction{{Ref: "refs/tags/v1", OldID: "c1", NewID: "c2", Force: true}})
	logCase(t, "tag-update", "v1 c1->c2 force", RejectTagImmutable.String(), r.Items[0].Reason)
	if r.Items[0].Reason != RejectTagImmutable {
		t.Fatal("tag update must be rejected")
	}
	r = s.Push("alice", []Instruction{
		{Ref: "refs/tags/v1", OldID: "c1"},
		{Ref: "refs/tags/v1", NewID: "c2"},
	})
	logCase(t, "tag-del-recreate-batch", "del+create v1", "create mismatches; delete co-skipped; whole batch rejected", r.Items)
	if r.Accepted || r.Items[1].Reason != RejectOldValueMismatch || r.Items[0].Reason != SkippedDueToOtherReject {
		t.Fatal("delete+recreate tag in one batch must fail atomically")
	}
	if !s.Push("alice", []Instruction{{Ref: "refs/tags/v1", OldID: "c1"}}).Accepted {
		t.Fatal("tag delete should succeed")
	}
	if !s.Push("alice", []Instruction{{Ref: "refs/tags/v1", NewID: "c2"}}).Accepted {
		t.Fatal("tag recreate should succeed")
	}
}

func TestProtectedRules(t *testing.T) {
	g := testGraph()

	s := NewStore(g)
	s.SetRules([]Rule{{Pattern: "refs/heads/protected/*",
		Restrictions: Restrictions{NoCreate: true, NoDelete: true, NoNonFF: true, AllowlistOnly: true},
		Allowlist:    []string{"alice"}}})
	r := s.Push("bob", []Instruction{{Ref: "refs/heads/protected/release", NewID: "c1"}})
	logCase(t, "no-create", "bob create protected/release", RejectProtectedCreate.String(), r.Items[0].Reason)
	if r.Items[0].Reason != RejectProtectedCreate {
		t.Fatal(r.Items[0].Reason)
	}

	s2 := NewStore(g)
	s2.SetRules([]Rule{{Pattern: "refs/heads/p/*",
		Restrictions: Restrictions{AllowlistOnly: true}, Allowlist: []string{"alice"}}})
	if s2.Push("alice", []Instruction{{Ref: "refs/heads/p/a", NewID: "c1"}}).Items[0].Reason != RejectNone {
		t.Fatal("allowlisted create failed")
	}
	r = s2.Push("bob", []Instruction{{Ref: "refs/heads/p/a", OldID: "c1", NewID: "c2"}})
	logCase(t, "allowlist", "bob update p/a", RejectProtectedAllowlist.String(), r.Items[0].Reason)
	if r.Items[0].Reason != RejectProtectedAllowlist {
		t.Fatal(r.Items[0].Reason)
	}

	s3 := NewStore(g)
	s3.SetRules([]Rule{{Pattern: "refs/heads/d", Restrictions: Restrictions{NoDelete: true}}})
	s3.Push("alice", []Instruction{{Ref: "refs/heads/d", NewID: "c1"}})
	r = s3.Push("alice", []Instruction{{Ref: "refs/heads/d", OldID: "c1"}})
	if r.Items[0].Reason != RejectProtectedDelete {
		t.Fatal("expected protected delete rejection")
	}

	s4 := NewStore(g)
	s4.SetRules([]Rule{{Pattern: "refs/heads/f", Restrictions: Restrictions{NoNonFF: true}}})
	s4.Push("alice", []Instruction{{Ref: "refs/heads/f", NewID: "c1"}})
	r = s4.Push("alice", []Instruction{{Ref: "refs/heads/f", OldID: "c1", NewID: "x1", Force: true}})
	logCase(t, "no-nonff-force", "forced nonff under NoNonFF", RejectProtectedNonFF.String(), r.Items[0].Reason)
	if r.Items[0].Reason != RejectProtectedNonFF {
		t.Fatal(r.Items[0].Reason)
	}

	// Union across two matching rules.
	rs, _ := NewRuleSet([]Rule{
		{Pattern: "refs/heads/u/*", Restrictions: Restrictions{AllowlistOnly: true}, Allowlist: []string{"z"}},
		{Pattern: "refs/heads/*/b", Restrictions: Restrictions{NoDelete: true}},
	})
	eff := rs.Match("refs/heads/u/b")
	logCase(t, "rule-union", "u/* + */b on u/b", "allowlist + noDelete both on",
		fmt.Sprintf("allowlistOnly=%v noDelete=%v", eff.allowlistOnly, eff.noDelete))
	if !eff.allowlistOnly || !eff.noDelete {
		t.Fatal("union of matched rules failed")
	}
}

func TestRuleMatchCostIndependentOfRuleCount(t *testing.T) {
	build := func(n int) int {
		rules := make([]Rule, 0, n+1)
		for i := 0; i < n; i++ {
			rules = append(rules, Rule{Pattern: RefName(fmt.Sprintf("refs/heads/other%d/x", i))})
		}
		rules = append(rules, Rule{Pattern: "refs/heads/keep/*", Restrictions: Restrictions{NoCreate: true}})
		rs, err := NewRuleSet(rules)
		if err != nil {
			t.Fatal(err)
		}
		before := rs.totalProbes()
		rs.Match("refs/heads/keep/name")
		return rs.totalProbes() - before
	}
	small, large := build(10), build(10000)
	logCase(t, "rule-cost", "10 rules vs 10000 rules", "same probe count", fmt.Sprintf("%d vs %d", small, large))
	if small != large {
		t.Fatalf("rule match cost grew with rule count: %d vs %d", small, large)
	}
}

// TestReasonPrecedence checks every adjacent pair in the reason ordering by
// constructing an instruction that triggers both and asserting the earlier
// reason wins.
func TestReasonPrecedence(t *testing.T) {
	g := testGraph()
	ruleAll := func(s *Store) {
		s.SetRules([]Rule{{Pattern: "refs/heads/prot",
			Restrictions: Restrictions{NoCreate: true, NoDelete: true, NoNonFF: true, AllowlistOnly: true},
			Allowlist:    []string{"nobody"}}})
	}

	type tc struct {
		name  string
		setup func(*Store)
		ins   Instruction
		user  string
		want  RejectReason
	}
	cases := []tc{
		{"malformed-vs-badname", nil, Instruction{Ref: "heads/x/"}, "alice", RejectMalformedInstruction},
		{"badname-vs-missing", nil, Instruction{Ref: "refs/heads/a/", NewID: "nope"}, "alice", RejectInvalidRefName},
		{"missing-vs-oldmismatch", nil, Instruction{Ref: "refs/heads/q", OldID: "ghost", NewID: "nope"}, "alice", RejectObjectMissing},
		{"oldmismatch-vs-nocreate", func(s *Store) { ruleAll(s) }, Instruction{Ref: "refs/heads/prot", OldID: "ghost", NewID: "c1"}, "alice", RejectOldValueMismatch},
		{"nocreate-vs-allowlist", func(s *Store) { ruleAll(s) }, Instruction{Ref: "refs/heads/prot", NewID: "c1"}, "bob", RejectProtectedCreate},
	}
	for _, c := range cases {
		s := NewStore(g)
		if c.setup != nil {
			c.setup(s)
		}
		r := s.Push(c.user, []Instruction{c.ins})
		logCase(t, c.name, fmt.Sprintf("%+v", c.ins), c.want.String(), r.Items[0].Reason)
		if r.Items[0].Reason != c.want {
			t.Fatalf("%s: got %s want %s", c.name, r.Items[0].Reason, c.want)
		}
	}

	// Delete path: NoDelete vs allowlist; old mismatch beats both.
	s2 := NewStore(g)
	s2.Push("alice", []Instruction{{Ref: "refs/heads/prot", NewID: "c1"}})
	ruleAll(s2)
	r := s2.Push("bob", []Instruction{{Ref: "refs/heads/prot", OldID: "c1"}})
	logCase(t, "nodelete-vs-allowlist", "bob delete prot", RejectProtectedDelete.String(), r.Items[0].Reason)
	if r.Items[0].Reason != RejectProtectedDelete {
		t.Fatalf("got %s", r.Items[0].Reason)
	}
	r = s2.Push("alice", []Instruction{{Ref: "refs/heads/prot", OldID: "wrong"}})
	if r.Items[0].Reason != RejectOldValueMismatch {
		t.Fatal("mismatch must precede delete protection")
	}

	// Update path: tag-immutable beats protected non-ff; protected non-ff
	// beats missing-force; allowlist ordering handled by tag immutability.
	s3 := NewStore(g)
	s3.SetRules([]Rule{{Pattern: "refs/tags/t", Restrictions: Restrictions{NoNonFF: true, AllowlistOnly: true}, Allowlist: []string{"x"}}})
	s3.Push("x", []Instruction{{Ref: "refs/tags/t", NewID: "c1"}})
	r = s3.Push("bob", []Instruction{{Ref: "refs/tags/t", OldID: "c1", NewID: "x1", Force: true}})
	logCase(t, "tag-immutable-vs-rest", "tag forced nonff by outsider", RejectTagImmutable.String(), r.Items[0].Reason)
	if r.Items[0].Reason != RejectTagImmutable {
		t.Fatalf("got %s", r.Items[0].Reason)
	}

	// Branch: protected non-ff beats missing force flag.
	s4 := NewStore(g)
	s4.SetRules([]Rule{{Pattern: "refs/heads/f", Restrictions: Restrictions{NoNonFF: true}}})
	s4.Push("alice", []Instruction{{Ref: "refs/heads/f", NewID: "c1"}})
	r = s4.Push("alice", []Instruction{{Ref: "refs/heads/f", OldID: "c1", NewID: "x1"}})
	logCase(t, "protectednonff-vs-noForce", "nonff unforced protected", RejectProtectedNonFF.String(), r.Items[0].Reason)
	if r.Items[0].Reason != RejectProtectedNonFF {
		t.Fatalf("got %s", r.Items[0].Reason)
	}

	// NonFF without force beats a later (none here) batch conflict: build a
	// batch where item 0 is nonff-unforced and item 1 duplicates it; item 0
	// keeps its own reason, item 1 gets batch-conflict; passing items in a
	// batch without batch conflict get the co-skip marker.
	s5 := NewStore(g)
	s5.Push("alice", []Instruction{
		{Ref: "refs/heads/a", NewID: "c1"},
		{Ref: "refs/heads/b", NewID: "root"},
	})
	r = s5.Push("alice", []Instruction{
		{Ref: "refs/heads/a", OldID: "c1", NewID: "x1"},   // nonff, no force
		{Ref: "refs/heads/b", OldID: "root", NewID: "c2"}, // ff, would pass
	})
	logCase(t, "co-skip", "rejected + passing items", "item0 nonff, item1 skipped", r.Items)
	if r.Items[0].Reason != RejectNonFFWithoutForce || r.Items[1].Reason != SkippedDueToOtherReject {
		t.Fatalf("got %s / %s", r.Items[0].Reason, r.Items[1].Reason)
	}
	if s5.Lookup("refs/heads/b") != "root" || s5.Lookup("refs/heads/a") != "c1" {
		t.Fatal("rejected push changed refs")
	}
	if len(s5.AuditLog()) != 1 {
		t.Fatalf("rejected push must not add audit entries, got %d", len(s5.AuditLog()))
	}
}

func TestBatchPrefixConflicts(t *testing.T) {
	g := testGraph()
	s := NewStore(g)
	// Create parent and child in one batch: conflict.
	r := s.Push("alice", []Instruction{
		{Ref: "refs/heads/team", NewID: "c1"},
		{Ref: "refs/heads/team/x", NewID: "c2"},
	})
	logCase(t, "batch-prefix-new-new", "create team + team/x", RejectBatchConflict.String(), r.Items)
	if r.Items[0].Reason != RejectBatchConflict || r.Items[1].Reason != RejectBatchConflict {
		t.Fatal("new/new prefix conflict must reject both")
	}
	// Pre-existing parent clashes with a child creation.
	s.Push("alice", []Instruction{{Ref: "refs/heads/team", NewID: "c1"}})
	r = s.Push("alice", []Instruction{{Ref: "refs/heads/team/x", NewID: "c2"}})
	if r.Items[0].Reason != RejectBatchConflict {
		t.Fatalf("got %s", r.Items[0].Reason)
	}
	// Restructure: delete parent, create child in same batch is allowed
	// because the final state contains only the child.
	r = s.Push("alice", []Instruction{
		{Ref: "refs/heads/team", OldID: "c1"},
		{Ref: "refs/heads/team/x", NewID: "c2"},
	})
	logCase(t, "batch-restructure", "delete team, create team/x", "accepted", r.Accepted)
	if !r.Accepted {
		t.Fatal("delete-parent + create-child should commit")
	}
	// Updating a surviving parent while creating its child must conflict:
	// the parent still exists in the final state.
	s.Push("alice", []Instruction{{Ref: "refs/heads/team/x", OldID: "c2"}})
	s.Push("alice", []Instruction{{Ref: "refs/heads/team", NewID: "c1"}})
	r = s.Push("alice", []Instruction{
		{Ref: "refs/heads/team", OldID: "c1", NewID: "c2"},
		{Ref: "refs/heads/team/y", NewID: "c3"},
	})
	logCase(t, "update-parent-create-child", "update team + create team/y", RejectBatchConflict.String(), r.Items)
	if r.Accepted {
		t.Fatal("updating a surviving parent while creating a child must conflict")
	}
	// Same reference twice in one batch.
	r = s.Push("alice", []Instruction{
		{Ref: "refs/heads/d1", NewID: "c1"},
		{Ref: "refs/heads/d1", NewID: "c1"},
	})
	if r.Items[0].Reason != RejectBatchConflict {
		t.Fatal("duplicate ref in batch must conflict")
	}
}

func TestAuditSequencesAndAtomicVisibility(t *testing.T) {
	g := testGraph()
	s := NewStore(g)
	r := s.Push("alice", []Instruction{
		{Ref: "refs/heads/a", NewID: "c1"},
		{Ref: "refs/heads/b", NewID: "x1"},
	})
	if !r.Accepted || r.AuditSeq != 1 {
		t.Fatalf("accepted=%v seq=%d", r.Accepted, r.AuditSeq)
	}
	audit := s.AuditLog()
	if len(audit) != 1 || audit[0].Seq != 1 || len(audit[0].Items) != 2 {
		t.Fatalf("bad audit: %+v", audit)
	}
	if audit[0].Items[0].Before != "" || audit[0].Items[0].After != "c1" {
		t.Fatal("audit before/after wrong")
	}
	// Rejected push: no seq advance, no audit entry.
	s.Push("alice", []Instruction{{Ref: "refs/heads/a", OldID: "c1", NewID: "missing"}, {Ref: "refs/heads/c", NewID: "c2"}})
	r2 := s.Push("alice", []Instruction{{Ref: "refs/heads/c", NewID: "c2"}})
	if !r2.Accepted || r2.AuditSeq != 2 {
		t.Fatalf("audit sequence must have no hole, got seq %d", r2.AuditSeq)
	}
	logCase(t, "audit", "two successes, one rejected between", "seqs 1,2 no holes", s.AuditLog())
}

func TestConcurrentSameOldValueExactlyOneWins(t *testing.T) {
	g := testGraph()
	s := NewStore(g)
	if !s.Push("seed", []Instruction{{Ref: "refs/heads/m", NewID: "c1"}}).Accepted {
		t.Fatal("seed failed")
	}
	const n = 64
	var wg sync.WaitGroup
	results := make([]PushReport, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx] = s.Push("pusher", []Instruction{{Ref: "refs/heads/m", OldID: "c1", NewID: "c3"}})
		}(i)
	}
	close(start)
	wg.Wait()

	accepted, mismatch := 0, 0
	for _, r := range results {
		switch {
		case r.Accepted:
			accepted++
		case r.Items[0].Reason == RejectOldValueMismatch:
			mismatch++
		default:
			t.Fatalf("unexpected result: %+v", r)
		}
	}
	logCase(t, "concurrent", "64 pushes same old value", "exactly 1 accepted, 63 mismatch",
		fmt.Sprintf("accepted=%d mismatch=%d", accepted, mismatch))
	if accepted != 1 || mismatch != n-1 {
		t.Fatalf("accepted=%d mismatch=%d", accepted, mismatch)
	}
	if got := s.Lookup("refs/heads/m"); got != "c3" {
		t.Fatalf("final value %s", got)
	}
	if len(s.AuditLog()) != 2 {
		t.Fatalf("exactly one winning audit record, got %d", len(s.AuditLog()))
	}
}

func TestRuleSnapshotDuringPush(t *testing.T) {
	// Rules are read under the push lock: changing rules concurrently only
	// ever affects whole pushes, producing serializable outcomes. This is a
	// structural check: SetRules and Push share one mutex.
	g := testGraph()
	s := NewStore(g)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.SetRules([]Rule{{Pattern: "refs/heads/*"}})
		}()
		go func(i int) {
			defer wg.Done()
			s.Push("alice", []Instruction{{Ref: RefName(fmt.Sprintf("refs/heads/r%d", i)), NewID: "c1"}})
		}(i)
	}
	wg.Wait()
	logCase(t, "rule-snapshot", "concurrent SetRules and Push", "no panic, audit seqs dense", len(s.AuditLog()))
	audit := s.AuditLog()
	for i, e := range audit {
		if e.Seq != i+1 {
			t.Fatal("audit sequence hole")
		}
	}
}
