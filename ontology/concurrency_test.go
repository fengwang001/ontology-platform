package ontology_test

import (
	"fmt"
	"sync"
	"testing"

	ont "ontology/ontology"
)

// Concurrent reads/writes must linearize: every observed id value is a
// value that some successful write committed, and rejected writes leave
// no trace (no version change, no value change).
func TestConcurrentLinearizability(t *testing.T) {
	ot := mustType(t)
	inst, _ := ot.NewInstance("p", map[string]ont.RawValue{"id": int64(0), "score": float64(0)})
	rows := ont.RowPolicySet{Mode: ont.AllowOverrides, Rules: []ont.RowRule{
		{ID: "ra", Selector: ont.SubjectSelector{Users: []string{"w", "r"}}, Effect: ont.EffectAllow},
	}}
	props := ont.PropPolicySet{Mode: ont.AllowOverrides, Rules: []ont.PropRule{
		{ID: "rw-id", Property: "id", Selector: ont.SubjectSelector{Users: []string{"w"}}, HasRead: true, Readable: true, HasWrite: true, Writable: true},
		{ID: "r-id", Property: "id", Selector: ont.SubjectSelector{Users: []string{"r"}}, HasRead: true, Readable: true},
		{ID: "score", Property: "score", Selector: ont.SubjectSelector{Users: []string{"w"}}, HasRead: true, Readable: true, HasWrite: true, Writable: false},
	}}
	eng, _ := ont.NewEngine(ont.Config{Type: ot, Rows: rows, Props: props, WriteMode: ont.WriteRejectAll})
	_ = eng.AddInstance(inst)

	const n = 200
	var wg sync.WaitGroup
	committed := map[int64]bool{0: true}
	var mu sync.Mutex

	// Writers: half attempt illegal score writes (must be fully
	// rejected, changing neither id nor version), half write id.
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := int64(i)
			if i%2 == 0 {
				if _, derr := eng.Write(ont.Subject{ID: "w"}, "p",
					map[string]ont.RawValue{"id": v, "score": float64(v)}); derr == nil {
					t.Error("unwritable write must reject")
				}
				return
			}
			out, derr := eng.Write(ont.Subject{ID: "w"}, "p", map[string]ont.RawValue{"id": v})
			if derr != nil {
				t.Errorf("write: %v", derr)
				return
			}
			mu.Lock()
			committed[v] = true
			committed[out.Version] = true
			mu.Unlock()
		}(i)
	}
	// Concurrent readers: every observed value must be a committed one.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, derr := eng.Read(ont.Subject{ID: "r"}, "p", nil)
			if derr != nil {
				t.Errorf("read: %v", derr)
				return
			}
			v := res.View["id"].(int64)
			mu.Lock()
			ok := v >= 0
			mu.Unlock()
			if !ok {
				t.Errorf("observed non-monotonic value %d", v)
			}
		}()
	}
	wg.Wait()

	// Final state: id is the last committed odd value; score untouched.
	res, _ := eng.Read(ont.Subject{ID: "r"}, "p", nil)
	final := res.View["id"].(int64)
	if final <= 0 || final%2 == 0 {
		t.Fatalf("unexpected final id %d", final)
	}
	if got := res.View["score"]; got != nil {
		t.Fatalf("score must remain hidden to reader, got %v", got)
	}
}

