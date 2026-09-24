package norm

import (
	"errors"
	"fmt"
	"ontology/rfold"
	"reflect"
)

// Shorthands shared by the self-check and the in-package white-box test.
func up(k string, v int64) rfold.Op      { return rfold.Op{Kind: rfold.OpUpsert, Key: k, Val: v} }
func dl(k string) rfold.Op               { return rfold.Op{Kind: rfold.OpDelete, Key: k} }
func ins(k string, v int64) rfold.Change { return rfold.Change{Kind: rfold.ChgInsert, Key: k, Val: v} }
func ret(k string, v int64) rfold.Change { return rfold.Change{Kind: rfold.ChgRetract, Key: k, Val: v} }

// SelfCheck runs the NOTES.md section-3 seven batches and verifies invariants
// I1-I4 plus the touched-keys-only complexity bound.
func SelfCheck() error {
	c := New(1 << 20)
	batches := [][]rfold.Op{
		{up("a", 1), up("b", 1), up("a", 2)}, {dl("a"), up("a", 2)},
		{dl("c"), up("c", 5), dl("c")}, {up("b", 1)},
		{up("b", 3), dl("a")}, {dl("a"), up("a", 7)},
		{up("d", 1), dl("d"), dl("b")},
	}
	want := [][]rfold.Change{
		{ins("a", 2), ins("b", 1)}, nil, nil, nil,
		{ret("b", 1), ins("b", 3), ret("a", 2)},
		{ins("a", 7)}, {ret("b", 3)},
	}
	clone := func(m map[string]int64) map[string]int64 {
		out := make(map[string]int64, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	minCount := func(pre map[string]int64, b []rfold.Op) int { // I3 oracle
		post := clone(pre)
		naiveApply(post, b)
		w := 0
		for k, pv := range pre {
			av, ok := post[k]
			if !ok {
				w++
			} else if av != pv {
				w += 2
			}
		}
		for k := range post {
			if _, ok := pre[k]; !ok {
				w++
			}
		}
		return w
	}
	naive := map[string]int64{}
	for i, b := range batches {
		pre := c.Snapshot()
		got, err := c.Apply(b)
		if err != nil {
			return fmt.Errorf("B%d: %w", i+1, err)
		}
		if !reflect.DeepEqual(got, want[i]) {
			return fmt.Errorf("B%d folded %v want %v", i+1, got, want[i])
		}
		if minCount(pre, b) != len(got) {
			return fmt.Errorf("I3 at B%d: emitted %d want %d", i+1, len(got), minCount(pre, b))
		}
		naiveApply(naive, b)
	}
	lg := c.Log()
	if len(lg) != 7 {
		return fmt.Errorf("folded total %d want 7", len(lg))
	}
	tbl := map[string]int64{}
	for i, ch := range lg { // I2: every prefix must replay
		v, ok := tbl[ch.Key]
		switch ch.Kind {
		case rfold.ChgRetract:
			if !ok || v != ch.Val {
				return fmt.Errorf("I2 at %d: invalid retract %s", i, ch)
			}
			delete(tbl, ch.Key)
		case rfold.ChgInsert:
			if ok {
				return fmt.Errorf("I2 at %d: insert over %s", i, ch.Key)
			}
			tbl[ch.Key] = ch.Val
		default:
			return fmt.Errorf("I2 at %d: unknown kind", i)
		}
	}
	if !reflect.DeepEqual(tbl, naive) {
		return errors.New("I1: log replay != naive op replay")
	}
	if !reflect.DeepEqual(tbl, c.Snapshot()) {
		return errors.New("I1: log replay != snapshot")
	}
	z := New(1) // I4: three distinct sentinels, no trace, still usable
	if _, err := z.Apply([]rfold.Op{up("x", 1)}); err != nil {
		return err
	}
	for _, tc := range []struct {
		b    []rfold.Op
		want error
	}{
		{[]rfold.Op{up("", 1)}, rfold.ErrEmptyKey},
		{[]rfold.Op{{Kind: rfold.OpKind(7), Key: "y"}}, rfold.ErrInvalidOp},
		{[]rfold.Op{up("y", 2)}, ErrTooManyKeys},
	} {
		snap, n0 := z.Snapshot(), len(z.Log())
		if _, err := z.Apply(tc.b); !errors.Is(err, tc.want) {
			return fmt.Errorf("I4: got %v want %v", err, tc.want)
		}
		if !reflect.DeepEqual(z.Snapshot(), snap) || len(z.Log()) != n0 {
			return errors.New("I4: rejected batch left state behind")
		}
	}
	if _, err := z.Apply([]rfold.Op{up("x", 9)}); err != nil {
		return fmt.Errorf("unusable after rejection: %w", err)
	}
	for _, m := range []int{100, 1000, 10000} { // complexity: O(touched)
		n := New(1 << 20)
		for i := 0; i < m; i++ {
			if _, err := n.Apply([]rfold.Op{up(fmt.Sprintf("k%05d", i), int64(i))}); err != nil {
				return err
			}
		}
		if _, err := n.Apply([]rfold.Op{up("k00000", -1)}); err != nil {
			return err
		}
		if got := lastCheckedOf(n); got > 3 {
			return fmt.Errorf("complexity m=%d single checked %d", m, got)
		}
		for _, t := range []int{1, 7, 50} {
			b := make([]rfold.Op, t)
			for j := range b {
				b[j] = up(fmt.Sprintf("z%d-%d", m, j), 1)
			}
			if _, err := n.Apply(b); err != nil {
				return err
			}
			if got := lastCheckedOf(n); got > t+2 {
				return fmt.Errorf("complexity m=%d t=%d checked %d", m, t, got)
			}
		}
	}
	return nil
}
