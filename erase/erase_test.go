package erase

import (
	"errors"
	"reflect"
	"testing"

	"ontology/refs"
	"ontology/store"
)

func newExec() *Executor {
	return NewExecutor(store.New(), refs.New())
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func checkReport(t *testing.T, got Report, want Report) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("报告不符\n got: %+v\nwant: %+v", got, want)
	}
}

func rep(deleted, detached, anonymized []int64, unlinked []refs.Edge) Report {
	if deleted == nil {
		deleted = []int64{}
	}
	if detached == nil {
		detached = []int64{}
	}
	if anonymized == nil {
		anonymized = []int64{}
	}
	if unlinked == nil {
		unlinked = []refs.Edge{}
	}
	return Report{Deleted: deleted, Detached: detached, Anonymized: anonymized, Unlinked: unlinked}
}

// buildExample 搭建题目示例图：记录 1..7，边与保全与题面一致。
func buildExample(t *testing.T) *Executor {
	t.Helper()
	ex := newExec()
	puts := []struct {
		id     int64
		owners []string
	}{
		{1, []string{"s"}}, {2, []string{"s", "t"}}, {3, nil}, {4, []string{"t"}},
		{5, []string{"s"}}, {6, []string{"t"}}, {7, []string{"s"}},
	}
	for _, p := range puts {
		must(t, ex.Put(p.id, p.owners, 0))
	}
	must(t, ex.AddRef(3, 1, refs.Cascade, 0))
	must(t, ex.AddRef(4, 1, refs.Cascade, 0))
	must(t, ex.AddRef(5, 1, refs.Cascade, 0))
	must(t, ex.AddRef(6, 3, refs.Restrict, 0))
	must(t, ex.AddRef(7, 2, refs.SetNull, 0))
	must(t, ex.Hold(5, 100, 0))
	return ex
}

func TestExampleRestrictedThenFixed(t *testing.T) {
	ex := buildExample(t)

	_, err := ex.Erase("s", 50)
	var re *RestrictedError
	if !errors.Is(err, ErrRestricted) || !errors.As(err, &re) {
		t.Fatalf("应报 ErrRestricted, got %v", err)
	}
	if re.Record != 3 || re.Referrer != 6 {
		t.Fatalf("ErrRestricted 应为 (3,6), got (%d,%d)", re.Record, re.Referrer)
	}

	// 被阻止后零变化：时钟未推进（now=10 仍被接受），墓碑未写入（重报同一错误）。
	must(t, ex.Put(8, nil, 10))
	if _, err := ex.Erase("s", 50); !errors.Is(err, ErrRestricted) {
		t.Fatalf("墓碑不应写入，应再次报 ErrRestricted, got %v", err)
	}
	// 把 6->3 改为 SetNull 后擦除成功，报告与题面一致。
	must(t, ex.RemoveRef(6, 3, 50))
	must(t, ex.AddRef(6, 3, refs.SetNull, 50))
	got, err := ex.Erase("s", 50)
	must(t, err)
	checkReport(t, got, rep(
		[]int64{1, 3, 7},
		[]int64{2},
		[]int64{5},
		[]refs.Edge{{Child: 4, Parent: 1}, {Child: 5, Parent: 1}, {Child: 6, Parent: 3}},
	))
	if ex.visited > 8 {
		t.Fatalf("visited = %d, 应不超过 4+(3+1+0)=8", ex.visited)
	}
	// 擦除后：s 已在墓碑，无任何记录归属 s。
	if _, err := ex.Erase("s", 60); !errors.Is(err, ErrAlreadyErased) {
		t.Fatalf("重复擦除 = %v, want ErrAlreadyErased", err)
	}
	if err := ex.Put(9, []string{"s"}, 60); !errors.Is(err, ErrErased) {
		t.Fatalf("墓碑主体 Put = %v, want ErrErased", err)
	}
	// 已删除记录不可再被引用。
	if err := ex.AddRef(2, 1, refs.Cascade, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("指向已删记录 = %v, want ErrNotFound", err)
	}
}

