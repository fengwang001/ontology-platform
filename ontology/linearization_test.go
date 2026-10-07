package ontology

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// tagImpl returns its tag so a test can see which generation a call used.
type tagImpl struct{ tag string }

func (t tagImpl) Pre(any) bool                           { return true }
func (t tagImpl) Execute(*ExecContext, any) (any, error) { return t.tag, nil }
func (t tagImpl) Post(any, any) bool                     { return true }

// naiveModel is the independent "serialize every op to a single thread"
// reference. It replays the observed total order (registry mutation log plus
// call traces) and computes the generation each call MUST have used.
type naiveModel struct {
	current map[[2]string]uint64 // (action,type) -> gen seq
	state   map[[2]string]entryState
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		current: map[[2]string]uint64{},
		state:   map[[2]string]entryState{},
	}
}

func key(a ActionID, t TypeID) [2]string { return [2]string{string(a), string(t)} }

// applyMutation updates the model at a mutation's serial position.
func (m *naiveModel) applyMutation(rec MutationRecord) {
	k := key(rec.Action, rec.Type)
	switch rec.Kind {
	case MutationRegister, MutationReplace:
		m.state[k] = stateRegistered
		m.current[k] = rec.Gen.seq
	case MutationWaive:
		m.state[k] = stateWaived
	}
}

// resolve is the naive chain walk the spec describes, in pure form.
func (m *naiveModel) resolve(chain []TypeID, a ActionID) (genSeq uint64, owner TypeID, ok bool, sawWaive bool) {
	for _, t := range chain {
		switch m.state[key(a, t)] {
		case stateRegistered:
			return m.current[key(a, t)], t, true, sawWaive
		case stateWaived:
			sawWaive = true
		}
	}
	return 0, "", false, sawWaive
}

// chainIDs returns the type-id chain used by the model for a concrete type.
func chainIDs(root, p3, p2, p1, concrete *ObjectType) []TypeID {
	return []TypeID{concrete.ID, p1.ID, p2.ID, p3.ID, root.ID}
}

