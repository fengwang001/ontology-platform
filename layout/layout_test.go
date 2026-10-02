package layout

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func feedOK(t *testing.T, state *Layout, line string, want []EventType) {
	t.Helper()
	got, err := state.Feed([]byte(line))
	if err != nil {
		t.Fatalf("Feed(%q): %v", line, err)
	}
	assertEvents(t, got, want)
}

func feedReason(t *testing.T, state *Layout, line string, want ErrorReason) {
	t.Helper()
	_, err := state.Feed([]byte(line))
	assertReason(t, err, want)
}

func assertEvents(t *testing.T, events []Event, want []EventType) {
	t.Helper()
	got := make([]EventType, len(events))
	for i, event := range events {
		got[i] = event.Type
	}
	if len(got) == 0 {
		got = nil
	}
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func assertReason(t *testing.T, err error, want ErrorReason) {
	t.Helper()
	var layoutErr *Error
	if !errors.As(err, &layoutErr) || layoutErr.Reason != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
}

func TestGivenExample(t *testing.T) {
	state, err := New(10, 10)
	if err != nil {
		t.Fatal(err)
	}

	feedOK(t, state, "if x:", []EventType{EventNewline})
	feedOK(t, state, "    foo(1,", []EventType{EventIndent})
	feedOK(t, state, "  2)", []EventType{EventNewline})
	feedOK(t, state, "    # c", nil)

	before := state.Snapshot()
	feedReason(t, state, "  bar", ReasonBadDedent)
	if !reflect.DeepEqual(state.Snapshot(), before) {
		t.Fatalf("rejected Feed changed state:\nbefore %+v\nafter  %+v", before, state.Snapshot())
	}

	feedOK(t, state, "bar", []EventType{EventDedent, EventNewline})

	events, err := state.Close()
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []EventType{EventEndMarker})
}

func TestExpectedAndUnexpectedIndent(t *testing.T) {
	state, _ := New(10, 10)
	feedOK(t, state, "if x:", []EventType{EventNewline})
	feedReason(t, state, "y", ReasonExpectedIndent)
	feedOK(t, state, "  y", []EventType{EventIndent, EventNewline})

	other, _ := New(10, 10)
	feedOK(t, other, "y", []EventType{EventNewline})
	feedReason(t, other, "  z", ReasonUnexpectedIndent)
}

func TestTabConsistency(t *testing.T) {
	spaceStack, _ := New(10, 10)
	feedOK(t, spaceStack, "if x:", []EventType{EventNewline})
	feedOK(t, spaceStack, "        x", []EventType{EventIndent, EventNewline})
	feedReason(t, spaceStack, "\tx", ReasonInconsistentTab)

	tabStack, _ := New(10, 10)
	feedOK(t, tabStack, "if x:", []EventType{EventNewline})
	feedOK(t, tabStack, "\tx", []EventType{EventIndent, EventNewline})
	feedReason(t, tabStack, "        x", ReasonInconsistentTab)

	column, altColumn := measureIndent([]byte(" \t"))
	if column != 8 || altColumn != 2 {
		t.Fatalf("measureIndent = (%d,%d), want (8,2)", column, altColumn)
	}
	column, altColumn = measureIndent([]byte("\t\t"))
	if column != 16 || altColumn != 2 {
		t.Fatalf("measureIndent = (%d,%d), want (16,2)", column, altColumn)
	}
}

func TestDanglingBranches(t *testing.T) {
	state, _ := New(10, 10)
	feedOK(t, state, "if x:", []EventType{EventNewline})
	feedOK(t, state, "  y", []EventType{EventIndent, EventNewline})
	feedOK(t, state, "else:", []EventType{EventDedent, EventNewline})
	feedOK(t, state, "  z", []EventType{EventIndent, EventNewline})
	feedReason(t, state, "else:", ReasonDanglingBranch)

	nested, _ := New(10, 10)
	feedOK(t, nested, "if x:", []EventType{EventNewline})
	feedReason(t, nested, "  else:", ReasonDanglingBranch)

	finallyAfterElse, _ := New(10, 10)
	for _, line := range []string{"try:", "  a", "except:", "  b", "else:", "  c", "finally:", "  d"} {
		if _, err := finallyAfterElse.Feed([]byte(line)); err != nil {
			t.Fatalf("Feed(%q): %v", line, err)
		}
	}

	plain, _ := New(10, 10)
	feedOK(t, plain, "y = 1", []EventType{EventNewline})
	feedReason(t, plain, "else:", ReasonDanglingBranch)
	feedOK(t, plain, "elsewhere = 2", []EventType{EventNewline})
	feedOK(t, plain, "else_ = 3", []EventType{EventNewline})
}

func TestDedentMultipleAndUnknownLevel(t *testing.T) {
	state, _ := New(10, 10)
	feedOK(t, state, "if a:", []EventType{EventNewline})
	feedOK(t, state, "  if b:", []EventType{EventIndent, EventNewline})
	feedOK(t, state, "    c", []EventType{EventIndent, EventNewline})
	feedReason(t, state, " x", ReasonBadDedent)
	feedOK(t, state, "d", []EventType{EventDedent, EventDedent, EventNewline})
}

func TestCommentsStringsAndContinuation(t *testing.T) {
	state, _ := New(10, 10)
	feedOK(t, state, "if a: # c", []EventType{EventNewline})
	feedOK(t, state, "  d = \":\"", []EventType{EventIndent, EventNewline})
	feedOK(t, state, "  e = '):(#'", []EventType{EventNewline})
	feedOK(t, state, "  f = 1 + \\", nil)
	feedOK(t, state, "      2", []EventType{EventNewline})

	feedReason(t, state, "  g = 'abc", ReasonString)
	feedReason(t, state, "  h = \"abc\\", ReasonString)
}

