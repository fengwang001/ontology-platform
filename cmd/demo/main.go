package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"ontology/gagg"
	"ontology/gdelta"
)

var fails int

func check(name string, ok bool) {
	if !ok {
		fails++
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK   " + name)
}

func main() {
	// gdelta: same-key update is one net change; no-op update emits nothing.
	old := gdelta.Row{G: "a", V: 5}
	same := gdelta.Row{G: "a", V: 8}
	got := gdelta.Entries(&old, &same, gdelta.Agg{Sum: 0, Count: 2}, gdelta.Agg{Sum: 0, Count: 2})
	want := []gdelta.Change{
		{Retract: true, G: "a", Sum: 0, Count: 2},
		{G: "a", Sum: 3, Count: 2},
	}
	check("gdelta: same-key update single pair", reflect.DeepEqual(got, want))
	check("gdelta: no-op update emits nothing", len(gdelta.Entries(&old, &old, gdelta.Agg{Sum: 3, Count: 2}, gdelta.Agg{Sum: 3, Count: 2})) == 0)

	// gagg: four distinguishable errors; rejected ops leave no trace.
	st := gagg.New(1)
	_, _ = st.Apply([]gdelta.Op{{Kind: gdelta.Insert, ID: 1, G: "a", V: 1}})
	base := st.View()
	bads := []struct {
		op  gdelta.Op
		err error
	}{
		{gdelta.Op{Kind: gdelta.Insert, ID: 1, G: "b"}, gagg.ErrRowExists},
		{gdelta.Op{Kind: gdelta.Update, ID: 9, G: "b"}, gagg.ErrRowNotFound},
		{gdelta.Op{Kind: gdelta.Insert, ID: 2}, gagg.ErrEmptyGroup},
		{gdelta.Op{Kind: gdelta.Insert, ID: 2, G: "b"}, gagg.ErrTooManyGroups},
	}
	okE, okS := true, true
	seen := map[error]bool{}
	for _, b := range bads {
		_, err := st.Apply([]gdelta.Op{b.op})
		if !errors.Is(err, b.err) || seen[err] {
			okE = false
		}
		seen[err] = true
		if !reflect.DeepEqual(st.View(), base) {
			okS = false
		}
	}
	check("gagg: four distinguishable errors", okE)
	check("gagg: rejected op leaves state unchanged", okS)

	if fails > 0 {
		os.Exit(1)
	}
}
