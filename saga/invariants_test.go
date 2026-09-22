package saga

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/journal"
	"ontology/step"
)

// TestInvariants is a single table-driven suite pinning invariants 1 and 2:
// reverse order, failed step not compensated, compensation failures continue.
func TestInvariants(t *testing.T) {
	type want struct {
		status    Status
		failed    []int
		compOrder []int
	}
	cases := []struct {
		name      string
		n         int
		fwdErr    map[int]error
		compErr   map[int]error
		uncertain map[int]bool
		nilComp   map[int]bool
		want      want
	}{
		{
			name: "all succeed", n: 4,
			want: want{status: StatusSucceeded},
		},
		{
			name: "definite failure at 2 compensates 1,0", n: 4,
			fwdErr:    map[int]error{2: step.Definite(errors.New("boom"))},
			want:      want{status: StatusCompensated, compOrder: []int{1, 0}},
		},
		{
			name: "unknown failure at 2 compensates 2,1,0", n: 4,
			fwdErr:    map[int]error{2: errors.New("timeout")},
			want:      want{status: StatusCompensated, compOrder: []int{2, 1, 0}},
		},
		{
			name: "comp fail at 1 still runs 0, partial", n: 4,
			fwdErr:    map[int]error{2: step.Definite(errors.New("x"))},
			compErr:   map[int]error{1: errors.New("comp fail")},
			want:      want{status: StatusPartialComp, failed: []int{1}, compOrder: []int{1, 0}},
		},
		{
			name: "nil compensation recorded as failure", n: 3,
			fwdErr:    map[int]error{2: step.Definite(errors.New("x"))},
			nilComp:   map[int]bool{1: true},
			want:      want{status: StatusPartialComp, failed: []int{1}, compOrder: []int{1, 0}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, _ := newOrchestrator(0)
			cnt := newCounters(tc.n)
			ss := buildSteps(cnt, tc.fwdErr, tc.compErr, nil, tc.nilComp)
			st, err := o.Run(context.Background(), tc.name, ss)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if st.Status != tc.want.status {
				t.Fatalf("status = %s, want %s", st.Status, tc.want.status)
			}
			if !reflect.DeepEqual(st.FailedComp, tc.want.failed) {
				t.Fatalf("failed comp = %v, want %v", st.FailedComp, tc.want.failed)
			}
			if got := compCallOrder(t, o.j, tc.name); tc.want.compOrder != nil &&
				!reflect.DeepEqual(got, tc.want.compOrder) {
				t.Fatalf("comp order = %v, want %v", got, tc.want.compOrder)
			}
			if err := o.SelfCheck(); err != nil {
				t.Fatalf("selfcheck: %v", err)
			}
			// Failed step itself never gets a compensation record (definite case).
			if idx, ok := tc.fwdErrFirstDef(tc); ok {
				for _, r := range o.j.Read(tc.name) {
					if r.Index == idx && (r.Kind == journal.AttemptComp ||
						r.Kind == journal.Compensated || r.Kind == journal.CompFailed) {
						t.Fatalf("failed step %d was compensated", idx)
					}
				}
			}
		})
	}
}

func compCallOrder(t *testing.T, j *journal.Journal, id string) []int {
	t.Helper()
	var order []int
	seen := map[int]bool{}
	for _, r := range j.Read(id) {
		if r.Kind == journal.AttemptComp && !seen[r.Index] {
			seen[r.Index] = true
			order = append(order, r.Index)
		}
	}
	return order
}

func (tc struct {
}) fwdErrFirstDef() {}

var _ = fmt.Sprintf
