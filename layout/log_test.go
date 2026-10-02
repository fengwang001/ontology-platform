package layout

import "testing"

func TestLoggedDecisionTrace(t *testing.T) {
	lines := []string{
		"if x:",
		"    foo(1,",
		"  2)",
		"    # c",
		"  bar",
		"bar",
	}
	state, _ := New(10, 10)

	for index, line := range lines {
		events, err := state.Feed([]byte(line))
		basis := "accepted: indentation, scan, logical completion"
		if err != nil {
			basis = "rejected: bad dedent, state remains unchanged"
			t.Logf("line=%d input=%q output=error=%v basis=%s snapshot=%+v",
				index, line, err, basis, state.Snapshot())
			continue
		}
		types := make([]EventType, len(events))
		for eventIndex, event := range events {
			types[eventIndex] = event.Type
		}
		t.Logf("line=%d input=%q output=%v basis=%s snapshot=%+v",
			index, line, types, basis, state.Snapshot())
	}

	closeEvents, err := state.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input=close output=%v basis=all blocks closed and no open logical line snapshot=%+v",
		closeEvents, state.Snapshot())
}
