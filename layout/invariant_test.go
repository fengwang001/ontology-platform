package layout

import "testing"

func TestIndentStackStrictlyIncreases(t *testing.T) {
	state, _ := New(10, 10)
	feedOK(t, state, "if a:", []EventType{EventNewline})
	feedOK(t, state, "        a", []EventType{EventIndent, EventNewline})
	snapshot := state.Snapshot()
	for i := 1; i < len(snapshot.IndentStack); i++ {
		outer := snapshot.IndentStack[i-1]
		inner := snapshot.IndentStack[i]
		if inner.Column <= outer.Column || inner.AltColumn <= outer.AltColumn {
			t.Fatalf("stack not strictly increasing: %+v -> %+v", outer, inner)
		}
	}
}
