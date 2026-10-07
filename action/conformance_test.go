// Differential test: the real engine is checked against an independent
// naive model that keeps its own transaction log, applies writes step by
// step onto a deep-copied working state, and calls hooks at fixed points.
// Both run the same action bodies and the same hook logic over many random
// nested action sequences; final state and hook firing records must match
// exactly.
package action

import (
	"fmt"
	"math/rand"
	"testing"

	oerr "ontology/errors"
	"ontology/hooks"
	"ontology/txn"
)

// snap is the read surface hook logic is written against, so the same
// logic runs on the real txn.View and on the naive deep-copied snapshot.
type snap interface {
	Get(typeName, id string) (txn.Value, bool)
	Count(typeName string) int
}

// ---------- naive model ----------

type naiveSnap struct {
	state map[string]map[string]txn.Value
}

func (s naiveSnap) Get(typeName, id string) (txn.Value, bool) {
	v, ok := s.state[typeName][id]
	return v, ok
}

func (s naiveSnap) Count(typeName string) int {
	return len(s.state[typeName])
}

func deepCopyState(in map[string]map[string]txn.Value) map[string]map[string]txn.Value {
	out := map[string]map[string]txn.Value{}
	for t, insts := range in {
		out[t] = map[string]txn.Value{}
		for id, v := range insts {
			vc := txn.Value{}
			for k, x := range v {
				vc[k] = x
			}
			out[t][id] = vc
		}
	}
	return out
}

type naiveHook struct {
	name  string
	logic func(s snap) error
}

type naiveModel struct {
	types     map[string]ObjectType
	defs      map[string]Definition
	pre       map[string][]naiveHook
	post      map[string][]naiveHook
	committed map[string]map[string]txn.Value
	records   []hooks.Record
	txnSeq    int64
}

// naiveTxn is one outermost transaction: an explicit write log applied
// step by step onto a deep-copied working state.
type naiveTxn struct {
	m        *naiveModel
	working  map[string]map[string]txn.Value
	log      []string
	preFired map[string]bool
	touched  []string
	failed   error
	txnID    int64
	action   string
}

func (nt *naiveTxn) record(phase hooks.Phase, typeName, hookName string) {
	nt.m.records = append(nt.m.records, hooks.Record{
		Seq: len(nt.m.records), TxnID: nt.txnID, Action: nt.action,
		Phase: phase, TypeName: typeName, HookName: hookName,
	})
}

func (nt *naiveTxn) poison(err error) error {
	if nt.failed == nil {
		nt.failed = err
	}
	return nt.failed
}

func (nt *naiveTxn) firePre(typeName string) error {
	if nt.preFired[typeName] {
		return nil
	}
	nt.preFired[typeName] = true
	for _, h := range nt.m.pre[typeName] {
		nt.record(hooks.Pre, typeName, h.name)
		// naive: the pre-write snapshot is a full deep copy, O(state).
		if err := h.logic(naiveSnap{deepCopyState(nt.working)}); err != nil {
			return oerr.PreHook(nt.action, fmt.Sprintf("pre-hook %s/%s: %v", typeName, h.name, err))
		}
	}
	return nil
}

func (nt *naiveTxn) write(op writeOp, typeName, id string, props map[string]any) error {
	if nt.failed != nil {
		return nt.failed
	}
	schema, ok := nt.m.types[typeName]
	if !ok {
		return nt.poison(oerr.InvalidArgument(nt.action, "unknown object type"))
	}
	if err := checkProps(schema, props); err != nil {
		return nt.poison(oerr.InvalidArgument(nt.action, err.Error()))
	}
	_, exists := nt.working[typeName][id]
	switch op {
	case opCreate:
		if exists {
			return nt.poison(oerr.InvalidArgument(nt.action, "instance already exists"))
		}
	case opUpdate, opDelete:
		if !exists {
			return nt.poison(oerr.InvalidArgument(nt.action, "target instance does not exist"))
		}
	}
	if err := nt.firePre(typeName); err != nil {
		return nt.poison(err)
	}
	if _, ok := nt.working[typeName]; !ok {
		nt.working[typeName] = map[string]txn.Value{}
	}
	if len(nt.touched) == 0 || func() bool {
		for _, t := range nt.touched {
			if t == typeName {
				return false
			}
		}
		return true
	}() {
		nt.touched = append(nt.touched, typeName)
	}
	switch op {
	case opCreate:
		nt.working[typeName][id] = cloneValue(props)
	case opUpdate:
		merged := nt.working[typeName][id]
		for k, v := range props {
			merged[k] = v
		}
	case opDelete:
		delete(nt.working[typeName], id)
	}
	nt.log = append(nt.log, fmt.Sprintf("%d:%s/%s", op, typeName, id))
	return nil
}

