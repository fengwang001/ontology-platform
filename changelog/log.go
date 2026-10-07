// Package changelog renders the engine's change events as human-readable log
// lines: every change prints its input, the groups touched and the decision
// rationale.
package changelog

import (
	"fmt"
	"io"
	"strings"

	"ontology/ontology"
)

// PrintEvents writes one line per event in a fixed, greppable format.
func PrintEvents(w io.Writer, events []ontology.ChangeEvent) {
	for _, ev := range events {
		fmt.Fprintf(w,
			"[seq=%04d] view=%s op=%s | input={%s} | touched=[%s] | rationale=%s\n",
			ev.Seq, ev.View, ev.Op, ev.Input, strings.Join(ev.Touched, ","), ev.Rationale)
		for _, g := range ev.Touched {
			fmt.Fprintf(w,
				"           group=%-12s before(sum=%.2f,count=%d) -> after(sum=%.2f,count=%d)\n",
				g, ev.Before[g].Sum, ev.Before[g].Count, ev.After[g].Sum, ev.After[g].Count)
		}
	}
}
