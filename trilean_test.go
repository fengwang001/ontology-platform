package ontology

import "testing"

var triValues = []Trilean{False, True, Unknown}

func triName(v Trilean) string { return v.String() }

func TestAndTruthTable(t *testing.T) {
	want := map[Trilean]map[Trilean]Trilean{
		False:   {False: False, True: False, Unknown: False},
		True:    {False: False, True: True, Unknown: Unknown},
		Unknown: {False: False, True: Unknown, Unknown: Unknown},
	}
	for _, a := range triValues {
		for _, b := range triValues {
			if got := And(a, b); got != want[a][b] {
				t.Errorf("And(%s, %s) = %s, want %s",
					triName(a), triName(b), triName(got), triName(want[a][b]))
			}
		}
	}
}

func TestOrTruthTable(t *testing.T) {
	want := map[Trilean]map[Trilean]Trilean{
		False:   {False: False, True: True, Unknown: Unknown},
		True:    {False: True, True: True, Unknown: True},
		Unknown: {False: Unknown, True: True, Unknown: Unknown},
	}
	for _, a := range triValues {
		for _, b := range triValues {
			if got := Or(a, b); got != want[a][b] {
				t.Errorf("Or(%s, %s) = %s, want %s",
					triName(a), triName(b), triName(got), triName(want[a][b]))
			}
		}
	}
}

func TestNotTruthTable(t *testing.T) {
	want := map[Trilean]Trilean{False: True, True: False, Unknown: Unknown}
	for _, a := range triValues {
		if got := Not(a); got != want[a] {
			t.Errorf("Not(%s) = %s, want %s", triName(a), triName(got), triName(want[a]))
		}
	}
}

// TestTreeKleene verifies the same truth tables through whole-tree
// evaluation, combining leaf outcomes via And/Or/Not nodes.
func TestTreeKleene(t *testing.T) {
	ev := NewEvaluator(8)
	leafFor := func(v Trilean) *Predicate {
		switch v {
		case True:
			return Eq("x", int64(1))
		case False:
			return Eq("x", int64(2))
		default:
			return Eq("missing", int64(1))
		}
	}
	props := map[string]any{"x": int64(1)}

	andWant := map[Trilean]map[Trilean]Trilean{
		False:   {False: False, True: False, Unknown: False},
		True:    {False: False, True: True, Unknown: Unknown},
		Unknown: {False: False, True: Unknown, Unknown: Unknown},
	}
	orWant := map[Trilean]map[Trilean]Trilean{
		False:   {False: False, True: True, Unknown: Unknown},
		True:    {False: True, True: True, Unknown: True},
		Unknown: {False: Unknown, True: True, Unknown: Unknown},
	}
	for _, a := range triValues {
		for _, b := range triValues {
			got, err := ev.Eval(AndP(leafFor(a), leafFor(b)), props, ev.NewResult())
			if err != nil || got != andWant[a][b] {
				t.Errorf("tree And(%s, %s) = %s, %v", triName(a), triName(b), triName(got), err)
			}
			got, err = ev.Eval(OrP(leafFor(a), leafFor(b)), props, ev.NewResult())
			if err != nil || got != orWant[a][b] {
				t.Errorf("tree Or(%s, %s) = %s, %v", triName(a), triName(b), triName(got), err)
			}
		}
	}
	for _, a := range triValues {
		want := Not(a)
		got, err := ev.Eval(NotP(leafFor(a)), props, ev.NewResult())
		if err != nil || got != want {
			t.Errorf("tree Not(%s) = %s, %v, want %s", triName(a), triName(got), err, triName(want))
		}
	}
}
