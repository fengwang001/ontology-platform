package exec

import (
	"testing"

	"ontology/action"
)

func linuxp() []action.KV { return []action.KV{kv("os", "linux")} }

func TestTableDriven(t *testing.T) {
	cases := []struct {
		name  string
		M     int
		steps []tableStep
	}{
		{
			name: "spec_example_merge_queue_loss_cache",
			M:    2,
			steps: []tableStep{
				{kind: "reg", worker: "W1", props: []action.KV{kv("os", "linux"), kv("arch", "x86")}, slots: 1},
				{kind: "exec", digest: "d1", plat: plat(kv("os", "linux")), prio: 1, wantID: 1, checkQueue: true, wantQ: []string{"d1"}},
				{kind: "exec", digest: "d2", plat: plat(kv("os", "win")), prio: 5, wantID: 2, checkQueue: true, wantQ: []string{"d2", "d1"}},
				{kind: "exec", digest: "d3", plat: plat(kv("os", "linux")), prio: 1, wantID: 3, checkQueue: true, wantQ: []string{"d2", "d1", "d3"}},
				{kind: "exec", digest: "d3", plat: plat(kv("os", "linux")), prio: 7, wantID: 4, checkQueue: true, wantQ: []string{"d3", "d2", "d1"}},
				{kind: "poll", worker: "W1", wantOK: true, att: 1, opW: 3, checkQueue: true, wantQ: []string{"d2", "d1"}},
				{kind: "cancel", wantWait: 4, wantOut: action.Outcome{Kind: action.Cancelled}, checkQueue: true, wantQ: []string{"d2", "d1"}},
				{kind: "lost", worker: "W1", checkQueue: true, wantQ: []string{"d2", "d1", "d3"}},
				{kind: "reg", worker: "W1", props: []action.KV{kv("os", "linux")}, slots: 1},
				{kind: "poll", worker: "W1", wantOK: true, att: 1, opW: 1, checkQueue: true, wantQ: []string{"d2", "d3"}},
				{kind: "complete", worker: "W1", opW: 1, att: 1, exit: 0,
					wantWait: 1, wantOut: action.Outcome{Kind: action.Result, Exit: 0},
					wantCacheHit: true, wantCacheExit: 0},
				{kind: "poll", worker: "W1", wantOK: true, att: 2, opW: 3},
				{kind: "complete", worker: "W1", opW: 3, att: 1, exit: 0, wantErr: action.ErrAttemptStale},
				{kind: "lost", worker: "W1", wantWait: 3, wantOut: action.Outcome{Kind: action.Lost}},
				{kind: "exec", digest: "d1", plat: plat(kv("os", "linux")), prio: 0,
					wantID: 5, wantWait: 5, wantOut: action.Outcome{Kind: action.Cached, Exit: 0}},
				{kind: "exec", digest: "d1", plat: plat(kv("os", "linux")), prio: 0, skip: true, wantID: 6, checkQueue: true, wantQ: []string{"d2", "d1"}},
			},
		},
		{
			name: "abandoned_completes_nonzero_to_new_waiter",
			M:    2,
			steps: []tableStep{
				{kind: "reg", worker: "W1", props: linuxp(), slots: 1},
				{kind: "exec", digest: "D", plat: plat(kv("os", "linux")), prio: 1, wantID: 1},
				{kind: "poll", worker: "W1", wantOK: true, att: 1, opW: 1},
				{kind: "cancel", wantWait: 1, wantOut: action.Outcome{Kind: action.Cancelled}},
				{kind: "exec", digest: "D", plat: plat(kv("os", "linux")), prio: 2, wantID: 2},
				{kind: "complete", worker: "W1", opW: 1, att: 1, exit: 3,
					wantWait: 2, wantOut: action.Outcome{Kind: action.Result, Exit: 3}},
			},
		},
		{
			name: "abandoned_loss_direct_delete",
			M:    1,
			steps: []tableStep{
				{kind: "reg", worker: "W1", props: linuxp(), slots: 1},
				{kind: "exec", digest: "D", plat: plat(kv("os", "linux")), prio: 1, wantID: 1},
				{kind: "poll", worker: "W1", wantOK: true, att: 1, opW: 1},
				{kind: "cancel", wantWait: 1, wantOut: action.Outcome{Kind: action.Cancelled}},
				{kind: "lost", worker: "W1"},
				{kind: "reg", worker: "W1", props: linuxp(), slots: 1},
				{kind: "exec", digest: "D", plat: plat(kv("os", "linux")), prio: 1, wantID: 2, checkQueue: true, wantQ: []string{"D"}},
			},
		},
		{
			name: "prio_rises_on_attach_falls_on_cancel",
			M:    3,
			steps: []tableStep{
				{kind: "exec", digest: "x", plat: plat(kv("a", "b")), prio: 2, wantID: 1, checkQueue: true, wantQ: []string{"x"}},
				{kind: "exec", digest: "y", plat: plat(kv("a", "b")), prio: 5, wantID: 2, checkQueue: true, wantQ: []string{"y", "x"}},
				{kind: "exec", digest: "x", plat: plat(kv("a", "b")), prio: 9, wantID: 3, checkQueue: true, wantQ: []string{"x", "y"}},
				{kind: "cancel", wantWait: 3, wantOut: action.Outcome{Kind: action.Cancelled}, checkQueue: true, wantQ: []string{"y", "x"}},
			},
		},
		{
			name: "infra_complete_is_loss_reattempt",
			M:    2,
			steps: []tableStep{
				{kind: "reg", worker: "W1", props: linuxp(), slots: 1},
				{kind: "exec", digest: "f", plat: plat(kv("os", "linux")), prio: 1, wantID: 1, checkQueue: true, wantQ: []string{"f"}},
				{kind: "poll", worker: "W1", wantOK: true, att: 1, opW: 1},
				{kind: "complete", worker: "W1", opW: 1, att: 1, infra: true, checkQueue: true, wantQ: []string{"f"}},
				{kind: "poll", worker: "W1", wantOK: true, att: 2, opW: 1},
				{kind: "complete", worker: "W1", opW: 1, att: 2, exit: 0,
					wantWait: 1, wantOut: action.Outcome{Kind: action.Result}, wantCacheHit: true},
			},
		},
		{
			name: "exactly_M_lost",
			M:    1,
			steps: []tableStep{
				{kind: "reg", worker: "W1", props: linuxp(), slots: 1},
				{kind: "exec", digest: "f", plat: plat(kv("os", "linux")), prio: 1, wantID: 1},
				{kind: "poll", worker: "W1", wantOK: true, att: 1, opW: 1},
				{kind: "lost", worker: "W1", wantWait: 1, wantOut: action.Outcome{Kind: action.Lost}},
			},
		},
		{
			name: "nonzero_exit_not_cached",
			M:    2,
			steps: []tableStep{
				{kind: "reg", worker: "W1", props: linuxp(), slots: 1},
				{kind: "exec", digest: "z", plat: plat(kv("os", "linux")), prio: 1, wantID: 1},
				{kind: "poll", worker: "W1", wantOK: true, att: 1, opW: 1},
				{kind: "complete", worker: "W1", opW: 1, att: 1, exit: 7,
					wantWait: 1, wantOut: action.Outcome{Kind: action.Result, Exit: 7}},
				{kind: "exec", digest: "z", plat: plat(kv("os", "linux")), prio: 1, wantID: 2, checkQueue: true, wantQ: []string{"z"}},
			},
		},
		{
			name: "attach_platform_mismatch_invalid",
			M:    2,
			steps: []tableStep{
				{kind: "exec", digest: "g", plat: plat(kv("os", "linux")), prio: 1, wantID: 1},
				{kind: "exec", digest: "g", plat: plat(kv("os", "win")), prio: 1, wantErr: action.ErrInvalid},
				{kind: "exec", digest: "g", plat: plat(kv("os", "linux"), kv("arch", "x86")), prio: 1, wantErr: action.ErrInvalid},
			},
		},
		{
			name: "queued_cancel_last_waiter_deletes",
			M:    2,
			steps: []tableStep{
				{kind: "exec", digest: "q", plat: plat(kv("a", "b")), prio: 1, wantID: 1},
				{kind: "cancel", wantWait: 1, wantOut: action.Outcome{Kind: action.Cancelled}, checkQueue: true, wantQ: []string{}},
				{kind: "exec", digest: "q", plat: plat(kv("a", "b")), prio: 1, wantID: 2, checkQueue: true, wantQ: []string{"q"}},
			},
		},
		{
			name: "double_cancel_state_error",
			M:    2,
			steps: []tableStep{
				{kind: "exec", digest: "q", plat: plat(kv("a", "b")), prio: 1, wantID: 1},
				{kind: "cancel", wantWait: 1, wantOut: action.Outcome{Kind: action.Cancelled}},
				{kind: "cancel", wantWait: 1, wantErr: action.ErrState},
			},
		},
		{
			name: "slot_holder_and_missing_errors",
			M:    2,
			steps: []tableStep{
				{kind: "reg", worker: "W1", props: linuxp(), slots: 1},
				{kind: "exec", digest: "a", plat: plat(kv("os", "linux")), prio: 1, wantID: 1},
				{kind: "exec", digest: "b", plat: plat(kv("os", "linux")), prio: 1, wantID: 2},
				{kind: "poll", worker: "W1", wantOK: true, att: 1, opW: 1},
				{kind: "poll", worker: "W1", wantErr: action.ErrNoFreeSlot},
				{kind: "poll", worker: "NOPE", wantErr: action.ErrNotFound},
				{kind: "complete", worker: "NOPE", opW: 1, att: 1, wantErr: action.ErrNotFound},
				{kind: "complete", worker: "W1", opW: 2, att: 1, wantErr: action.ErrState},
			},
		},
		{
			name: "register_duplicate_and_bad_args",
			M:    2,
			steps: []tableStep{
				{kind: "reg", worker: "W", props: linuxp(), slots: 1},
				{kind: "reg", worker: "W", props: linuxp(), slots: 1, wantErr: action.ErrExists},
				{kind: "reg", worker: "B", props: linuxp(), slots: 65, wantErr: action.ErrInvalid},
				{kind: "reg", worker: "", props: linuxp(), slots: 1, wantErr: action.ErrInvalid},
				{kind: "exec", digest: "", plat: plat(kv("os", "linux")), prio: 1, wantErr: action.ErrInvalid},
				{kind: "exec", digest: "p", plat: plat(kv("os", "linux")), prio: 10, wantErr: action.ErrInvalid},
				{kind: "cancel", wantWait: 0, wantErr: action.ErrInvalid},
				{kind: "cancel", wantWait: 999, wantErr: action.ErrNotFound},
				{kind: "lost", worker: "GHOST", wantErr: action.ErrNotFound},
			},
		},
		{
			name: "skip_cache_and_cached_immediate",
			M:    2,
			steps: []tableStep{
				{kind: "reg", worker: "W1", props: linuxp(), slots: 1},
				{kind: "exec", digest: "c", plat: plat(kv("os", "linux")), prio: 1, skip: true, wantID: 1},
				{kind: "poll", worker: "W1", wantOK: true, att: 1, opW: 1},
				{kind: "complete", worker: "W1", opW: 1, att: 1, exit: 0, wantCacheHit: true},
				{kind: "exec", digest: "c", plat: plat(kv("os", "linux")), prio: 1,
					wantID: 2, wantWait: 2, wantOut: action.Outcome{Kind: action.Cached, Exit: 0}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runTable(t, c.M, c.steps) })
	}
}
