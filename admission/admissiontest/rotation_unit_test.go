package admissiontest

import (
	"reflect"
	"testing"

	"ontology/admission"
)

func cfgRotation(total int64) *admission.Config {
	return &admission.Config{
		TotalSeats: total,
		Levels:     []admission.LevelConfig{{Name: "L", Kind: admission.LevelLimited, Share: 1, QueueLimit: 100, Timeout: 1000}},
		Rules: []admission.Rule{{
			Name: "r", Priority: 0,
			Match:       admission.MatchConditions{},
			TargetLevel: "L", Distinguish: admission.FlowByNamespace,
		}},
	}
}

func reqAt(id, ns string, seats int64, t admission.Time) *admission.Request {
	return &admission.Request{
		ID: id, UserGroup: "", Verb: "put", Resource: "r",
		User: "u", Namespace: ns, Seats: seats,
	}
}

func ids(ev []NaiveEvent) []string {
	var out []string
	for _, e := range ev {
		if e.Kind == "executed" {
			out = append(out, e.ID)
		}
	}
	return out
}

// TestRotationOrdering 三个流 A/B/C 各排队，单席位；逐个完成，验证 A1 B1 C1 A2 B2。
func TestRotationOrdering(t *testing.T) {
	real, err := admission.NewController(cfgRotation(1))
	if err != nil {
		t.Fatal(err)
	}
	model, err := NewNaiveModel(cfgRotation(1))
	if err != nil {
		t.Fatal(err)
	}

	holder := reqAt("h", "h", 1, 0)
	submitBoth(t, real, model, holder, 0)

	queue := []struct{ id, ns string }{
		{"a1", "A"}, {"b1", "B"}, {"c1", "C"}, {"a2", "A"}, {"b2", "B"},
	}
	now := admission.Time(1)
	for _, q := range queue {
		submitBoth(t, real, model, reqAt(q.id, q.ns, 1, now), now)
	}

	prev := "h"
	want := []string{"a1", "b1", "c1", "a2", "b2"}
	for _, w := range want {
		now++
		ar := real.Complete(prev, now)
		nr, nerr := model.Complete(prev, now)
		if nerr != 0 {
			t.Fatalf("naive clock: %v", nerr)
		}
		got := eventIDsReal(ar.Events)
		wan := ids(nr.Events)
		if len(got) != 1 || got[0] != w || !reflect.DeepEqual(got, wan) {
			t.Fatalf("after complete %s: real=%v naive=%v want=[%s]", prev, got, wan, w)
		}
		prev = w
	}
}

// TestTimeoutThenComplete：一个流的等待者超时后，另一个流在完成释放时按轮转顺序出队。
func TestTimeoutThenComplete(t *testing.T) {
	real, _ := admission.NewController(cfgRotation(5))
	model, _ := NewNaiveModel(cfgRotation(5))

	holder := reqAt("h", "H", 4, 0)
	submitBoth(t, real, model, holder, 0)
	// A 宽请求 4，剩 1 座放不下；B 窄请求 1 排队在 A 之后。
	submitBoth(t, real, model, reqAt("wide", "A", 4, 1), 1)
	submitBoth(t, real, model, reqAt("narrow", "B", 1, 1), 1)

	now := admission.Time(2)
	ar := real.Complete("h", now)
	nr, _ := model.Complete("h", now)
	if !reflect.DeepEqual(eventIDsReal(ar.Events), ids(nr.Events)) {
		t.Fatalf("complete after: real=%v naive=%v", eventIDsReal(ar.Events), ids(nr.Events))
	}
}

func submitBoth(t *testing.T, real *admission.Controller, model *NaiveModel, req *admission.Request, now admission.Time) {
	t.Helper()
	ar := real.Submit(req, now)
	nr := model.Submit(req, now)
	if ar.Submit.Decision != nr.Decision || errClassOf(ar.Submit.Err) != nr.ErrClass {
		t.Fatalf("submit %s mismatch real=%v naive=%v", req.ID, ar.Submit.Decision, nr.Decision)
	}
}

func eventIDsReal(ev []admission.Event) []string {
	var out []string
	for _, e := range ev {
		if e.Kind == "executed" {
			out = append(out, e.ID)
		}
	}
	return out
}

func errClassOf(e *admission.AdmissionError) admission.ErrorClass {
	if e == nil {
		return 0
	}
	return e.Class
}