func TestExampleHoldExpiresAtBoundary(t *testing.T) {
	ex := buildExample(t)
	// 保全恰在 now=100 失效：5 进入删除集，匿名化为空。
	must(t, ex.RemoveRef(6, 3, 0))
	must(t, ex.AddRef(6, 3, refs.SetNull, 0))
	got, err := ex.Erase("s", 100)
	must(t, err)
	checkReport(t, got, rep(
		[]int64{1, 3, 5, 7},
		[]int64{2},
		nil,
		[]refs.Edge{{Child: 4, Parent: 1}, {Child: 6, Parent: 3}},
	))
}

func TestBehaviors(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"共有记录只摘除归属", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s", "t"}, 0))
			got, err := ex.Erase("s", 0)
			must(t, err)
			checkReport(t, got, rep(nil, []int64{1}, nil, nil))
			// 记录保留且仍归属 t。
			plan, err := ex.Plan("t", 1)
			must(t, err)
			checkReport(t, plan, rep([]int64{1}, nil, nil, nil))
		}},
		{"Cascade 子记录归属他人时只断开", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			must(t, ex.Put(2, []string{"t"}, 0))
			must(t, ex.AddRef(2, 1, refs.Cascade, 0))
			got, err := ex.Erase("s", 0)
			must(t, err)
			checkReport(t, got, rep([]int64{1}, nil, nil, []refs.Edge{{Child: 2, Parent: 1}}))
			plan, err := ex.Plan("t", 1)
			must(t, err)
			checkReport(t, plan, rep([]int64{2}, nil, nil, nil))
		}},
		{"无主子记录级联", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			must(t, ex.Put(2, nil, 0))
			must(t, ex.Put(3, nil, 0))
			must(t, ex.AddRef(2, 1, refs.Cascade, 0))
			must(t, ex.AddRef(3, 2, refs.Cascade, 0))
			got, err := ex.Erase("s", 0)
			must(t, err)
			checkReport(t, got, rep([]int64{1, 2, 3}, nil, nil, nil))
		}},
		{"保全小 1 生效", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			must(t, ex.Hold(1, 100, 0))
			got, err := ex.Erase("s", 99)
			must(t, err)
			checkReport(t, got, rep(nil, nil, []int64{1}, nil))
		}},
		{"保全恰等失效", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			must(t, ex.Hold(1, 100, 0))
			got, err := ex.Erase("s", 100)
			must(t, err)
			checkReport(t, got, rep([]int64{1}, nil, nil, nil))
		}},
		{"被保全种子匿名化且其指向 D 的边断开", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			must(t, ex.Put(2, []string{"s"}, 0))
			must(t, ex.AddRef(2, 1, refs.Cascade, 0))
			must(t, ex.Hold(2, 100, 0))
			got, err := ex.Erase("s", 50)
			must(t, err)
			checkReport(t, got, rep([]int64{1}, nil, []int64{2}, []refs.Edge{{Child: 2, Parent: 1}}))
			// 匿名化后保全到期也不会被补删：记录 2 仍在（可被引用）。
			must(t, ex.Put(3, []string{"u"}, 150))
			must(t, ex.AddRef(3, 2, refs.SetNull, 150))
		}},
		{"Restrict 引用者自身在 D 内时不阻止", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			must(t, ex.Put(2, nil, 0))
			must(t, ex.Put(3, nil, 0))
			must(t, ex.AddRef(2, 1, refs.Cascade, 0))
			must(t, ex.AddRef(3, 1, refs.Cascade, 0))
			must(t, ex.AddRef(2, 3, refs.Restrict, 0))
			got, err := ex.Erase("s", 0)
			must(t, err)
			checkReport(t, got, rep([]int64{1, 2, 3}, nil, nil, nil))
		}},
		{"Cascade 环", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			must(t, ex.Put(2, nil, 0))
			must(t, ex.Put(3, nil, 0))
			must(t, ex.AddRef(2, 1, refs.Cascade, 0))
			must(t, ex.AddRef(3, 2, refs.Cascade, 0))
			must(t, ex.AddRef(2, 3, refs.Cascade, 0))
			got, err := ex.Erase("s", 0)
			must(t, err)
			checkReport(t, got, rep([]int64{1, 2, 3}, nil, nil, nil))
			if ex.visited > 4 {
				t.Fatalf("visited = %d, 应不超过 1+(1+1+1)=4", ex.visited)
			}
		}},
		{"被阻止时零变化", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			must(t, ex.Put(2, []string{"t"}, 0))
			must(t, ex.AddRef(2, 1, refs.Restrict, 0))
			before, err := ex.Plan("t", 5)
			must(t, err)
			if _, err := ex.Erase("s", 50); !errors.Is(err, ErrRestricted) {
				t.Fatalf("应被阻止, got %v", err)
			}
			after, err := ex.Plan("t", 5)
			must(t, err)
			checkReport(t, after, before)
			// 时钟未推进：now=40 仍被接受。
			must(t, ex.Put(3, nil, 40))
			// 墓碑未写入：重试仍报 ErrRestricted 而非 ErrAlreadyErased。
			if _, err := ex.Erase("s", 50); !errors.Is(err, ErrRestricted) {
				t.Fatalf("重试 = %v, want ErrRestricted", err)
			}
		}},
		{"Plan 与 Erase 一致", func(t *testing.T) {
			ex := buildExample(t)
			must(t, ex.RemoveRef(6, 3, 0))
			must(t, ex.AddRef(6, 3, refs.SetNull, 0))
			planned, err := ex.Plan("s", 50)
			must(t, err)
			visitedPlan := ex.visited
			// Plan 不改状态：再次 Plan 结果相同，Erase 结果也相同。
			again, err := ex.Plan("s", 50)
			must(t, err)
			checkReport(t, again, planned)
			done, err := ex.Erase("s", 50)
			must(t, err)
			checkReport(t, done, planned)
			if ex.visited != visitedPlan {
				t.Fatalf("Plan 与 Erase 的 visited 不一致: %d vs %d", visitedPlan, ex.visited)
			}
		}},
		{"Plan 遇时钟回报错", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 100))
			if _, err := ex.Plan("s", 50); !errors.Is(err, ErrClockRegression) {
				t.Fatalf("Plan 时钟回退 = %v, want ErrClockRegression", err)
			}
		}},
		{"空主体擦除成功并写墓碑", func(t *testing.T) {
			ex := newExec()
			got, err := ex.Erase("nobody", 7)
			must(t, err)
			checkReport(t, got, rep(nil, nil, nil, nil))
			if _, err := ex.Erase("nobody", 8); !errors.Is(err, ErrAlreadyErased) {
				t.Fatalf("重复擦除 = %v, want ErrAlreadyErased", err)
			}
		}},
		{"墓碑拦截 Put", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			_, err := ex.Erase("s", 1)
			must(t, err)
			if err := ex.Put(2, []string{"s"}, 2); !errors.Is(err, ErrErased) {
				t.Fatalf("Put 含墓碑主体 = %v, want ErrErased", err)
			}
			if err := ex.Put(2, []string{"s", "u"}, 2); !errors.Is(err, ErrErased) {
				t.Fatalf("Put 部分含墓碑主体 = %v, want ErrErased", err)
			}
			must(t, ex.Put(2, []string{"u"}, 2))
		}},
		{"Hold 重复覆盖", func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			must(t, ex.Hold(1, 200, 0))
			must(t, ex.Hold(1, 50, 10)) // 覆盖为更短的保全
			got, err := ex.Erase("s", 60)
			must(t, err)
			checkReport(t, got, rep([]int64{1}, nil, nil, nil))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func TestRejectionOrder(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, ex *Executor)
		op    func(ex *Executor) error
		want  error
	}{
		{"Put 参数非法先于时钟回退", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 100))
		}, func(ex *Executor) error {
			return ex.Put(0, nil, 50)
		}, ErrInvalidArgument},
		{"Put 时钟回退先于已存在", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 100))
		}, func(ex *Executor) error {
			return ex.Put(1, nil, 50)
		}, ErrClockRegression},
		{"Put 已存在先于 ErrErased", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, []string{"s", "t"}, 0))
			_, err := ex.Erase("s", 10)
			must(t, err)
		}, func(ex *Executor) error {
			return ex.Put(1, []string{"s"}, 20)
		}, ErrExists},
		{"Put 墓碑主体", func(t *testing.T, ex *Executor) {
			_, err := ex.Erase("s", 0)
			must(t, err)
		}, func(ex *Executor) error {
			return ex.Put(2, []string{"s"}, 20)
		}, ErrErased},
		{"AddRef 参数非法先于时钟回退", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 100))
		}, func(ex *Executor) error {
			return ex.AddRef(1, 1, refs.Cascade, 50)
		}, ErrInvalidArgument},
		{"AddRef 时钟回退先于记录不存在", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 100))
		}, func(ex *Executor) error {
			return ex.AddRef(1, 2, refs.Cascade, 50)
		}, ErrClockRegression},
		{"AddRef 记录不存在先于已存在", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 0))
			must(t, ex.Put(2, nil, 0))
			must(t, ex.AddRef(1, 2, refs.Cascade, 0))
		}, func(ex *Executor) error {
			return ex.AddRef(1, 3, refs.Cascade, 0)
		}, ErrNotFound},
		{"AddRef 已存在先于出边超限", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 0))
			for p := int64(2); p <= 9; p++ {
				must(t, ex.Put(p, nil, 0))
				must(t, ex.AddRef(1, p, refs.SetNull, 0))
			}
		}, func(ex *Executor) error {
			return ex.AddRef(1, 2, refs.Restrict, 0)
		}, ErrExists},
		{"AddRef 出边超限", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 0))
			for p := int64(2); p <= 10; p++ {
				must(t, ex.Put(p, nil, 0))
			}
			for p := int64(2); p <= 9; p++ {
				must(t, ex.AddRef(1, p, refs.SetNull, 0))
			}
		}, func(ex *Executor) error {
			return ex.AddRef(1, 10, refs.SetNull, 0)
		}, ErrTooManyOutgoing},
		{"RemoveRef 参数非法先于时钟回退", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 100))
		}, func(ex *Executor) error {
			return ex.RemoveRef(1, 1, 50)
		}, ErrInvalidArgument},
		{"RemoveRef 记录不存在", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 0))
		}, func(ex *Executor) error {
			return ex.RemoveRef(1, 2, 0)
		}, ErrNotFound},
		{"RemoveRef 边不存在", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 0))
			must(t, ex.Put(2, nil, 0))
		}, func(ex *Executor) error {
			return ex.RemoveRef(1, 2, 0)
		}, ErrNotFound},
		{"Hold 参数非法先于时钟回退", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 100))
		}, func(ex *Executor) error {
			return ex.Hold(1, 40, 50)
		}, ErrInvalidArgument},
		{"Hold 时钟回退先于记录不存在", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 100))
		}, func(ex *Executor) error {
			return ex.Hold(2, 60, 50)
		}, ErrClockRegression},
		{"Hold 记录不存在", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 0))
		}, func(ex *Executor) error {
			return ex.Hold(2, 60, 0)
		}, ErrNotFound},
		{"Erase 参数非法先于时钟回退", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, nil, 100))
		}, func(ex *Executor) error {
			_, err := ex.Erase("", 50)
			return err
		}, ErrInvalidArgument},
		{"Erase 时钟回退先于 ErrAlreadyErased", func(t *testing.T, ex *Executor) {
			_, err := ex.Erase("s", 100)
			must(t, err)
		}, func(ex *Executor) error {
			_, err := ex.Erase("s", 50)
			return err
		}, ErrClockRegression},
		{"Erase ErrAlreadyErased 先于 ErrRestricted", func(t *testing.T, ex *Executor) {
			must(t, ex.Put(1, []string{"s"}, 0))
			_, err := ex.Erase("s", 10)
			must(t, err)
			// 墓碑写入后再布置一个 Restrict 结构；重擦应报 ErrAlreadyErased 而非去计算它。
			must(t, ex.Put(2, []string{"t"}, 15))
			must(t, ex.Put(3, nil, 15))
			must(t, ex.AddRef(3, 2, refs.Restrict, 15))
		}, func(ex *Executor) error {
			_, err := ex.Erase("s", 20)
			return err
		}, ErrAlreadyErased},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex := newExec()
			tc.setup(t, ex)
			if err := tc.op(ex); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}
