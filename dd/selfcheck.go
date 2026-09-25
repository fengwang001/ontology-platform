package dd

import (
	"errors"
	"fmt"

	"ontology/tup"
)

// recompute derives tuple reference counts, distinct counts and the total
// from scratch by scanning the active rows — the batch oracle for the
// incremental state.
func recompute(rows map[int]row) (map[string]map[tup.T]int, int) {
	batch := make(map[string]map[tup.T]int)
	total := 0
	for _, r := range rows {
		m := batch[r.key]
		if m == nil {
			m = make(map[tup.T]int)
			batch[r.key] = m
		}
		if m[r.t] == 0 {
			total++
		}
		m[r.t]++
	}
	return batch, total
}

// verify compares the incremental state against a batch recomputation.
func (e *Engine) verify() error {
	batch, total := recompute(e.rows)
	got := 0
	var mismatch error
	e.tab.Groups(func(key string, g *tup.Group) {
		want := batch[key]
		for t, n := range g.Snapshot() {
			if n <= 0 || want[t] != n {
				mismatch = fmt.Errorf("dd: refcount mismatch key=%q tuple=%v got=%d want=%d", key, t, n, want[t])
			}
		}
		for t, n := range want {
			if g.Refs(t) != n {
				mismatch = fmt.Errorf("dd: missing live tuple key=%q tuple=%v want=%d", key, t, n)
			}
		}
		if g.Distinct() != len(want) {
			mismatch = fmt.Errorf("dd: distinct mismatch key=%q got=%d want=%d", key, g.Distinct(), len(want))
		}
		got += g.Distinct()
	})
	if mismatch != nil {
		return mismatch
	}
	if got != total || e.Total() != total {
		return fmt.Errorf("dd: total mismatch got=%d exposed=%d want=%d", got, e.Total(), total)
	}
	return nil
}

// SelfCheck replays the specification's eight-step sequence on a private
// engine, re-verifying all four invariants after every operation, then the
// rejection errors and the constant-work bound. It never exposes checks.
func (e *Engine) SelfCheck() error {
	c := New()
	// The specification's eight steps: id/c2/want per step; del marks the
	// two deletes; col1 is "a","a","b","a","a",-,"b",-.
	ids := []int{1, 2, 3, 4, 1, 4, 2, 3}
	c2s := []int{10, 20, 10, 10, 30, 0, 10, 0}
	want := []int{1, 2, 3, 3, 4, 3, 2, 2}
	del := map[int]bool{5: true, 7: true}
	c1s := []string{"a", "a", "b", "a", "a", "", "b", ""}
	for i := range ids {
		var err error
		if del[i] {
			err = c.Delete(ids[i])
		} else {
			err = c.Upsert(ids[i], "k", c1s[i], c2s[i])
		}
		if err != nil {
			return fmt.Errorf("dd: self-check step %d: %w", i+1, err)
		}
		if d := c.Distinct("k"); d != want[i] {
			return fmt.Errorf("dd: self-check step %d distinct=%d want=%d", i+1, d, want[i])
		}
		if err := c.verify(); err != nil {
			return fmt.Errorf("dd: self-check step %d: %w", i+1, err)
		}
	}
	if err := c.rejectChecks(); err != nil {
		return err
	}
	return c.boundChecks()
}

// rejectChecks exercises the three distinct invalid ops and proves state is untouched.
func (e *Engine) rejectChecks() error {
	cases := []struct {
		name string
		err  error
		do   func() error
	}{
		{"delete missing", ErrRowNotFound, func() error { return e.Delete(999) }},
		{"non-positive rowID", ErrRowIDNotPositive, func() error { return e.Upsert(0, "k", "a", 1) }},
		{"empty key", ErrEmptyKey, func() error { return e.Upsert(9, "", "a", 1) }},
	}
	for i, tc := range cases {
		rows, total, dk := len(e.rows), e.Total(), e.Distinct("k")
		if err := tc.do(); !errors.Is(err, tc.err) {
			return fmt.Errorf("dd: self-check %s: got %v want %v", tc.name, err, tc.err)
		}
		for j, oc := range cases {
			if j != i && errors.Is(oc.err, tc.err) {
				return fmt.Errorf("dd: self-check sentinel %s not distinct", tc.name)
			}
		}
		if len(e.rows) != rows || e.Total() != total || e.Distinct("k") != dk {
			return fmt.Errorf("dd: self-check %s changed state", tc.name)
		}
	}
	return nil
}

// boundChecks verifies one tuple-touching operation inspects a constant
// number of tuples regardless of how many tuples the group already holds.
func (e *Engine) boundChecks() error {
	for _, m := range []int{100, 1000, 10000} {
		c := New()
		for i := 1; i <= m; i++ {
			if err := c.Upsert(i, "k", "x", i); err != nil {
				return err
			}
		}
		c.checks = 0
		if err := c.Delete(m); err != nil {
			return err
		}
		if err := c.Upsert(m+1, "k", "x", 1); err != nil {
			return err
		}
		if c.checks > 2 {
			return fmt.Errorf("dd: self-check m=%d inspected %d tuples, want <= 2", m, c.checks)
		}
	}
	return nil
}