func (nt *naiveTxn) Create(t, id string, p map[string]any) error { return nt.write(opCreate, t, id, p) }
func (nt *naiveTxn) Update(t, id string, p map[string]any) error { return nt.write(opUpdate, t, id, p) }
func (nt *naiveTxn) Delete(t, id string) error                   { return nt.write(opDelete, t, id, nil) }

func (nt *naiveTxn) Invoke(name string, params map[string]any) error {
	if nt.failed != nil {
		return nt.failed
	}
	def, ok := nt.m.defs[name]
	if !ok {
		return nt.poison(oerr.InvalidArgument(nt.action, "unknown action definition"))
	}
	if err := def.Run(nt, params); err != nil {
		return nt.poison(err)
	}
	return nt.failed
}

func (nt *naiveTxn) Get(typeName, id string) (txn.Value, bool) {
	v, ok := nt.working[typeName][id]
	return v, ok
}

// execute runs one outermost action on the naive model.
func (m *naiveModel) execute(name string, params map[string]any) error {
	def, ok := m.defs[name]
	if !ok {
		return oerr.InvalidArgument(name, "unknown action definition")
	}
	m.txnSeq++
	nt := &naiveTxn{
		m:        m,
		working:  deepCopyState(m.committed),
		preFired: map[string]bool{},
		txnID:    m.txnSeq,
		action:   name,
	}
	err := def.Run(nt, params)
	if nt.failed != nil {
		err = nt.failed
	}
	if err != nil {
		return err // rollback: working copy is simply discarded
	}
	var failures []oerr.HookFailure
	final := naiveSnap{deepCopyState(nt.working)}
	for _, typeName := range nt.touched {
		for _, h := range m.post[typeName] {
			nt.record(hooks.Post, typeName, h.name)
			if err := h.logic(final); err != nil {
				failures = append(failures, oerr.HookFailure{TypeName: typeName, HookName: h.name, Message: err.Error()})
			}
		}
	}
	if len(failures) > 0 {
		return oerr.PostHook(name, failures)
	}
	m.committed = nt.working // commit: swap in the working copy
	return nil
}

// ---------- random nested sequence generator ----------

type opSpec struct {
	op    string // "create" | "update" | "delete"
	typ   string
	id    string
	props map[string]any
}

type callSpec struct {
	ops      []opSpec
	children []*callSpec
}

func (c *callSpec) params() map[string]any {
	return map[string]any{"spec": c}
}

