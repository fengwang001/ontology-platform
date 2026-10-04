package timeline_test

import (
	"errors"
	"testing"

	"ontology/internal/timeline"
	"ontology/override"
	"ontology/playout"
	"ontology/slot"
)

// op fields: name, now, id, start, dur, fixed (schedule only), wantErr.
type op struct {
	name    string
	now     int64
	id      string
	start   int64
	dur     int64
	fixed   bool
	wantErr error
}

type atCheck struct {
	t   int64
	res timeline.Result
}

type tableCase struct {
	name   string
	f      int64
	ops    []op
	checks []atCheck
}

func mustChannel(t *testing.T, f int64) *timeline.Timeline {
	t.Helper()
	c, err := slot.New(f)
	if err != nil {
		t.Fatalf("slot.New(%d): %v", f, err)
	}
	return c
}

func (o op) run(c *timeline.Timeline) error {
	switch o.name {
	case "schedule":
		return slot.Schedule(c, o.now, o.id, o.start, o.dur, o.fixed)
	case "preempt":
		return override.Preempt(c, o.now, o.id, o.start, o.dur)
	case "shift":
		return override.Shift(c, o.now, o.id, o.start, o.dur)
	case "cancel":
		return override.Cancel(c, o.now, o.id)
	default:
		panic("unknown op " + o.name)
	}
}

func runCase(t *testing.T, tc tableCase) *timeline.Timeline {
	t.Helper()
	c := mustChannel(t, tc.f)
	for i, o := range tc.ops {
		err := o.run(c)
		if !errors.Is(err, o.wantErr) {
			t.Fatalf("%s op#%d %+v: got %v want %v", tc.name, i, o, err, o.wantErr)
		}
	}
	for _, ck := range tc.checks {
		got, err := playout.At(c, ck.t)
		if err != nil {
			t.Fatalf("%s At(%d): %v", tc.name, ck.t, err)
		}
		if got != ck.res {
			t.Fatalf("%s At(%d) = %+v want %+v", tc.name, ck.t, got, ck.res)
		}
	}
	return c
}

func prog(id string, off int64) timeline.Result {
	return timeline.Result{Kind: playout.KindProgram, ID: id, Offset: off}
}
func ovr(id string, off int64) timeline.Result {
	return timeline.Result{Kind: playout.KindOverride, ID: id, Offset: off}
}
func filler(off int64) timeline.Result {
	return timeline.Result{Kind: playout.KindFiller, Offset: off}
}

