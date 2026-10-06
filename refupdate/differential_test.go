package refupdate

import (
	"fmt"
	"math/rand"
	"testing"
)

// naiveOracle is an independently written reference model: deliberately
// linear, map/slice based, re-scanning all rules (no trie) and recomputing
// ancestry with an explicit DFS. It mirrors the spec directly so it can catch
// divergence in the optimized implementation.
type naiveOracle struct {
	graph     map[CommitID][]CommitID
	refs      map[RefName]CommitID
	rules     []Rule
	user      string
	auditSeqs int
}

func newNaive(user string, rules []Rule) *naiveOracle {
	return &naiveOracle{
		graph: make(map[CommitID][]CommitID),
		refs:  make(map[RefName]CommitID),
		rules: rules,
		user:  user,
	}
}

func (o *naiveOracle) addCommit(c Commit) {
	o.graph[c.ID] = append([]CommitID(nil), c.Parents...)
}

func (o *naiveOracle) descendant(a, b CommitID) bool {
	if a == b {
		_, ok := o.graph[a]
		return ok
	}
	seen := map[CommitID]bool{}
	stack := []CommitID{a}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		for _, p := range o.graph[cur] {
			if p == b {
				return true
			}
			if !seen[p] {
				stack = append(stack, p)
			}
		}
	}
	return false
}

func (o *naiveOracle) matchRules(name RefName) effectiveRestrictions {
	var eff effectiveRestrictions
	segs := name.Segments()
	for _, r := range o.rules {
		psegs := r.Pattern.Segments()
		if len(psegs) != len(segs) {
			continue
		}
		match := true
		for i := range segs {
			if psegs[i] != Wildcard && psegs[i] != segs[i] {
				match = false
				break
			}
		}
		if match {
			eff.merge(*ruleToEffective(r))
		}
	}
	return eff
}

func (o *naiveOracle) push(ins []Instruction) (bool, []RejectReason) {
	type ev struct {
		pass   bool
		reason RejectReason
		after  CommitID
		ref    RefName
		kind   RefKind
		old    CommitID
	}
	evs := make([]ev, len(ins))
	for i, in := range ins {
		e := &evs[i]
		e.ref = in.Ref
		e.old = o.refs[in.Ref]
		e.after = e.old
		create, del := in.OldID == "", in.NewID == ""
		switch {
		case create && del:
			e.reason = RejectMalformedInstruction
			continue
		}
		if in.Ref == "" {
			e.reason = RejectInvalidRefName
			continue
		}
		e.kind = in.Ref.Kind()
		if e.kind == KindUnknown {
			e.reason = RejectMalformedInstruction
			continue
		}
		if !in.Ref.ValidSyntax() {
			e.reason = RejectInvalidRefName
			continue
		}
		if !del {
			if _, ok := o.graph[in.NewID]; !ok {
				e.reason = RejectObjectMissing
				continue
			}
		}
		if in.OldID != e.old {
			e.reason = RejectOldValueMismatch
			continue
		}
		eff := o.matchRules(in.Ref)
		switch {
		case create:
			if eff.noCreate {
				e.reason = RejectProtectedCreate
				continue
			}
			if !eff.allowsUser(o.user) {
				e.reason = RejectProtectedAllowlist
				continue
			}
		case del:
			if eff.noDelete {
				e.reason = RejectProtectedDelete
				continue
			}
			if !eff.allowsUser(o.user) {
				e.reason = RejectProtectedAllowlist
				continue
			}
		default:
			if e.kind == KindTag {
				e.reason = RejectTagImmutable
				continue
			}
			if !o.descendant(in.NewID, in.OldID) {
				if eff.noNonFF {
					e.reason = RejectProtectedNonFF
					continue
				}
				if !in.Force {
					e.reason = RejectNonFFWithoutForce
					continue
				}
			}
			if !eff.allowsUser(o.user) {
				e.reason = RejectProtectedAllowlist
				continue
			}
		}
		if del {
			e.after = ""
		} else {
			e.after = in.NewID
		}
		e.pass = true
	}

	batch := RejectNone
	seen := map[RefName]int{}
	deleted := map[RefName]bool{}
	tdel, tcreate := map[RefName]bool{}, map[RefName]bool{}
	var created []RefName
	for i := range evs {
		e := &evs[i]
		if !e.pass {
			continue
		}
		if _, ok := seen[e.ref]; ok {
			batch = RejectBatchConflict
		}
		seen[e.ref] = i
		if ins[i].OldID == "" {
			created = append(created, e.ref)
			if e.kind == KindTag {
				tcreate[e.ref] = true
			}
		}
		if ins[i].NewID == "" {
			deleted[e.ref] = true
		}
		if ins[i].NewID == "" && e.kind == KindTag {
			tdel[e.ref] = true
		}
	}
	for r := range tdel {
		if tcreate[r] {
			batch = RejectBatchConflict
		}
	}
	for _, n := range created {
		for ex := range o.refs {
			if deleted[ex] {
				continue
			}
			if RefPrefixConflict(n, ex) {
				batch = RejectBatchConflict
			}
		}
	}
	for i := range created {
		for j := i + 1; j < len(created); j++ {
			if RefPrefixConflict(created[i], created[j]) {
				batch = RejectBatchConflict
			}
		}
	}

	anyFail := batch != RejectNone
	for i := range evs {
		if !evs[i].pass {
			anyFail = true
		}
	}
	reasons := make([]RejectReason, len(ins))
	if anyFail {
		for i := range evs {
			if !evs[i].pass {
				reasons[i] = evs[i].reason
			} else if batch != RejectNone {
				reasons[i] = batch
			} else {
				reasons[i] = SkippedDueToOtherReject
			}
		}
		return false, reasons
	}
	for i := range evs {
		if evs[i].after == "" {
			delete(o.refs, evs[i].ref)
		} else {
			o.refs[evs[i].ref] = evs[i].after
		}
	}
	o.auditSeqs++
	return true, reasons
}