func TestContinuationBlankLineCompletes(t *testing.T) {
	state, _ := New(10, 10)
	feedOK(t, state, "x = 1 + \\", nil)
	feedOK(t, state, "", []EventType{EventNewline})
	events, err := state.Close()
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, []EventType{EventEndMarker})
}

func TestBracketRules(t *testing.T) {
	state, _ := New(10, 2)
	feedOK(t, state, "((", nil)
	feedReason(t, state, "(", ReasonBracketTooDeep)
	feedOK(t, state, "))", []EventType{EventNewline})
	feedReason(t, state, ")", ReasonBracket)

	mismatch, _ := New(10, 10)
	feedReason(t, mismatch, "(]", ReasonBracket)

	blank, _ := New(10, 10)
	feedOK(t, blank, "(", nil)
	feedOK(t, blank, "", nil)
	feedOK(t, blank, "  # comment", nil)
	feedOK(t, blank, ")", []EventType{EventNewline})
}

func TestIndentLimit(t *testing.T) {
	state, _ := New(1, 10)
	feedOK(t, state, "if a:", []EventType{EventNewline})
	feedOK(t, state, " if b:", []EventType{EventIndent, EventNewline})
	feedReason(t, state, "  c", ReasonIndentTooDeep)
}

func TestRejectOrder(t *testing.T) {
	state, _ := New(1, 10)
	feedOK(t, state, "if x:", []EventType{EventNewline})
	feedOK(t, state, " if y:", []EventType{EventIndent, EventNewline})
	feedReason(t, state, "  else: (", ReasonIndentTooDeep)

	dangling, _ := New(10, 10)
	feedOK(t, dangling, "y", []EventType{EventNewline})
	feedReason(t, dangling, "else: '", ReasonDanglingBranch)

	tabs, _ := New(10, 10)
	feedOK(t, tabs, "if x:", []EventType{EventNewline})
	feedOK(t, tabs, "        x", []EventType{EventIndent, EventNewline})
	feedReason(t, tabs, "\tx: '", ReasonInconsistentTab)
}

func TestCloseRejectionsAndClosed(t *testing.T) {
	block, _ := New(10, 10)
	feedOK(t, block, "if x:", []EventType{EventNewline})
	_, err := block.Close()
	assertReason(t, err, ReasonMissingBlock)

	openBracket, _ := New(10, 10)
	feedOK(t, openBracket, "(", nil)
	_, err = openBracket.Close()
	assertReason(t, err, ReasonIncompleteInput)

	closed, _ := New(10, 10)
	if _, err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	feedReason(t, closed, "x", ReasonClosed)
	_, err = closed.Close()
	assertReason(t, err, ReasonClosed)
}

func TestLongLine(t *testing.T) {
	state, _ := New(10, 10)
	line := strings.Repeat("x", 10001)
	feedReason(t, state, line, ReasonLineTooLong)
	if state.ScannedBytes() != 0 {
		t.Fatalf("ScannedBytes = %d, want 0", state.ScannedBytes())
	}
}

func TestScannedByteCounter(t *testing.T) {
	state, _ := New(10, 10)
	accepted := []string{"if x:", "  y + (", "", " # c", " )", "z = 'bad"}
	want := 0
	for index, line := range accepted {
		_, err := state.Feed([]byte(line))
		if err == nil {
			want += len(line)
		}
		if got := state.ScannedBytes(); got != want {
			t.Fatalf("after line %d ScannedBytes = %d, want %d", index, got, want)
		}
	}

	longLine := strings.Repeat("x", 10001)
	feedReason(t, state, longLine, ReasonLineTooLong)
	if got := state.ScannedBytes(); got != want {
		t.Fatalf("rejected long line changed ScannedBytes to %d, want %d", got, want)
	}
}

func TestConcurrentReplay(t *testing.T) {
	lines := [][]byte{
		[]byte("if x:"),
		[]byte("  foo(a,"),
		[]byte("       b)"),
		[]byte("else:"),
		[]byte("  y"),
	}

	var wg sync.WaitGroup
	snapshots := make([]Snapshot, 64)
	eventSlices := make([][]EventType, 64)
	for i := range snapshots {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			state, _ := New(10, 10)
			var events []EventType
			for _, line := range lines {
				got, err := state.Feed(line)
				if err != nil {
					t.Errorf("Feed(%q): %v", line, err)
					return
				}
				for _, event := range got {
					events = append(events, event.Type)
				}
			}
			closeEvents, err := state.Close()
			if err != nil {
				t.Errorf("Close: %v", err)
				return
			}
			for _, event := range closeEvents {
				events = append(events, event.Type)
			}
			snapshots[index] = state.Snapshot()
			eventSlices[index] = events
		}(i)
	}
	wg.Wait()

	wantEvents := []EventType{
		EventNewline,
		EventIndent,
		EventNewline,
		EventDedent,
		EventNewline,
		EventIndent,
		EventNewline,
		EventDedent,
		EventEndMarker,
	}
	for i, events := range eventSlices {
		if !reflect.DeepEqual(events, wantEvents) {
			t.Fatalf("goroutine %d events = %v, want %v", i, events, wantEvents)
		}
		if !snapshots[i].Closed {
			t.Fatalf("goroutine %d was not closed", i)
		}
	}
}