func genCall(r *rand.Rand, depth int, ids []string, exists func(typ, id string) bool) *callSpec {
	c := &callSpec{}
	for i, n := 0, r.Intn(3); i < n; i++ {
		typ := []string{"A", "B"}[r.Intn(2)]
		var op string
		switch r.Intn(10) {
		case 0, 1, 2, 3, 4, 5:
			op = "create"
		case 6, 7, 8:
			op = "update"
		default:
			op = "delete"
		}
		// State-aware id choice: creates target absent ids, updates and
		// deletes target present ones, so most calls are valid; intra-tree
		// conflicts and hooks still produce failures.
		cand := []string{}
		for _, id := range ids {
			if (op == "create") != exists(typ, id) {
				cand = append(cand, id)
			}
		}
		id := ids[r.Intn(len(ids))]
		if len(cand) > 0 && r.Intn(10) > 0 {
			id = cand[r.Intn(len(cand))]
		}
		var props map[string]any
		if op != "delete" {
			if typ == "A" {
				props = map[string]any{"n": r.Intn(10)}
			} else {
				props = map[string]any{"s": fmt.Sprintf("v%d", r.Intn(10))}
			}
			if r.Intn(20) == 0 { // rare type-mismatched payload
				props = map[string]any{"bad": true}
			}
		}
		c.ops = append(c.ops, opSpec{op: op, typ: typ, id: id, props: props})
	}
	if depth > 0 {
		for i, n := 0, r.Intn(3); i < n; i++ {
			c.children = append(c.children, genCall(r, depth-1, ids, exists))
		}
	}
	return c
}

// nodeAction is the single recursive action definition used by both sides.
var nodeAction = Definition{Name: "node", Run: func(ctx Ctx, p map[string]any) error {
	spec := p["spec"].(*callSpec)
	for _, op := range spec.ops {
		var err error
		switch op.op {
		case "create":
			err = ctx.Create(op.typ, op.id, op.props)
		case "update":
			err = ctx.Update(op.typ, op.id, op.props)
		case "delete":
			err = ctx.Delete(op.typ, op.id)
		}
		if err != nil {
			return err
		}
	}
	for _, child := range spec.children {
		if err := ctx.Invoke("node", child.params()); err != nil {
			return err
		}
	}
	return nil
}}

// sharedHookLogic returns the deterministic hook logic used by both sides.
func sharedHookLogic() (preA, preB, postA, postB func(s snap) error) {
	preA = func(s snap) error { // fires on first A write; sees outer writes
		if s.Count("A") >= 5 {
			return fmt.Errorf("too many A instances: %d", s.Count("A"))
		}
		return nil
	}
	preB = func(s snap) error {
		if _, ok := s.Get("B", "id0"); ok {
			if _, ok2 := s.Get("A", "id0"); ok2 {
				return fmt.Errorf("id0 may not exist in both A and B")
			}
		}
		return nil
	}
	postA = func(s snap) error {
		if s.Count("A")%2 == 1 {
			return fmt.Errorf("odd number of A instances: %d", s.Count("A"))
		}
		return nil
	}
	postB = func(s snap) error {
		if s.Count("B") > 2 {
			return fmt.Errorf("too many B instances: %d", s.Count("B"))
		}
		return nil
	}
	return
}

func TestConformanceWithNaiveModel(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2024} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runConformance(t, seed)
		})
	}
}