// TestRandomDifferential generates random commit DAGs, random rules and
// random instruction batches and compares verdicts, per-item reasons, final
// reference state and audit sequence counts against the naive oracle.
func TestRandomDifferential(t *testing.T) {
	const iterations = 400
	rng := rand.New(rand.NewSource(20261006))
	users := []string{"alice", "bob", "carol"}

	for iter := 0; iter < iterations; iter++ {
		g := NewGraph()
		oracle := newNaive("alice", nil)

		// Random DAG: commits may parent any earlier commit.
		nCommits := 1 + rng.Intn(14)
		ids := make([]CommitID, nCommits)
		for i := range ids {
			ids[i] = CommitID(fmt.Sprintf("c%d", i))
		}
		for i, id := range ids {
			var parents []CommitID
			if i > 0 && rng.Intn(4) != 0 {
				pick := rng.Intn(i)
				parents = append(parents, ids[pick])
				if rng.Intn(3) == 0 && pick > 0 {
					parents = append(parents, ids[rng.Intn(pick)])
				}
			}
			g.AddCommit(Commit{ID: id, Parents: parents})
			oracle.addCommit(Commit{ID: id, Parents: parents})
		}
		// Inject an id that names no object, used for object-missing cases.
		missing := CommitID("missing-x")

		// Random rules over a small segment alphabet.
		namePool := []RefName{"refs/heads/main", "refs/heads/dev", "refs/heads/rel/x",
			"refs/tags/v1", "refs/tags/v2", "refs/heads/bad name", ""}
		patterns := []RefName{"refs/heads/*", "refs/tags/*", "refs/heads/rel/*", "refs/heads/main"}
		var rules []Rule
		for _, p := range patterns {
			if rng.Intn(2) == 0 {
				rules = append(rules, Rule{
					Pattern: p,
					Restrictions: Restrictions{
						NoDelete:      rng.Intn(2) == 0,
						NoNonFF:       rng.Intn(2) == 0,
						NoCreate:      rng.Intn(2) == 0,
						AllowlistOnly: rng.Intn(2) == 0,
					},
					Allowlist: []string{users[rng.Intn(len(users))]},
				})
			}
		}
		s := NewStore(g)
		if err := s.SetRules(rules); err != nil {
			t.Fatal(err)
		}
		oracle.rules = rules

		nBatches := 1 + rng.Intn(5)
		for b := 0; b < nBatches; b++ {
			user := users[rng.Intn(len(users))]
			oracle.user = user
			nIns := 1 + rng.Intn(4)
			ins := make([]Instruction, nIns)
			for k := range ins {
				ref := namePool[rng.Intn(len(namePool))]
				in := Instruction{Ref: ref}
				mode := rng.Intn(6)
				idAt := func() CommitID {
					if rng.Intn(6) == 0 {
						return missing
					}
					return ids[rng.Intn(len(ids))]
				}
				switch mode {
				case 0: // create
					in.NewID = idAt()
				case 1: // update with random old
					in.OldID = idAt()
					in.NewID = idAt()
					in.Force = rng.Intn(2) == 0
				case 2: // delete with random old
					in.OldID = idAt()
				case 3: // both empty
				case 4: // update using current value as old (realistic)
					if cur, ok := oracle.refs[ref]; ok {
						in.OldID = cur
						in.NewID = idAt()
						in.Force = rng.Intn(2) == 0
					} else {
						in.NewID = idAt()
					}
				default:
					if cur, ok := oracle.refs[ref]; ok {
						in.OldID = cur
					} else {
						in.NewID = idAt()
					}
				}
				ins[k] = in
			}

			got := s.Push(user, ins)
			wantAccepted, wantReasons := oracle.push(ins)

			reasons := make([]RejectReason, len(ins))
			for i := range got.Items {
				reasons[i] = got.Items[i].Reason
			}
			if got.Accepted != wantAccepted {
				logCase(t, fmt.Sprintf("diff-iter%d-batch%d", iter, b),
					fmt.Sprintf("user=%s ins=%+v rules=%+v refs=%v", user, ins, rules, oracle.refs),
					fmt.Sprintf("accepted=%v reasons=%v", wantAccepted, wantReasons),
					fmt.Sprintf("accepted=%v reasons=%v", got.Accepted, reasons))
				t.Fatal("acceptance divergence")
			}
			for i := range reasons {
				if reasons[i] != wantReasons[i] {
					logCase(t, fmt.Sprintf("diff-iter%d-batch%d-item%d", iter, b, i),
						fmt.Sprintf("%+v", ins), wantReasons[i].String(), reasons[i].String())
					t.Fatal("per-item reason divergence")
				}
			}
			gotRefs := s.Refs()
			if len(gotRefs) != len(oracle.refs) {
				t.Fatalf("ref state size divergence: %v vs %v", gotRefs, oracle.refs)
			}
			for k, v := range oracle.refs {
				if gotRefs[k] != v {
					t.Fatalf("ref %s: %s vs oracle %s", k, gotRefs[k], v)
				}
			}
			if len(s.AuditLog()) != oracle.auditSeqs {
				t.Fatalf("audit seq divergence: %d vs %d", len(s.AuditLog()), oracle.auditSeqs)
			}
			if iter%50 == 0 && b == 0 {
				logCase(t, "diff-ok", fmt.Sprintf("iter=%d batch sample", iter),
					"matches oracle", fmt.Sprintf("accepted=%v reasons=%v", got.Accepted, reasons))
			}
		}
	}
}
