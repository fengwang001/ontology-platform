package negotiate

import (
	"reflect"
	"testing"

	"ontology/adapter"
)

type negCase struct {
	name       string
	h          int
	mutate     func(r *adapter.Registry) // 依次应用，必须全部成功
	rng        string
	allowLossy bool
	scopes     []string
	now        int64
	wantPlan   *Plan
	wantKind   Kind
	wantStep   int
}

// base 复现题目示例：H=4，SetAdapter(1,假,真)、SetAdapter(3,真,假)、Sunset(3,100)。
func base(r *adapter.Registry) {
	must(r.SetAdapter(1, false, true))
	must(r.SetAdapter(3, true, false))
	must(r.Sunset(3, 100))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func full(r *adapter.Registry) { // H=4 全链路无有损
	must(r.SetAdapter(1, false, false))
	must(r.SetAdapter(2, false, false))
	must(r.SetAdapter(3, false, false))
}

func TestNegotiate(t *testing.T) {
	tests := []negCase{
		// 题目主示例（H=4，注册表版本号 3，now=50）。
		{name: "head only", h: 4, mutate: base, rng: "4", now: 50,
			wantPlan: &Plan{Version: 4, Steps: []int{}, Warning: -1, RegistryVersion: 3}},
		{name: "strict lossy rejected", h: 4, mutate: base, rng: "3", now: 50,
			wantKind: KindLossy, wantStep: 3},
		{name: "lossy allowed", h: 4, mutate: base, rng: "3", allowLossy: true, now: 50,
			wantPlan: &Plan{Version: 3, Steps: []int{3}, ReqLossySteps: 1, Warning: 50, RegistryVersion: 3}},
		{name: "range reports top reason", h: 4, mutate: base, rng: "1-3", now: 50,
			wantKind: KindLossy, wantStep: 3},
		{name: "range lossy allowed picks top", h: 4, mutate: base, rng: "1-3", allowLossy: true, now: 50,
			wantPlan: &Plan{Version: 3, Steps: []int{3}, ReqLossySteps: 1, Warning: 50, RegistryVersion: 3}},
		{name: "missing step", h: 4, mutate: base, rng: "1-2", now: 50,
			wantKind: KindNoPath, wantStep: 2},
		{name: "future version", h: 4, mutate: base, rng: "5-9", now: 50,
			wantKind: KindFutureVersion},
		{name: "sunset at exact t", h: 4, mutate: base, rng: "3", allowLossy: true, now: 100,
			wantKind: KindSunset},
		{name: "sunset propagates down chain", h: 4, mutate: base, rng: "1-2", now: 100,
			wantKind: KindSunset},
		{name: "sunset t-1 still up", h: 4, mutate: base, rng: "3", allowLossy: true, now: 99,
			wantPlan: &Plan{Version: 3, Steps: []int{3}, ReqLossySteps: 1, Warning: 1, RegistryVersion: 3}},
		{name: "hi clamped to H", h: 4, mutate: base, rng: "3-9", allowLossy: true, now: 50,
			wantPlan: &Plan{Version: 4, Steps: []int{}, Warning: -1, RegistryVersion: 3}},
		{name: "lo equals H", h: 4, mutate: base, rng: "4-4", now: 50,
			wantPlan: &Plan{Version: 4, Steps: []int{}, Warning: -1, RegistryVersion: 3}},
		{name: "scan high to low first wins", h: 4, mutate: base, rng: "1-4", allowLossy: true, now: 50,
			wantPlan: &Plan{Version: 4, Steps: []int{}, Warning: -1, RegistryVersion: 3}},

		// 预览权限（注册表版本号变为 4）。
		{name: "preview top no scope strict", h: 4, mutate: func(r *adapter.Registry) {
			base(r)
			must(r.Preview(4, "beta"))
		}, rng: "3-4", now: 50, wantKind: KindNoPermission},
		{name: "preview top skipped lossy", h: 4, mutate: func(r *adapter.Registry) {
			base(r)
			must(r.Preview(4, "beta"))
		}, rng: "3-4", allowLossy: true, now: 50,
			wantPlan: &Plan{Version: 3, Steps: []int{3}, ReqLossySteps: 1, Warning: 50, RegistryVersion: 4}},
		{name: "preview with scope served", h: 4, mutate: func(r *adapter.Registry) {
			base(r)
			must(r.Preview(4, "beta"))
		}, rng: "3-4", scopes: []string{"beta"}, now: 50,
			wantPlan: &Plan{Version: 4, Steps: []int{}, Warning: -1, RegistryVersion: 4}},
		{name: "preview candidate skipped lower wins", h: 3, mutate: func(r *adapter.Registry) {
			must(r.SetAdapter(1, false, false))
			must(r.SetAdapter(2, false, false))
			must(r.Preview(2, "beta"))
		}, rng: "1-2", now: 0,
			wantPlan: &Plan{Version: 1, Steps: []int{1, 2}, Warning: -1, RegistryVersion: 3}},

		// 删除与补链（接主示例）。
		{name: "remove adapter breaks path", h: 4, mutate: func(r *adapter.Registry) {
			base(r)
			must(r.RemoveAdapter(1))
		}, rng: "1", allowLossy: true, now: 50,
			wantKind: KindNoPath, wantStep: 1},
		{name: "fill step2 keeps top", h: 4, mutate: func(r *adapter.Registry) {
			base(r)
			must(r.RemoveAdapter(1))
			must(r.SetAdapter(2, false, false))
		}, rng: "1-3", allowLossy: true, now: 50,
			wantPlan: &Plan{Version: 3, Steps: []int{3}, ReqLossySteps: 1, Warning: 50, RegistryVersion: 5}},
		{name: "fill step2 lossy verdict unchanged", h: 4, mutate: func(r *adapter.Registry) {
			base(r)
			must(r.RemoveAdapter(1))
			must(r.SetAdapter(2, false, false))
		}, rng: "1-2", now: 50,
			wantKind: KindLossy, wantStep: 3},
		{name: "fill step2 serves v2", h: 4, mutate: func(r *adapter.Registry) {
			base(r)
			must(r.RemoveAdapter(1))
			must(r.SetAdapter(2, false, false))
		}, rng: "2-2", allowLossy: true, now: 50,
			wantPlan: &Plan{Version: 2, Steps: []int{2, 3}, ReqLossySteps: 1, Warning: 50, RegistryVersion: 5}},

		// H=1 特例。
		{name: "h1 single", h: 1, rng: "1", now: 0,
			wantPlan: &Plan{Version: 1, Steps: []int{}, Warning: -1, RegistryVersion: 0}},
		{name: "h1 clamped", h: 1, rng: "1-1000", now: 0,
			wantPlan: &Plan{Version: 1, Steps: []int{}, Warning: -1, RegistryVersion: 0}},
		{name: "h1 future", h: 1, rng: "2-", now: 0, wantKind: KindFutureVersion},

		// 下线边界与链传递。
		{name: "sunset only on chain versions", h: 2, mutate: func(r *adapter.Registry) {
			must(r.SetAdapter(1, false, false))
			must(r.Sunset(1, 100))
		}, rng: "2", now: 100,
			wantPlan: &Plan{Version: 2, Steps: []int{}, Warning: -1, RegistryVersion: 2}},
		{name: "sunset at t-1 warning", h: 2, mutate: func(r *adapter.Registry) {
			must(r.SetAdapter(1, false, false))
			must(r.Sunset(1, 100))
		}, rng: "1", now: 99,
			wantPlan: &Plan{Version: 1, Steps: []int{1}, Warning: 1, RegistryVersion: 2}},
		{name: "sunset at t down", h: 2, mutate: func(r *adapter.Registry) {
			must(r.SetAdapter(1, false, false))
			must(r.Sunset(1, 100))
		}, rng: "1", now: 100, wantKind: KindSunset},
		{name: "sunset mid chain downs lower", h: 3, mutate: func(r *adapter.Registry) {
			must(r.SetAdapter(1, false, false))
			must(r.SetAdapter(2, false, false))
			must(r.Sunset(2, 100))
		}, rng: "1", now: 100, wantKind: KindSunset},

		// 首个缺失步与首个有损步。
		{name: "first missing step", h: 5, mutate: func(r *adapter.Registry) {
			must(r.SetAdapter(1, false, false))
			must(r.SetAdapter(3, false, false))
			must(r.SetAdapter(4, false, false))
		}, rng: "1", now: 0, wantKind: KindNoPath, wantStep: 2},
		{name: "first lossy step", h: 3, mutate: func(r *adapter.Registry) {
			must(r.SetAdapter(1, true, false))
			must(r.SetAdapter(2, true, false))
		}, rng: "1", now: 0, wantKind: KindLossy, wantStep: 1},

		// 响应有损只计数不阻断。
		{name: "resp lossy counts only", h: 2, mutate: func(r *adapter.Registry) {
			must(r.SetAdapter(1, false, true))
		}, rng: "1", now: 0,
			wantPlan: &Plan{Version: 1, Steps: []int{1}, RespLossySteps: 1, Warning: -1, RegistryVersion: 1}},
		{name: "resp lossy counts both directions", h: 3, mutate: func(r *adapter.Registry) {
			must(r.SetAdapter(1, true, true))
			must(r.SetAdapter(2, false, true))
		}, rng: "1", allowLossy: true, now: 0,
			wantPlan: &Plan{Version: 1, Steps: []int{1, 2}, ReqLossySteps: 1, RespLossySteps: 2, Warning: -1, RegistryVersion: 2}},

		// 告警取链上 min(t-now)。
		{name: "warning is min remaining", h: 4, mutate: func(r *adapter.Registry) {
			full(r)
			must(r.Sunset(1, 300))
			must(r.Sunset(2, 100))
			must(r.Sunset(3, 200))
		}, rng: "1", now: 50,
			wantPlan: &Plan{Version: 1, Steps: []int{1, 2, 3}, Warning: 50, RegistryVersion: 6}},
		{name: "warning over subchain", h: 4, mutate: func(r *adapter.Registry) {
			full(r)
			must(r.Sunset(1, 300))
			must(r.Sunset(2, 100))
			must(r.Sunset(3, 200))
		}, rng: "3", now: 50,
			wantPlan: &Plan{Version: 3, Steps: []int{3}, Warning: 150, RegistryVersion: 6}},

		// 校验顺序：参数非法 > 语法 > 时间 > 未来版本。
		{name: "empty scope beats syntax", h: 4, mutate: base, rng: "x", scopes: []string{""}, now: -1,
			wantKind: KindInvalidArgument},
		{name: "syntax beats time", h: 4, mutate: base, rng: "2-1", now: -1,
			wantKind: KindSyntax},
		{name: "time beats future", h: 4, mutate: base, rng: "9", now: adapter.MaxTime + 1,
			wantKind: KindInvalidTime},
		{name: "negative now", h: 4, mutate: base, rng: "1", now: -1,
			wantKind: KindInvalidTime},
		{name: "max now ok", h: 4, mutate: base, rng: "4", now: adapter.MaxTime,
			wantPlan: &Plan{Version: 4, Steps: []int{}, Warning: -1, RegistryVersion: 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := adapter.New(tt.h)
			if err != nil {
				t.Fatalf("New(%d) err = %v", tt.h, err)
			}
			if tt.mutate != nil {
				tt.mutate(r)
			}
			n := New(r)
			plan, err := n.Negotiate(tt.rng, tt.allowLossy, tt.scopes, tt.now)
			if tt.wantPlan != nil {
				if err != nil {
					t.Fatalf("Negotiate(%q) err = %v, want plan %+v", tt.rng, err, tt.wantPlan)
				}
				if !reflect.DeepEqual(plan, *tt.wantPlan) {
					t.Fatalf("Negotiate(%q) = %+v, want %+v", tt.rng, plan, *tt.wantPlan)
				}
				if len(plan.Steps) != tt.h-plan.Version {
					t.Fatalf("len(Steps) = %d, want H-v = %d", len(plan.Steps), tt.h-plan.Version)
				}
				return
			}
			nerr, ok := err.(*Error)
			if !ok {
				t.Fatalf("Negotiate(%q) err = %v, want *Error kind %d", tt.rng, err, tt.wantKind)
			}
			if nerr.Kind != tt.wantKind {
				t.Fatalf("Negotiate(%q) kind = %d, want %d", tt.rng, nerr.Kind, tt.wantKind)
			}
			if (tt.wantKind == KindNoPath || tt.wantKind == KindLossy) && nerr.Step != tt.wantStep {
				t.Fatalf("Negotiate(%q) step = %d, want %d", tt.rng, nerr.Step, tt.wantStep)
			}
		})
	}
}

func TestNegotiateReadOnly(t *testing.T) {
	r, _ := adapter.New(4)
	base(r)
	n := New(r)
	before := r.Version()
	for i := 0; i < 10; i++ {
		if _, err := n.Negotiate("1-4", true, nil, 50); err != nil {
			t.Fatal(err)
		}
		if _, err := n.Negotiate("bad-range", false, nil, 50); err == nil {
			t.Fatal("want syntax error")
		}
	}
	if got := r.Version(); got != before {
		t.Fatalf("Negotiate advanced registry version: %d -> %d", before, got)
	}
}