func runConformance(t *testing.T, seed int64) {
	r := rand.New(rand.NewSource(seed))
	ids := []string{"id0", "id1", "id2", "id3", "id4", "id5"}
	preA, preB, postA, postB := sharedHookLogic()

	// Real engine.
	reg := hooks.NewRegistry()
	rec := hooks.NewRecorder()
	reg.RegisterPre("A", "preA", func(c hooks.Context) error { return preA(c.View) })
	reg.RegisterPre("B", "preB", func(c hooks.Context) error { return preB(c.View) })
	reg.RegisterPost("A", "postA", func(c hooks.Context) error { return postA(c.View) })
	reg.RegisterPost("B", "postB", func(c hooks.Context) error { return postB(c.View) })
	e := NewEngine(reg, rec)
	defer e.Close()
	e.RegisterType(ObjectType{Name: "A", Props: map[string]PropType{"n": Int}})
	e.RegisterType(ObjectType{Name: "B", Props: map[string]PropType{"s": String}})
	e.RegisterAction(nodeAction)

	// Naive model.
	m := &naiveModel{
		types:     map[string]ObjectType{"A": e.types["A"], "B": e.types["B"]},
		defs:      map[string]Definition{"node": nodeAction},
		pre:       map[string][]naiveHook{"A": {{"preA", preA}}, "B": {{"preB", preB}}},
		post:      map[string][]naiveHook{"A": {{"postA", postA}}, "B": {{"postB", postB}}},
		committed: map[string]map[string]txn.Value{},
	}

	const calls = 120
	outcomes := map[string]int{}
	// Bootstrap: create id0..id2 of both types so updates and deletes have
	// valid targets from the start.
	for _, typ := range []string{"A", "B"} {
		for _, id := range ids[:3] {
			var props map[string]any
			if typ == "A" {
				props = map[string]any{"n": 1}
			} else {
				props = map[string]any{"s": "boot"}
			}
			spec := &callSpec{ops: []opSpec{{op: "create", typ: typ, id: id, props: props}}}
			// Hooks may reject some bootstrap calls; both sides run the
			// same calls and stay in sync regardless.
			e.Execute("node", spec.params())
			m.execute("node", spec.params())
		}
	}
	for i := 0; i < calls; i++ {
		exists := func(typ, id string) bool {
			_, ok := m.committed[typ][id]
			return ok
		}
		spec := genCall(r, 3, ids, exists)
		params := spec.params()

		realRes := e.Execute("node", params)
		naiveErr := m.execute("node", params)
		if k, ok := oerr.AsKind(realRes.Err); ok {
			outcomes[k.String()]++
		} else {
			outcomes["success"]++
		}

		realKind, realOK := oerr.AsKind(realRes.Err)
		naiveKind, naiveOK := oerr.AsKind(naiveErr)
		if realOK != naiveOK || (realOK && realKind != naiveKind) {
			t.Fatalf("call %d: error kind mismatch: real=%v naive=%v\ninput=%+v",
				i, realRes.Err, naiveErr, spec)
		}
		// Compare aggregated post-hook failures element by element.
		if realOK && realKind == oerr.KindPostHook {
			rf := realRes.Err.(*oerr.Error).Failures
			nf := naiveErr.(*oerr.Error).Failures
			if fmt.Sprintf("%v", rf) != fmt.Sprintf("%v", nf) {
				t.Fatalf("call %d: aggregate mismatch:\nreal=%v\nnaive=%v", i, rf, nf)
			}
		}
		// Compare full committed state after every call.
		for _, typ := range []string{"A", "B"} {
			for _, id := range ids {
				rv, rok := e.Get(typ, id)
				nv, nok := m.committed[typ][id]
				if rok != nok || fmt.Sprintf("%v", rv) != fmt.Sprintf("%v", nv) {
					t.Fatalf("call %d: state divergence at %s/%s: real=%v(%v) naive=%v(%v)\ninput=%+v",
						i, typ, id, rv, rok, nv, nok, spec)
				}
			}
		}
		if i < 3 || realRes.Err != nil {
			t.Logf("call %d input=%+v", i, spec)
			t.Logf("call %d output: real=%v naive=%v", i, realRes.Err, naiveErr)
			t.Logf("call %d basis: same bodies+hooks; real=(overlay txn, O(1) view) vs naive=(write log, deep-copy snapshots)", i)
		}
	}

	// Compare the complete hook firing records.
	realRecs := rec.Records()
	if len(realRecs) != len(m.records) {
		t.Fatalf("hook record count: real=%d naive=%d", len(realRecs), len(m.records))
	}
	for i := range realRecs {
		a, b := realRecs[i], m.records[i]
		if a != b {
			t.Fatalf("hook record %d mismatch:\nreal=%+v\nnaive=%+v", i, a, b)
		}
	}
	t.Logf("seed=%d: %d calls, %d hook records, final state equal, outcomes=%v",
		seed, calls, len(realRecs), outcomes)
	if outcomes["success"] == 0 || outcomes[oerr.KindPreHook.String()] == 0 ||
		outcomes[oerr.KindPostHook.String()] == 0 || outcomes[oerr.KindInvalidArgument.String()] == 0 {
		t.Fatalf("seed=%d: random sequence did not exercise all paths: %v", seed, outcomes)
	}
}
