package api

import (
	"errors"
	"fmt"
	"math/rand"
)

// step is one row of the twelve-step sequence (S set, P savepoint, R rollback, X release).
type step struct {
	kind    byte
	key     string
	val, id int
	want    map[string]int
	wantErr error
}

var twelve = []step{
	{'S', "a", 1, -1, map[string]int{"a": 1}, nil},
	{'P', "", 0, 0, map[string]int{"a": 1}, nil},
	{'S', "a", 2, -1, map[string]int{"a": 2}, nil},
	{'P', "", 0, 1, map[string]int{"a": 2}, nil},
	{'S', "b", 5, -1, map[string]int{"a": 2, "b": 5}, nil},
	{'X', "", 0, 1, map[string]int{"a": 2, "b": 5}, nil},
	{'S', "c", 8, -1, map[string]int{"a": 2, "b": 5, "c": 8}, nil},
	{'R', "", 0, 0, map[string]int{"a": 1}, nil},
	{'S', "b", 3, -1, map[string]int{"a": 1, "b": 3}, nil},
	{'P', "", 0, 2, map[string]int{"a": 1, "b": 3}, nil},
	{'S', "c", 6, -1, map[string]int{"a": 1, "b": 3, "c": 6}, nil},
	{'R', "", 0, 1, map[string]int{"a": 1, "b": 3, "c": 6}, ErrSavepointNotFound},
}

// SelfCheck verifies the four invariants. Nil iff everything holds.
func (k *KV) SelfCheck() error {
	for _, f := range []func() error{checkTwelve, CheckErrors, CheckNaive} {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

func checkTwelve() error {
	c, _ := New(100)
	for i, o := range twelve {
		var err error
		switch o.kind {
		case 'S':
			err = c.Set(o.key, o.val)
		case 'P':
			if id := c.Savepoint(); id != o.id {
				return fmt.Errorf("step %d: id=%d want %d", i+1, id, o.id)
			}
		case 'R':
			err = c.RollbackTo(o.id)
		case 'X':
			err = c.Release(o.id)
		}
		if !errors.Is(err, o.wantErr) || !eqMap(dump(c), o.want) {
			return fmt.Errorf("step %d: err=%v store=%v want=%v", i+1, err, dump(c), o.want)
		}
	}
	return nil
}

// CheckErrors covers all four distinct sentinels and the no-trace guarantee.
func CheckErrors() error {
	if _, e := New(0); !errors.Is(e, ErrInvalidLimit) {
		return fmt.Errorf("invalid limit: %v", e)
	}
	d, _ := New(2)
	before := dump(d)
	if e := d.Set("", 1); !errors.Is(e, ErrEmptyKey) || !eqMap(dump(d), before) {
		return fmt.Errorf("empty key: %v", e)
	}
	_ = d.Set("a", 1)
	_ = d.Set("b", 2)
	want := map[string]int{"a": 1, "b": 2} // nUndo now at the limit of 2
	if e := d.Set("c", 3); !errors.Is(e, ErrLogFull) || !eqMap(dump(d), want) {
		return fmt.Errorf("log full: %v", e)
	}
	sp := d.Savepoint()
	if e := d.RollbackTo(sp); e != nil || !eqMap(dump(d), want) { // consume sp
		return fmt.Errorf("consume: %v", e)
	}
	for _, f := range []func(int) error{d.RollbackTo, d.Release} {
		if e := f(sp); !errors.Is(e, ErrSavepointNotFound) || !eqMap(dump(d), want) {
			return fmt.Errorf("dead savepoint: %v", e)
		}
	}
	return nil
}

func cp(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// CheckNaive compares randomized sequences against an independent naive model.
func CheckNaive() error {
	for seed := int64(0); seed < 40; seed++ {
		r := rand.New(rand.NewSource(seed))
		g, _ := New(1 << 20)
		nd := map[string]int{}
		var ids []int
		var snaps []map[string]int
		next := 0
		drop := func(i int) { ids, snaps = ids[:i], snaps[:i] }
		for n := 0; n < 200; n++ {
			switch r.Intn(10) {
			case 0, 1, 2, 3:
				kk, vv := testKeys[r.Intn(len(testKeys))], r.Intn(20)
				if e := g.Set(kk, vv); e != nil {
					return e
				}
				nd[kk] = vv
			case 4, 5:
				if id := g.Savepoint(); id != next {
					return fmt.Errorf("id drift %d!=%d", id, next)
				}
				next++
				ids, snaps = append(ids, next-1), append(snaps, cp(nd))
			case 6, 7:
				if len(ids) > 0 {
					i := r.Intn(len(ids))
					if e := g.RollbackTo(ids[i]); e != nil {
						return e
					}
					nd = cp(snaps[i])
					drop(i)
				}
			default: // release: target and deeper savepoints die, data unchanged
				if len(ids) > 0 {
					i := r.Intn(len(ids))
					if e := g.Release(ids[i]); e != nil {
						return e
					}
					drop(i)
				}
			}
			if !eqMap(dump(g), nd) {
				return fmt.Errorf("naive mismatch seed=%d n=%d g=%v naive=%v", seed, n, dump(g), nd)
			}
		}
	}
	return nil
}