func TestTableDriven(t *testing.T) {
	cases := []tableCase{
		{
			name: "spec example preempt",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 100, false, nil},
				{"schedule", 0, "B", 200, 60, false, nil},
				{"schedule", 0, "C", 300, 100, true, nil},
				{"preempt", 0, "X", 150, 30, false, nil},
			},
			checks: []atCheck{{50, filler(1)}, {160, ovr("X", 10)}, {190, prog("A", 90)}, {270, filler(3)}},
		},
		{
			name: "spec example shift d=30",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 100, false, nil},
				{"schedule", 0, "B", 200, 60, false, nil},
				{"schedule", 0, "C", 300, 100, true, nil},
				{"shift", 0, "Y", 150, 30, false, nil},
			},
			checks: []atCheck{
				{160, ovr("Y", 10)}, {179, ovr("Y", 29)}, {185, prog("A", 55)},
				{230, prog("B", 0)}, {289, prog("B", 59)}, {295, filler(5)},
			},
		},
		{
			name: "shift d=40 gap exactly absorbs",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 100, false, nil},
				{"schedule", 0, "B", 200, 60, false, nil},
				{"schedule", 0, "C", 300, 100, true, nil},
				{"shift", 0, "Y", 150, 40, false, nil},
			},
			checks: []atCheck{{240, prog("B", 0)}, {299, prog("B", 59)}, {300, prog("C", 0)}},
		},
		{
			name: "shift d=41 crowds fixed",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 100, false, nil},
				{"schedule", 0, "B", 200, 60, false, nil},
				{"schedule", 0, "C", 300, 100, true, nil},
				{"shift", 0, "Y", 150, 41, false, timeline.ErrCrowdFixed},
			},
			checks: []atCheck{{200, prog("B", 0)}, {259, prog("B", 59)}, {300, prog("C", 0)}},
		},
		{
			name: "preempt then earlier shift conflicts",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 100, false, nil},
				{"preempt", 0, "X", 150, 30, false, nil},
				{"shift", 0, "Y", 120, 10, false, timeline.ErrOverride},
			},
			checks: []atCheck{{160, ovr("X", 10)}},
		},
		{
			name: "shift at fixed start is inside fixed",
			f:    7,
			ops: []op{
				{"schedule", 0, "C", 300, 100, true, nil},
				{"shift", 0, "Y", 300, 10, false, timeline.ErrInFixed},
			},
			checks: []atCheck{{350, prog("C", 50)}},
		},
		{
			name: "abutting is not overlap and start==now allowed",
			f:    7,
			ops: []op{
				{"schedule", 10, "A", 10, 50, false, nil},
				{"schedule", 10, "B", 60, 40, false, nil},
			},
			checks: []atCheck{{59, prog("A", 49)}, {60, prog("B", 0)}},
		},
		{
			name: "shift starting exactly on floating start moves whole segment",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 50, false, nil},
				{"schedule", 0, "B", 150, 50, false, nil},
				{"shift", 0, "Y", 150, 20, false, nil},
			},
			checks: []atCheck{{149, prog("A", 49)}, {170, prog("B", 0)}, {219, prog("B", 49)}},
		},
		{
			name: "shift starting in gap",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 50, false, nil},
				{"schedule", 0, "B", 200, 50, false, nil},
				{"shift", 0, "Y", 160, 20, false, nil},
			},
			checks: []atCheck{{159, filler(2)}, {179, ovr("Y", 19)}, {180, filler(0)}, {181, filler(1)}, {220, prog("B", 20)}},
		},
		{
			name: "multiple consecutive moved segments",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 10, false, nil},
				{"schedule", 0, "B", 110, 10, false, nil},
				{"schedule", 0, "C", 120, 10, false, nil},
				{"shift", 0, "Y", 105, 5, false, nil},
			},
			checks: []atCheck{
				{104, prog("A", 4)}, {109, ovr("Y", 4)},
				{110, prog("A", 5)}, {115, prog("B", 0)}, {125, prog("C", 0)}, {134, prog("C", 9)},
			},
		},
		{
			name: "split program split again",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 100, false, nil},
				{"shift", 0, "Y1", 120, 10, false, nil},
				{"shift", 5, "Y2", 140, 10, false, nil},
			},
			checks: []atCheck{
				{119, prog("A", 19)}, {129, ovr("Y1", 9)},
				{149, ovr("Y2", 9)}, {159, prog("A", 39)}, {189, prog("A", 69)}, {219, prog("A", 99)},
			},
		},
		{
			name: "cancel truncates multi-segment program",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 100, false, nil},
				{"shift", 0, "Y", 150, 20, false, nil},
				{"cancel", 160, "A", 0, 0, false, timeline.ErrEnded},
			},
			checks: []atCheck{{149, prog("A", 49)}, {169, ovr("Y", 19)}, {171, filler(1)}},
		},
		{
			name: "cancel live multi-segment program returns nil",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 100, false, nil},
				{"shift", 0, "Y", 150, 20, false, nil},
				{"cancel", 180, "A", 0, 0, false, nil},
			},
			checks: []atCheck{{179, prog("A", 59)}, {185, filler(5)}},
		},
		{
			name: "cancel ended keeps identifier",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 50, false, nil},
				{"cancel", 200, "A", 0, 0, false, timeline.ErrEnded},
				{"schedule", 200, "A", 200, 10, false, timeline.ErrDuplicate},
			},
			checks: []atCheck{{149, prog("A", 49)}, {205, filler(6)}},
		},
		{
			name: "cancel preempt restores underlying program",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 100, false, nil},
				{"preempt", 0, "X", 150, 30, false, nil},
				{"cancel", 160, "X", 0, 0, false, nil},
			},
			checks: []atCheck{{160, prog("A", 60)}},
		},
		{
			name: "filler origin ignores preempt but honors shift",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 100, false, nil},
				{"preempt", 0, "X", 250, 20, false, nil},
				{"shift", 10, "Y", 300, 10, false, nil},
			},
			checks: []atCheck{{249, filler(0)}, {269, ovr("X", 19)}, {305, ovr("Y", 5)}, {311, filler(1)}},
		},
		{
			name: "rejected shift leaves state untouched",
			f:    7,
			ops: []op{
				{"schedule", 0, "A", 100, 50, false, nil},
				{"schedule", 0, "C", 200, 50, true, nil},
				{"shift", 0, "Y", 190, 30, false, timeline.ErrCrowdFixed},
			},
			checks: []atCheck{{149, prog("A", 49)}, {150, filler(0)}, {199, filler(0)}, {200, prog("C", 0)}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runCase(t, tc) })
	}
}
