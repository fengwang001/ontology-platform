package activator

import (
	"errors"
	"testing"

	"ontology/config"
)

type tStep struct {
	kind    string // push / ready / serving / state
	ver     int64
	in      config.PushInput
	name    string
	now     int64
	wantErr error
	wantV   int64
	wantOK  bool
	hasOK   bool
}

func serving(name string, now int64, v int64) tStep {
	return tStep{kind: "serving", name: name, now: now, hasOK: true, wantOK: true, wantV: v}
}

func noServing(name string, now int64) tStep {
	return tStep{kind: "serving", name: name, now: now, hasOK: true, wantOK: false}
}

func stateExists(name string, now, serving int64) tStep {
	return tStep{kind: "state", name: name, now: now, hasOK: true, wantOK: true, wantV: serving}
}

func stateGone(name string, now int64) tStep {
	return tStep{kind: "state", name: name, now: now, hasOK: true, wantOK: false}
}

// TestTableDriven 合并覆盖校验次序、超时边界、取代重置、删除与 NACK 不落盘。
func TestTableDriven(t *testing.T) {
	cases := []struct {
		name  string
		w     int64
		steps []tStep
	}{
		{name: "invalid-warmup-window", w: 0},
		{
			name: "non-consecutive-version-then-ok",
			w:    10,
			steps: []tStep{
				{kind: "push", ver: 2, in: pushC("c"), now: 0, wantErr: config.ErrBadVersion},
				{kind: "push", ver: 1, in: pushC("c"), now: 0},
				stateExists("c", 1, 0),
			},
		},
		{
			name: "dangling-before-in-use",
			w:    10,
			steps: []tStep{
				{kind: "push", ver: 1, in: withRoute(pushC("c1"), "r1", "c1"), now: 0},
				{kind: "ready", name: "c1", now: 0},
				{kind: "push", ver: 2, in: config.PushInput{
					Routes:         []config.Route{{Name: "r1", Clusters: []string{"cX"}}},
					DeleteClusters: []string{"c1"},
				}, now: 1, wantErr: config.ErrDangling},
				{kind: "push", ver: 2, in: config.PushInput{
					DeleteClusters: []string{"c1"},
				}, now: 2, wantErr: config.ErrInUse},
			},
		},
		{
			name: "same-resource-update-and-delete",
			w:    10,
			steps: []tStep{
				{kind: "push", ver: 1, in: config.PushInput{
					Clusters:       []config.Cluster{{Name: "c1"}},
					DeleteClusters: []string{"c1"},
				}, now: 0, wantErr: config.ErrInvalidArgument},
				{kind: "push", ver: 1, in: config.PushInput{
					Routes:       []config.Route{{Name: "r1", Clusters: []string{"c1"}}},
					DeleteRoutes: []string{"r1"},
				}, now: 0, wantErr: config.ErrInvalidArgument},
			},
		},
		{
			name: "param-checks",
			w:    10,
			steps: []tStep{
				{kind: "push", ver: 1, in: pushC(""), now: 0, wantErr: config.ErrInvalidArgument},
				{kind: "push", ver: 1, in: pushC("c", "c"), now: 0, wantErr: config.ErrInvalidArgument},
				{kind: "push", ver: 1, in: withRoute(pushC("c"), "r"), now: 0, wantErr: config.ErrInvalidArgument},
				{kind: "push", ver: 1, in: withRoute(pushC("c"), "r", "c", "c"), now: 0, wantErr: config.ErrInvalidArgument},
				{kind: "push", ver: 1, in: withRoute(pushC("c"), "", "c"), now: 0, wantErr: config.ErrInvalidArgument},
				{kind: "push", ver: 1, in: config.PushInput{
					Clusters: []config.Cluster{{Name: "c1"}, {Name: "c2"}, {Name: "c3"}, {Name: "c4"},
						{Name: "c5"}, {Name: "c6"}, {Name: "c7"}, {Name: "c8"}},
					Routes: []config.Route{{Name: "r", Clusters: []string{
						"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}}},
				}, now: 0}, // 恰 8 个互异引用合法
			},
		},
		{
			name: "invalid-time-and-clock-backwards",
			w:    10,
			steps: []tStep{
				{kind: "push", ver: 1, in: pushC("c"), now: -1, wantErr: config.ErrInvalidTime},
				{kind: "push", ver: 1, in: pushC("c"), now: config.MaxTime + 1, wantErr: config.ErrInvalidTime},
				{kind: "push", ver: 1, in: pushC("c"), now: 5},
				{kind: "serving", name: "r", now: 4, wantErr: config.ErrClockBackwards},
				{kind: "ready", name: "c", now: 4, wantErr: config.ErrClockBackwards},
				{kind: "state", name: "c", now: 4, wantErr: config.ErrClockBackwards},
			},
		},
		{
			name: "deadline-exact-fails-one-before-ready",
			w:    10,
			steps: []tStep{
				{kind: "push", ver: 1, in: pushC("c1"), now: 0},
				{kind: "ready", name: "c1", now: 9}, // 差 1 仍可 Ready
				{kind: "push", ver: 2, in: pushC("c2"), now: 9},
				stateGone("c2", 19), // 9+10<=19 恰失败，新集群被移除
			},
		},
		{
			name: "repeat-push-replaces-pending-resets-warming",
			w:    10,
			steps: []tStep{
				{kind: "push", ver: 1, in: withRoute(pushC("c1"), "r1", "c1"), now: 0},
				{kind: "ready", name: "c1", now: 0},
				{kind: "push", ver: 2, in: withRoute(pushC("c1"), "r1", "c1"), now: 8},
				{kind: "ready", name: "c1", now: 17}, // 旧 since=0 的计时被重置，17 距 8 仅 9ms
				serving("r1", 17, 2),
			},
		},
		{
			name: "delete-route-immediate-then-cluster-deletable",
			w:    10,
			steps: []tStep{
				{kind: "push", ver: 1, in: withRoute(pushC("c1"), "r1", "c1"), now: 0},
				{kind: "ready", name: "c1", now: 0},
				serving("r1", 0, 1),
				{kind: "push", ver: 2, in: config.PushInput{DeleteRoutes: []string{"r1"}}, now: 1},
				noServing("r1", 1),
				{kind: "push", ver: 3, in: config.PushInput{DeleteClusters: []string{"c1"}}, now: 2},
				stateGone("c1", 2),
			},
		},
		{
			name: "delete-missing-names-noop",
			w:    10,
			steps: []tStep{
				{kind: "push", ver: 1, in: config.PushInput{
					DeleteClusters: []string{"x"}, DeleteRoutes: []string{"y"},
				}, now: 0},
			},
		},
		{
			name: "ready-not-warming-nack-no-persist",
			w:    10,
			steps: []tStep{
				{kind: "push", ver: 1, in: pushC("c1"), now: 0},
				{kind: "ready", name: "", now: 0, wantErr: config.ErrInvalidArgument},
				{kind: "ready", name: "missing", now: 0, wantErr: config.ErrNotWarming},
				{kind: "ready", name: "c1", now: 11, wantErr: config.ErrNotWarming},
				stateGone("c1", 11),
				// 被拒操作未推进序号，ver=2 仍可接受。
				{kind: "push", ver: 2, in: pushC("c1"), now: 11},
			},
		},
		{
			name: "simultaneous-timeup-order-and-cascade",
			w:    10,
			steps: []tStep{
				// a 为新增（无在役），b 为更新（旧在役 v1），同刻 since=0。
				{kind: "push", ver: 1, in: withRoute(pushC("b"), "rb", "b"), now: 0},
				{kind: "ready", name: "b", now: 0},
				serving("rb", 0, 1),
				{kind: "push", ver: 2, in: withRoute(pushC("b", "a"), "rb", "b"), now: 0},
				{kind: "push", ver: 3, in: withRoute(pushC("a"), "ra", "a"), now: 1},
				// now=11：a(1+10<=11) 移除、ra 丢弃；b(0+10<=11) 回退旧在役，rb 按普通条件激活 v2。
				stateGone("a", 11),
				serving("rb", 11, 2),
				noServing("ra", 11),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(tc.w)
			if a == nil {
				if len(tc.steps) != 0 {
					t.Fatalf("New(%d)=nil but steps defined", tc.w)
				}
				return
			}
			for i, s := range tc.steps {
				t.Logf("case=%s step#%d kind=%s ver=%d name=%q now=%d in=%+v",
					tc.name, i, s.kind, s.ver, s.name, s.now, s.in)
				var err error
				switch s.kind {
				case "push":
					err = a.Push(s.ver, s.in, s.now)
					t.Logf("  -> push err=%v（判定依据：%s）", err, reason(err))
				case "ready":
					err = a.Ready(s.name, s.now)
					t.Logf("  -> ready err=%v（判定依据：%s）", err, reason(err))
				case "serving":
					v, okk, e := a.Serving(s.name, s.now)
					err = e
					t.Logf("  -> serving v=%d ok=%v err=%v（判定依据：%s）", v, okk, e, reason(e))
					if err == nil && s.hasOK && (okk != s.wantOK || okk && v != s.wantV) {
						t.Fatalf("step#%d serving: got v=%d ok=%v want v=%d ok=%v", i, v, okk, s.wantV, s.wantOK)
					}
				case "state":
					st, exists, e := a.State(s.name, s.now)
					err = e
					t.Logf("  -> state %+v exists=%v err=%v（判定依据：%s）", st, exists, e, reason(e))
					if err == nil && s.hasOK && (exists != s.wantOK || exists && st.ServingVer != s.wantV) {
						t.Fatalf("step#%d state: got serving=%d exists=%v want v=%d exists=%v",
							i, st.ServingVer, exists, s.wantV, s.wantOK)
					}
				default:
					t.Fatalf("unknown kind %q", s.kind)
				}
				if !errors.Is(err, s.wantErr) {
					t.Fatalf("step#%d: err=%v want %v", i, err, s.wantErr)
				}
			}
		})
	}
}

func reason(err error) string {
	switch {
	case err == nil:
		return "接受/已结算"
	case errors.Is(err, config.ErrInvalidArgument):
		return "参数非法"
	case errors.Is(err, config.ErrInvalidTime):
		return "时间非法"
	case errors.Is(err, config.ErrClockBackwards):
		return "时钟回退"
	case errors.Is(err, config.ErrBadVersion):
		return "序号不符"
	case errors.Is(err, config.ErrDangling):
		return "悬空引用"
	case errors.Is(err, config.ErrInUse):
		return "集群在用"
	case errors.Is(err, config.ErrNotWarming):
		return "未在预热"
	default:
		return err.Error()
	}
}
