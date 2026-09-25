package api

import (
	"bytes"
	"errors"
	"testing"

	"ontology/hook"
	"ontology/snapshot"
)

func asInt(v any) int {
	if n, ok := v.(int); ok {
		return n
	}
	return 0
}

// endAfterStart 跨字段不变量：end 必须大于 start。
func endAfterStart(s snapshot.Snapshot) (bool, string) {
	start, _ := s.Get("start")
	end, _ := s.Get("end")
	return asInt(end) > asInt(start), "end 必须大于 start"
}

// statusOpen 单字段前置条件：当前 status 为 open 才允许变更（只看旧值）。
func statusOpen(s snapshot.Snapshot) (bool, string) {
	st, _ := s.Get("status")
	return st == "open", "status 必须为 open"
}

func failWith(reason string) hook.Check {
	return func(snapshot.Snapshot) (bool, string) { return false, reason }
}

func boom(snapshot.Snapshot) (bool, string) { panic("boom") }

func hk(name string, ph hook.Phase, c hook.Check) hook.Hook {
	return hook.Hook{Name: name, AppliesTo: "Task", Phase: ph, Check: c}
}

// 遍历 pre/post × 单字段/跨字段 × 聚合/短路/内部错误形态。
func TestValidateForms(t *testing.T) {
	task := Object{Type: "Task", Attrs: map[string]any{"start": 5, "end": 9, "status": "open"}}
	closed := Object{Type: "Task", Attrs: map[string]any{"start": 5, "end": 9, "status": "closed"}}
	postRan := 0
	probe := hook.Hook{Name: "probe", AppliesTo: "Task", Phase: hook.Post,
		Check: func(snapshot.Snapshot) (bool, string) { postRan++; return true, "" }}
	cases := []struct {
		name         string
		obj          Object
		change       map[string]any
		hooks        []hook.Hook
		wantSentinel error
		wantInternal bool
		wantFailures int
		wantPostRan  int
	}{
		{"单字段pre前置满足", task, map[string]any{"level": 2}, []hook.Hook{hk("st", hook.Pre, statusOpen)}, nil, false, 0, 0},
		{"单字段pre前置不满足", closed, map[string]any{"level": 2}, []hook.Hook{hk("st", hook.Pre, statusOpen)}, ErrPreFailed, false, 1, 0},
		{"跨字段post合法", task, map[string]any{"end": 10}, []hook.Hook{hk("xs", hook.Post, endAfterStart)}, nil, false, 0, 0},
		{"跨字段post违规", task, map[string]any{"end": 3}, []hook.Hook{hk("xs", hook.Post, endAfterStart)}, ErrPostFailed, false, 1, 0},
		{"跨字段误挂pre被放过", task, map[string]any{"end": 3}, []hook.Hook{hk("xs", hook.Pre, endAfterStart)}, nil, false, 0, 0},
		{"pre多失败聚合", task, map[string]any{"level": -1},
			[]hook.Hook{hk("a", hook.Pre, failWith("a 失败")), hk("b", hook.Pre, failWith("b 失败"))}, ErrPreFailed, false, 2, 0},
		{"pre失败阻止post", closed, map[string]any{"level": 2}, []hook.Hook{hk("st", hook.Pre, statusOpen), probe}, ErrPreFailed, false, 1, 0},
		{"pre全过才跑post", task, map[string]any{"level": 3}, []hook.Hook{hk("st", hook.Pre, statusOpen), probe}, nil, false, 0, 1},
		{"post多失败聚合", task, map[string]any{"end": 3},
			[]hook.Hook{hk("xs", hook.Post, endAfterStart), hk("c", hook.Post, failWith("c 失败"))}, ErrPostFailed, false, 2, 0},
		{"pre钩子panic", task, map[string]any{"level": 1}, []hook.Hook{hk("boom", hook.Pre, boom)}, ErrPreFailed, true, 1, 0},
		{"post钩子panic", task, map[string]any{"level": 1}, []hook.Hook{hk("boom", hook.Post, boom)}, ErrPostFailed, true, 1, 0},
		{"无钩子直接通过", task, map[string]any{"level": 1}, nil, nil, false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			postRan = 0
			r := hook.NewRegistry()
			for _, h := range tc.hooks {
				r.Register(h)
			}
			err := NewValidator(r).Validate(tc.obj, Change{Set: tc.change})
			if tc.wantSentinel == nil {
				if err != nil {
					t.Fatalf("期望通过, 得到 %v", err)
				}
			} else {
				if !errors.Is(err, tc.wantSentinel) {
					t.Fatalf("期望哨兵 %v, 得到 %v", tc.wantSentinel, err)
				}
				var ve *ValidationError
				if !errors.As(err, &ve) || len(ve.Failures) != tc.wantFailures {
					t.Fatalf("期望聚合 %d 条失败, 得到 %v", tc.wantFailures, err)
				}
			}
			if got := errors.Is(err, ErrHookInternal); got != tc.wantInternal {
				t.Fatalf("ErrHookInternal = %v, 期望 %v", got, tc.wantInternal)
			}
			if postRan != tc.wantPostRan {
				t.Fatalf("post 运行 %d 次, 期望 %d", postRan, tc.wantPostRan)
			}
		})
	}
}

