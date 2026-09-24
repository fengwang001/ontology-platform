package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/api"
)

// 不变量1：随机操作序列后 Get 与朴素批量模型逐键一致。
func TestGetMatchesBatchModel(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			s := api.New(1 << 30)
			r := rand.New(rand.NewSource(seed))
			model := map[string]int{}
			switched := false
			for i := 0; i < 500; i++ {
				k := fmt.Sprintf("k%d", r.Intn(40))
				switch r.Intn(10) {
				case 0:
					if !switched {
						_ = s.BeginMigration()
						_ = s.Backfill()
					}
				case 1:
					if !switched && s.Switch() == nil {
						switched = true
						for k, v := range model {
							model[k] = 2 * v
						}
					}
				default:
					v := r.Intn(100)
					if s.Put(k, v) != nil {
						t.Fatalf("Put(%s,%d) rejected", k, v)
					}
					if switched {
						model[k] = 2 * v
					} else {
						model[k] = v
					}
				}
			}
			for i := 0; i < 50; i++ {
				k := fmt.Sprintf("k%d", i)
				if got := s.Get(k); got != model[k] {
					t.Fatalf("Get(%q)=%d, want %d", k, got, model[k])
				}
			}
		})
	}
}

// 不变量2：DualWrite 成功 Put 后 v2[k]==2*v1[k]；故障下两表同时不变。
func TestDualWriteConsistency(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(fmt.Sprintf("fault=%v", fault), func(t *testing.T) {
			s := api.New(100)
			_ = s.Put("a", 3)
			_ = s.BeginMigration()
			if fault {
				s.InjectDualWriteFault()
				if err := s.Put("b", 4); !errors.Is(err, api.ErrDualWriteFault) {
					t.Fatalf("fault put err=%v", err)
				}
			} else if err := s.Put("b", 4); err != nil {
				t.Fatal(err)
			}
			_ = s.Backfill()
			if err := s.Switch(); err != nil {
				t.Fatal(err)
			}
			wantA, wantB := 6, 8
			if fault {
				wantB = 0 // v1、v2 同时不变
			}
			if s.Get("a") != wantA || s.Get("b") != wantB {
				t.Fatalf("Get(a)=%d Get(b)=%d, want %d/%d", s.Get("a"), s.Get("b"), wantA, wantB)
			}
		})
	}
}

// 不变量4：被拒操作不改变任何状态，且哨兵错误可判定、互不相同。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name string
		op   func(*api.Store) error
		want error
	}{
		{"switch in Normal", (*api.Store).Switch, api.ErrBadPhase},
		{"backfill in Normal", (*api.Store).Backfill, api.ErrBadPhase},
		{"empty key", func(s *api.Store) error { return s.Put("", 1) }, api.ErrEmptyKey},
		{"negative value", func(s *api.Store) error { return s.Put("x", -1) }, api.ErrBadValue},
		{"2v overflow", func(s *api.Store) error { return s.Put("x", 6) }, api.ErrBadValue},
		{"dual write fault", func(s *api.Store) error {
			s.InjectDualWriteFault()
			_ = s.BeginMigration()
			return s.Put("x", 1)
		}, api.ErrDualWriteFault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := api.New(10)
			_ = s.Put("keep", 2)
			if err := tc.op(s); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
			if s.Get("keep") != 2 || s.Get("x") != 0 {
				t.Fatal("rejected op changed state")
			}
			if tc.want == api.ErrDualWriteFault {
				_ = s.Switch() // 故障持续；Switch 后只写 v2，恢复正常
				if err := s.Put("keep", 4); err != nil || s.Get("keep") != 8 {
					t.Fatal("store unusable after rejection")
				}
			} else if err := s.Put("keep", 4); err != nil || s.Get("keep") != 4 {
				t.Fatal("store unusable after rejection")
			}
		})
	}
	sents := []error{api.ErrBadPhase, api.ErrEmptyKey, api.ErrBadValue, api.ErrDualWriteFault}
	for i, a := range sents {
		for _, b := range sents[i+1:] {
			if errors.Is(a, b) {
				t.Fatalf("sentinels %v and %v not distinct", a, b)
			}
		}
	}
}

// SelfCheck 必须核验四条不变量并全部通过。
func TestSelfCheck(t *testing.T) {
	if err := api.New(100).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
