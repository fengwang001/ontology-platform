package broadphase

import (
	"reflect"
	"testing"
)

func box(lx, hx, ly, hy int64) Box {
	return Box{LX: lx, HX: hx, LY: ly, HY: hy}
}

func assertEvents(t *testing.T, got, want Events) {
	t.Helper()
	normalize := func(events Events) Events {
		if len(events.FatEnter) == 0 {
			events.FatEnter = []Pair{}
		}
		if len(events.FatExit) == 0 {
			events.FatExit = []Pair{}
		}
		if len(events.ContactEnter) == 0 {
			events.ContactEnter = []Pair{}
		}
		if len(events.ContactExit) == 0 {
			events.ContactExit = []Pair{}
		}
		return events
	}
	got, want = normalize(got), normalize(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestSpecExample(t *testing.T) {
	bp, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}

	events, err := bp.Insert(1, box(0, 10, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, emptyEvents())

	events, err = bp.Insert(2, box(13, 20, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, Events{
		FatEnter:     []Pair{{1, 2}},
		FatExit:      []Pair{},
		ContactEnter: []Pair{},
		ContactExit:  []Pair{},
	})

	events, err = bp.Insert(3, box(14, 16, 0, 4))
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, Events{
		FatEnter:     []Pair{{2, 3}},
		FatExit:      []Pair{},
		ContactEnter: []Pair{{2, 3}},
		ContactExit:  []Pair{},
	})

	moved, err := bp.Move(2, box(12, 20, 0, 10))
	if err != nil || moved.Refatted {
		t.Fatalf("move = %#v, %v; want no refat", moved, err)
	}

	moved, err = bp.Move(2, box(9, 20, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Refatted || bp.crossed != 0 || bp.checks != 0 {
		t.Fatalf("refat=%v crossed=%d checks=%d, want true/0/0", moved.Refatted, bp.crossed, bp.checks)
	}
	assertEvents(t, moved.Events, Events{
		ContactEnter: []Pair{{1, 2}},
	})

	moved, err = bp.Move(2, box(30, 40, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Refatted || bp.crossed != 3 || bp.checks != 2 {
		t.Fatalf("crossed=%d checks=%d, want 3/2", bp.crossed, bp.checks)
	}
	assertEvents(t, moved.Events, Events{
		FatExit:     []Pair{{1, 2}, {2, 3}},
		ContactExit: []Pair{{1, 2}, {2, 3}},
	})

	moved, err = bp.Move(2, box(14, 20, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Refatted || bp.crossed != 2 || bp.checks != 1 {
		t.Fatalf("crossed=%d checks=%d, want 2/1", bp.crossed, bp.checks)
	}
	assertEvents(t, moved.Events, Events{
		FatEnter:     []Pair{{2, 3}},
		ContactEnter: []Pair{{2, 3}},
	})

	events, err = bp.SetFilter(3, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, Events{
		FatExit:     []Pair{{2, 3}},
		ContactExit: []Pair{{2, 3}},
	})

	events, err = bp.SetFilter(2, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, events, Events{
		FatEnter:     []Pair{{2, 3}},
		ContactEnter: []Pair{{2, 3}},
	})
}