// 参数校验：不进入任何钩子。
func TestInvalidArgument(t *testing.T) {
	ran := 0
	r := hook.NewRegistry()
	r.Register(hook.Hook{Name: "h", AppliesTo: "Task", Phase: hook.Pre,
		Check: func(snapshot.Snapshot) (bool, string) { ran++; return true, "" }})
	v := NewValidator(r)
	for _, tc := range []struct {
		name string
		obj  Object
		ch   Change
	}{
		{"空类型", Object{Attrs: map[string]any{}}, Change{Set: map[string]any{"a": 1}}},
		{"空变更集", Object{Type: "Task"}, Change{}},
		{"nil变更集", Object{Type: "Task"}, Change{Set: nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := v.Validate(tc.obj, tc.ch); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("期望 ErrInvalidArgument, 得到 %v", err)
			}
		})
	}
	if ran != 0 {
		t.Fatalf("参数不合法时钩子被运行 %d 次", ran)
	}
}

// 只读契约：pre 全部看到变更前快照、post 全部看到变更后快照，阶段内字节相同。
func TestFrozenSnapshotVisibility(t *testing.T) {
	obj := Object{Type: "Task", Attrs: map[string]any{"start": 5, "end": 9}}
	var preSnaps, postSnaps [][]byte
	capture := func(dst *[][]byte) hook.Check {
		return func(s snapshot.Snapshot) (bool, string) {
			*dst = append(*dst, s.Bytes())
			return true, ""
		}
	}
	r := hook.NewRegistry()
	for _, n := range []string{"p1", "p2"} {
		r.Register(hook.Hook{Name: n, AppliesTo: "Task", Phase: hook.Pre, Check: capture(&preSnaps)})
	}
	for _, n := range []string{"q1", "q2"} {
		r.Register(hook.Hook{Name: n, AppliesTo: "Task", Phase: hook.Post, Check: capture(&postSnaps)})
	}
	if err := NewValidator(r).Validate(obj, Change{Set: map[string]any{"end": 10}}); err != nil {
		t.Fatal(err)
	}
	wantPre := snapshot.Freeze("Task", map[string]any{"start": 5, "end": 9}).Bytes()
	wantPost := snapshot.Freeze("Task", map[string]any{"start": 5, "end": 10}).Bytes()
	for _, b := range preSnaps {
		if !bytes.Equal(b, wantPre) {
			t.Fatal("pre 钩子看到的不是变更前冻结快照")
		}
	}
	for _, b := range postSnaps {
		if !bytes.Equal(b, wantPost) {
			t.Fatal("post 钩子看到的不是变更后冻结快照")
		}
	}
}

// 确定性：任意注册顺序，聚合结果逐字节相同。
func TestDeterministicAcrossRegistrationOrders(t *testing.T) {
	obj := Object{Type: "Task", Attrs: map[string]any{"level": 1}}
	var first string
	for _, order := range [][]string{{"f1", "ok1", "f2"}, {"f2", "f1", "ok1"}, {"ok1", "f2", "f1"}} {
		r := hook.NewRegistry()
		for _, n := range order {
			name := n
			r.Register(hk(name, hook.Pre, func(snapshot.Snapshot) (bool, string) {
				return name == "ok1", name + " rejected"
			}))
		}
		err := NewValidator(r).Validate(obj, Change{Set: map[string]any{"level": 2}})
		if err == nil {
			t.Fatal("期望聚合失败")
		}
		if first == "" {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("注册顺序 %v 结果 %q, 期望 %q", order, err.Error(), first)
		}
	}
}