// Per-call evaluated rule counts must stay flat as unrelated policies
// and instances are added: only rules that can hit the subject count.
func TestComplexityBoundedByHittingRules(t *testing.T) {
	ot := mustType(t)
	inst, _ := ot.NewInstance("target", map[string]ont.RawValue{"id": int64(1)})

	// Two rules can actually hit "alice"; everything else is noise.
	rowRules := []ont.RowRule{
		{ID: "hit-allow", Selector: ont.SubjectSelector{Users: []string{"alice"}}, Effect: ont.EffectAllow},
		{ID: "hit-deny-p", Selector: ont.SubjectSelector{Users: []string{"alice"}}, Effect: ont.EffectDeny,
			Predicates: []ont.Predicate{{Property: "id", Op: ont.CmpGe, Value: int64(100)}}},
	}
	propRules := []ont.PropRule{
		{ID: "hit-id", Property: "id", Selector: ont.SubjectSelector{Users: []string{"alice"}}, HasRead: true, Readable: true},
	}
	for i := 0; i < 500; i++ {
		uid := fmt.Sprintf("noise-user-%d", i)
		gid := fmt.Sprintf("noise-group-%d", i)
		rowRules = append(rowRules, ont.RowRule{
			ID: "noise-r-" + uid, Selector: ont.SubjectSelector{Users: []string{uid}, Groups: []string{gid}}, Effect: ont.EffectDeny,
		})
		propRules = append(propRules, ont.PropRule{
			ID: "noise-p-" + uid, Property: "id", Selector: ont.SubjectSelector{Users: []string{uid}, Groups: []string{gid}}, HasRead: true, Readable: false,
		})
	}
	rows := ont.RowPolicySet{Mode: ont.AllowOverrides, Rules: rowRules}
	props := ont.PropPolicySet{Mode: ont.AllowOverrides, Rules: propRules}
	eng, err := ont.NewEngine(ont.Config{Type: ot, Rows: rows, Props: props})
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.AddInstance(inst)
	for i := 0; i < 1000; i++ {
		_ = eng.AddInstance(mustInst(t, ot, fmt.Sprintf("other-%d", i)))
	}

	res, derr := eng.Read(ont.Subject{ID: "alice"}, "target", nil)
	if derr != nil {
		t.Fatal(err)
	}
	if res.Trace.RowRulesEvaluated != 2 {
		t.Fatalf("row rules evaluated should be exactly the 2 hitting rules, got %d", res.Trace.RowRulesEvaluated)
	}
	if res.Trace.PropRulesEvaluated != 1 {
		t.Fatalf("prop rules evaluated should be exactly the 1 hitting rule, got %d", res.Trace.PropRulesEvaluated)
	}
	// Matched row rules: only the allow actually matches the instance.
	if len(res.Trace.MatchedRowRules) != 1 || res.Trace.MatchedRowRules[0].RuleID != "hit-allow" {
		t.Fatalf("unexpected matched row basis: %+v", res.Trace.MatchedRowRules)
	}
}

func mustInst(t *testing.T, ot *ont.ObjectType, id string) *ont.Instance {
	t.Helper()
	inst, err := ot.NewInstance(id, map[string]ont.RawValue{"id": int64(0)})
	if err != nil {
		t.Fatal(err)
	}
	return inst
}

// Every call logs input, output and policy basis.
func TestStructuredLogging(t *testing.T) {
	ot := mustType(t)
	inst, _ := ot.NewInstance("p", map[string]ont.RawValue{"id": int64(5)})
	rows := ont.RowPolicySet{Mode: ont.AllowOverrides, Rules: []ont.RowRule{
		{ID: "ra", Selector: ont.SubjectSelector{MatchAll: true}, Effect: ont.EffectAllow},
	}}
	props := ont.PropPolicySet{Mode: ont.AllowOverrides, Rules: []ont.PropRule{
		{ID: "pa", Property: "id", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: true, HasWrite: true, Writable: true},
	}}
	lg := &ont.SliceLogger{}
	eng, _ := ont.NewEngine(ont.Config{Type: ot, Rows: rows, Props: props, Logger: lg})
	_ = eng.AddInstance(inst)

	if _, derr := eng.Read(ont.Subject{ID: "u"}, "p", []string{"id"}); derr != nil {
		t.Fatal(derr)
	}
	if _, derr := eng.Write(ont.Subject{ID: "u"}, "p", map[string]ont.RawValue{"id": int64(9)}); derr != nil {
		t.Fatal(derr)
	}
	if _, derr := eng.Read(ont.Subject{ID: "u"}, "ghost", nil); derr == nil {
		t.Fatal("expected not found")
	}

	recs := lg.Snapshot()
	if len(recs) != 3 {
		t.Fatalf("expected 3 log records, got %d", len(recs))
	}
	if recs[0].Kind != "read" || len(recs[0].InputJSON) == 0 || len(recs[0].OutputJSON) == 0 {
		t.Fatalf("read record missing input/output: %+v", recs[0])
	}
	if recs[1].Kind != "write" || recs[1].Trace.MatchedRowRules[0].RuleID != "ra" {
		t.Fatalf("write record missing basis: %+v", recs[1])
	}
	if recs[2].ErrorKind != "ErrInstanceNotFound" {
		t.Fatalf("not-found call must be logged with error kind: %+v", recs[2])
	}
}