// TestConcurrentReplaceAndInvokeLinearizable interleaves many replacements
// and invocations under concurrent goroutines, then verifies every call used
// exactly the generation the independent naive model predicts at its serial
// position. A generation that "exists between two replacements but matches no
// serial position" is impossible by construction; this test proves it.
func TestConcurrentReplaceAndInvokeLinearizable(t *testing.T) {
	reg, d, a := newSetup()
	root, p3, p2, p1, concrete := hierarchy()

	// Only concrete registers; chain length to a hit is always exactly 1.
	mustReg(t, reg, a.ID, concrete.ID, tagImpl{tag: "g0"})

	// Record the initial generation position for the model.
	model := newNaiveModel()
	model.current[key(a.ID, concrete.ID)] = reg.MutationLog()[0].Gen.seq
	model.state[key(a.ID, concrete.ID)] = stateRegistered

	obj := &Instance{ID: "o", Type: concrete}

	const numReplace = 40
	const numCallers = 8
	const callsPer = 60

	var wg sync.WaitGroup

	// Replacements serialize through the registry mutex; record every op in
	// the mutation log.
	for i := 0; i < numReplace; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tag := fmt.Sprintf("g%d", i+1)
			_, _ = reg.Replace(a.ID, concrete.ID, tagImpl{tag: tag}, nil, nil)
		}(i)
	}

	// Callers run concurrently; each trace pins CallSeq + GenSeq.
	wg.Add(numCallers)
	for c := 0; c < numCallers; c++ {
		go func() {
			defer wg.Done()
			for j := 0; j < callsPer; j++ {
				_, _, err := d.Invoke(context.Background(), obj, a.ID, "x")
				if err != nil {
					t.Errorf("unexpected invoke error: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()

	// Replay the observed total order. Call positions and mutation positions
	// share the registry's global seq; we merge them by seq.
	type op struct {
		seq   uint64
		isMut bool
		mut   MutationRecord
		trace *DispatchTrace
	}
	var ops []op
	for _, rec := range reg.MutationLog() {
		ops = append(ops, op{seq: rec.Seq, isMut: true, mut: rec})
	}
	for _, tr := range d.Traces() {
		ops = append(ops, op{seq: tr.CallSeq, trace: tr})
	}
	// Stable sort by seq (positions are unique: mutations take even/odd? not
	// necessarily, but seq is unique by construction).
	for i := 0; i < len(ops); i++ {
		for j := i + 1; j < len(ops); j++ {
			if ops[j].seq < ops[i].seq {
				ops[i], ops[j] = ops[j], ops[i]
			}
		}
	}

	callsChecked := 0
	for _, o := range ops {
		if o.isMut {
			model.applyMutation(o.mut)
			continue
		}
		wantSeq, wantOwner, ok, _ := model.resolve(chainIDs(root, p3, p2, p1, concrete), a.ID)
		if !ok {
			t.Fatalf("model says no dispatch at call seq %d", o.seq)
		}
		if o.trace.GenSeq != wantSeq {
			t.Fatalf("call seq %d used gen %d, naive serial model says %d",
				o.seq, o.trace.GenSeq, wantSeq)
		}
		if o.trace.OwnerType != wantOwner {
			t.Fatalf("call seq %d owner %s, model says %s",
				o.seq, o.trace.OwnerType, wantOwner)
		}
		callsChecked++
	}

	if callsChecked != numCallers*callsPer {
		t.Fatalf("checked %d calls, want %d", callsChecked, numCallers*callsPer)
	}
}

// TestDispatchDepthBound proves the walk never inspects more hops than the
// actual depth from the concrete type to the first registered ancestor, and
// that adding many unrelated registered types does not change that length.
func TestDispatchDepthBound(t *testing.T) {
	reg, d, a := newSetup()
	root, _, p2, _, concrete := hierarchy()
	_ = root

	// hierarchy() yields concrete -> p1 -> p2 -> p3 -> root.
	// Register the action on p2 only: concrete->p1->p2 is exactly 3 hops.
	mustReg(t, reg, a.ID, p2.ID, mkImpl("p2"))
	obj := &Instance{ID: "o", Type: concrete}
	_, tr := invokeOK(d, obj, a.ID, "x")
	if tr.Hops != 3 {
		t.Fatalf("want exactly 3 hops to p2, got %d", tr.Hops)
	}

	// Register the same action on many unrelated types. None lies on this
	// instance's chain, so hop count must remain exactly 3.
	for i := 0; i < 200; i++ {
		unrelated := &ObjectType{ID: TypeID(fmt.Sprintf("unrelated-%d", i))}
		mustReg(t, reg, a.ID, unrelated.ID, mkImpl("u"))
	}
	_, tr = invokeOK(d, obj, a.ID, "x")
	if tr.Hops != 3 {
		t.Fatalf("hop count must be independent of unrelated registrations, got %d", tr.Hops)
	}

	// Direct registration bounds hops to 1.
	mustReg(t, reg, a.ID, concrete.ID, mkImpl("c"))
	_, tr = invokeOK(d, obj, a.ID, "x")
	if tr.Hops != 1 || tr.HitBasis != BasisDirect {
		t.Fatalf("direct hit must be 1 hop/direct, got hops=%d basis=%d", tr.Hops, tr.HitBasis)
	}

	// When nothing is registered anywhere on the chain, hops equal chain
	// length (the only case with no "hit depth" to bound against).
	reg2 := NewRegistry()
	d2 := NewDispatcher(reg2)
	reg2.DeclareAction(mkAction("empty"))
	_, tr, err := d2.Invoke(context.Background(), obj, "empty", "x")
	if err == nil {
		t.Fatal("want no-dispatch error")
	}
	if tr.Hops != 5 {
		t.Fatalf("undispatchable walk may touch full chain (5), got %d", tr.Hops)
	}
}
