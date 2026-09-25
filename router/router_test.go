package router

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"

	"ontology/part"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// TestScanCountBound：m 档分区各一个 Key，Migrate 扫描数不随 m 增长（白盒直读 scanCount）。
func TestScanCountBound(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		ids := make([]int, m)
		for i := range ids {
			ids[i] = i
		}
		r := New(ids...)
		for i := 0; i < m; i++ {
			must(r.Assign(fmt.Sprintf("k%d", i), i))
		}
		must(r.BeginDrain(0))
		must(r.Migrate(0, 1))
		if r.scanCount > scanBound {
			t.Fatalf("m=%d: scanned %d partitions, want <= constant %d", m, r.scanCount, scanBound)
		}
	}
}

// TestMigrateConsistency：迁移后 from 空且 Removed、to 恰好多出这批、owner 绝不指向 Removed。
func TestMigrateConsistency(t *testing.T) {
	cases := []struct {
		name     string
		assigns  map[string]int
		drain    int
		from, to int
	}{
		{"single key", map[string]int{"a": 0, "b": 1}, 1, 1, 0},
		{"batch", map[string]int{"a": 0, "b": 2, "c": 2, "d": 2}, 2, 2, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := New(0, 1, 2)
			for k, p := range c.assigns {
				must(r.Assign(k, p))
			}
			beforeTo := r.Count(c.to)
			must(r.BeginDrain(c.drain))
			must(r.Migrate(c.from, c.to))
			if r.Count(c.from) != 0 || r.parts[c.from].State() != part.Removed {
				t.Errorf("from not emptied+Removed: count=%d state=%s", r.Count(c.from), r.parts[c.from].State())
			}
			moved := 0
			for k, p := range c.assigns {
				want := p
				if p == c.from {
					want = c.to
					moved++
				}
				if r.owner[k] != want {
					t.Errorf("owner[%s]=%d want %d", k, r.owner[k], want)
				}
				if r.parts[r.owner[k]].State() == part.Removed {
					t.Errorf("key %s owned by Removed partition %d", k, r.owner[k])
				}
			}
			if got := r.Count(c.to); got != beforeTo+moved {
				t.Errorf("Count(to)=%d want %d", got, beforeTo+moved)
			}
			if err := r.Migrate(c.from, c.to); !errors.Is(err, ErrBadMigrate) {
				t.Errorf("re-migrate from Removed: got %v want ErrBadMigrate", err)
			}
		})
	}
}

// TestCountMatchesBatchRecompute：随机操作序列下，任意时刻 Count 与
// 「只统计被接受的 Assign + 施加全部已完成 Migrate」的批量重算一致。
func TestCountMatchesBatchRecompute(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 42, 99} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b9))
			r := New(0, 1, 2)
			states := []part.State{part.Active, part.Active, part.Active}
			model := map[string]int{}
			var keys []string
			check := func(op int, wantOK bool, err error) {
				t.Helper()
				if (err == nil) != wantOK {
					t.Fatalf("op %d: err=%v wantOK=%v", op, err, wantOK)
				}
			}
			for op := 0; op < 300; op++ {
				switch rng.IntN(4) {
				case 0:
					k := fmt.Sprintf("k%d", len(keys))
					p := rng.IntN(5) - 1 // -1..3：越界触发分区不存在
					ok := p >= 0 && p < 3 && states[p] == part.Active
					if check(op, ok, r.Assign(k, p)); ok {
						model[k] = p
						keys = append(keys, k)
					}
				case 1:
					p := rng.IntN(3)
					ok := states[p] == part.Active
					if check(op, ok, r.BeginDrain(p)); ok {
						states[p] = part.Draining
					}
				case 2:
					from, to := rng.IntN(5)-1, rng.IntN(5)-1
					ok := from >= 0 && from < 3 && to >= 0 && to < 3 && from != to &&
						states[from] == part.Draining && states[to] == part.Active
					if check(op, ok, r.Migrate(from, to)); ok {
						for k, p := range model {
							if p == from {
								model[k] = to
							}
						}
						states[from] = part.Removed
					}
				case 3:
					k := "ghost"
					if len(keys) > 0 && rng.IntN(2) == 0 {
						k = keys[rng.IntN(len(keys))]
					}
					p, assigned := model[k]
					check(op, assigned && states[p] == part.Active, r.Put(k, "v"))
				}
				want := [3]int{}
				for _, p := range model {
					want[p]++
				}
				for p, w := range want {
					if got := r.Count(p); got != w {
						t.Fatalf("op %d Count(%d)=%d want %d", op, p, got, w)
					}
				}
			}
		})
	}
}
