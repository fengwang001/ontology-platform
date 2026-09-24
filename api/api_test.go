package api_test

import (
	"errors"
	"testing"

	"ontology/api"
	"ontology/rep"
)

type put struct {
	v    string
	ver  int64
	reps []int
}
type scn struct {
	name string
	n    int
	puts []put
}

var scenarios = []scn{
	{"全空", 3, nil},
	{"空副本回填", 3, []put{{"a", 5, []int{0, 1}}}},
	{"并列取字典序", 3, []put{{"b", 7, []int{0, 1}}, {"c", 7, []int{2}}}},
	{"晚到旧写", 3, []put{{"c", 7, []int{0, 1, 2}}, {"d", 4, []int{0}}}},
	{"乱序分散", 4, []put{{"x", 1, []int{0}}, {"y", 2, []int{1, 2}}, {"z", 3, []int{3}}}},
	{"全覆盖无修复", 2, []put{{"a", 1, []int{0, 1}}}},
}

func build(t *testing.T, n int, puts []put) *api.Engine {
	t.Helper()
	eng, _ := api.New(n)
	for _, p := range puts {
		if err := eng.Put("k", p.v, p.ver, p.reps); err != nil {
			t.Fatal(err)
		}
	}
	return eng
}

func TestNaiveConsistency(t *testing.T) { // 不变量1
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			eng := build(t, sc.n, sc.puts)
			w, _, ok := rep.Winner(eng.Snapshot("k")) // 朴素参照
			v, found, _, err := eng.Read("k")
			if err != nil || found != ok {
				t.Fatalf("found=%v ok=%v err=%v", found, ok, err)
			}
			if !ok {
				return
			}
			if v != w.Value {
				t.Fatalf("winner=%q，朴素参照=%q", v, w.Value)
			}
			for i, e := range eng.Snapshot("k") {
				if e != w {
					t.Fatalf("副本%d=%+v 未收敛到 %+v", i, e, w)
				}
			}
		})
	}
}

func TestRepairConverges(t *testing.T) { // 不变量2
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			eng := build(t, sc.n, sc.puts)
			v1, _, _, _ := eng.Read("k")
			v2, _, r2, _ := eng.Read("k")
			if v1 != v2 || r2 != 0 {
				t.Fatalf("二次 Read %q->%q repaired=%d", v1, v2, r2)
			}
		})
	}
}

func TestRepairNeverDowngrades(t *testing.T) { // 不变量3
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			eng := build(t, sc.n, sc.puts)
			before := eng.Snapshot("k")
			w, _, ok := rep.Winner(before)
			eng.Read("k")
			for i, e := range eng.Snapshot("k") {
				if !before[i].Empty && e.Ver < before[i].Ver {
					t.Fatalf("副本%d 版本 %d->%d 降低", i, before[i].Ver, e.Ver)
				}
				if ok && e.Ver != w.Ver {
					t.Fatalf("副本%d 版本=%d，应为读前最大版本 %d", i, e.Ver, w.Ver)
				}
			}
		})
	}
}

type badOp struct {
	name string
	op   func(*api.Engine) error
	want error
}

func TestRejectedOpsNoSideEffect(t *testing.T) { // 不变量4
	bads := []badOp{
		{"n为0", func(*api.Engine) error { _, err := api.New(0); return err }, api.ErrBadN},
		{"空键", func(e *api.Engine) error { return e.Put("", "x", 1, []int{0}) }, api.ErrEmptyKey},
		{"零版本", func(e *api.Engine) error { return e.Put("k", "x", 0, []int{0}) }, api.ErrBadVer},
		{"负版本", func(e *api.Engine) error { return e.Put("k", "x", -1, []int{0}) }, api.ErrBadVer},
		{"空副本集", func(e *api.Engine) error { return e.Put("k", "x", 1, nil) }, api.ErrBadReps},
		{"越界下标", func(e *api.Engine) error { return e.Put("k", "x", 1, []int{3}) }, api.ErrBadReps},
		{"负下标", func(e *api.Engine) error { return e.Put("k", "x", 1, []int{-1}) }, api.ErrBadReps},
		{"Read空键", func(e *api.Engine) error { _, _, _, err := e.Read(""); return err }, api.ErrEmptyKey},
	}
	for _, b := range bads {
		t.Run(b.name, func(t *testing.T) {
			eng := build(t, 3, []put{{"v", 2, []int{0, 2}}})
			before := eng.Snapshot("k")
			if err := b.op(eng); !errors.Is(err, b.want) {
				t.Fatalf("err=%v，应为 %v", err, b.want)
			}
			for i, e := range eng.Snapshot("k") {
				if e != before[i] {
					t.Fatalf("副本%d 状态被改", i)
				}
			}
			if err := eng.Put("k", "ok", 9, []int{1}); err != nil { // 拒绝后仍可用
				t.Fatal(err)
			}
		})
	}
}

func TestErrorsDistinct(t *testing.T) {
	errs := []error{api.ErrBadN, api.ErrBadVer, api.ErrEmptyKey, api.ErrBadReps}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("第%d与第%d个哨兵错误相同", i, j)
			}
		}
	}
}
