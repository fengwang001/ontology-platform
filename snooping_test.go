package snooping

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
)

const (
	g1 = uint32(0xE0000100)
	g2 = uint32(0xE0000101)
	g3 = uint32(0xE0000102)
	ll = uint32(0xE00000AA)
)

// step 是表驱动测试中的一步操作；wantPorts/wantEv 为 nil 时不检查返回值。
type step struct {
	op        string // "report" | "leave" | "query" | "drain" | "forward"
	port      int
	group     uint32
	srcIP     uint32
	now       int64
	wantPorts []int
	wantEv    []QueryEvent
	wantErr   error
}

func gev(t int64) QueryEvent { return QueryEvent{Time: t, Kind: General} }

func sev(t int64, g uint32, p int) QueryEvent {
	return QueryEvent{Time: t, Kind: Specific, Group: g, Port: p}
}

func mustNew(t *testing.T, p int, own uint32, qi, qri int64, rb int, lmqi int64, fl []bool, flood bool, gmax, lp int) *Switch {
	t.Helper()
	s, err := New(p, own, qi, qri, rb, lmqi, fl, flood, gmax, lp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// base 是题目示例配置：P=4, ownIP=5, QI=125, QRI=10, Rb=2, LMQI=1，
// 故 GMI=260、OQPI=255。
func base(t *testing.T) *Switch {
	t.Helper()
	return mustNew(t, 4, 5, 125, 10, 2, 1, make([]bool, 4), false, 16, 16)
}

func exec(s *Switch, st step) ([]int, []QueryEvent, error) {
	switch st.op {
	case "report":
		p, err := s.Report(st.port, st.group, st.now)
		return p, nil, err
	case "leave":
		return nil, nil, s.Leave(st.port, st.group, st.now)
	case "query":
		return nil, nil, s.Query(st.port, st.srcIP, st.now)
	case "drain":
		ev, err := s.Drain(st.now)
		return nil, ev, err
	case "forward":
		p, err := s.Forward(st.group, st.port, st.now)
		return p, nil, err
	}
	return nil, nil, fmt.Errorf("unknown op %q", st.op)
}

func runSteps(t *testing.T, s *Switch, steps []step) {
	t.Helper()
	for i, st := range steps {
		gotPorts, gotEv, err := exec(s, st)
		if !errors.Is(err, st.wantErr) {
			t.Fatalf("step %d %+v: err=%v, want %v", i, st, err, st.wantErr)
		}
		if st.wantPorts != nil && !slices.Equal(gotPorts, st.wantPorts) {
			t.Fatalf("step %d %+v: ports=%v, want %v", i, st, gotPorts, st.wantPorts)
		}
		if st.wantEv != nil && !slices.Equal(gotEv, st.wantEv) {
			t.Fatalf("step %d %+v: events=%v, want %v", i, st, gotEv, st.wantEv)
		}
	}
}

func TestScenarios(t *testing.T) {
	cases := []struct {
		name  string
		make  func(t *testing.T) *Switch
		steps []step
	}{
		{
			// 题目例一：t=0 Report(1,g) exp=260；t=100 Report(2,g) exp=360；
			// t=200 Leave(1,g)：exp=min(260,202)=202，特定组查询定于 200、201。
			name: "example-membership",
			make: base,
			steps: []step{
				{op: "report", port: 1, group: g1, now: 0, wantPorts: []int{}},
				{op: "report", port: 2, group: g1, now: 100, wantPorts: []int{}},
				{op: "leave", port: 1, group: g1, now: 200},
				{op: "drain", now: 201, wantEv: []QueryEvent{gev(0), gev(125), sev(200, g1, 1), sev(201, g1, 1)}},
				{op: "forward", group: g1, port: 3, now: 201, wantPorts: []int{1, 2}},
				{op: "forward", group: g1, port: 3, now: 202, wantPorts: []int{2}},
			},
		},
		{
			// t=201 Report(1,g)：201 的特定组查询照发（先发后取消），exp=461。
			name: "report-cancels-pending-but-same-tick-fires",
			make: base,
			steps: []step{
				{op: "report", port: 1, group: g1, now: 0, wantPorts: []int{}},
				{op: "report", port: 2, group: g1, now: 100, wantPorts: []int{}},
				{op: "leave", port: 1, group: g1, now: 200},
				{op: "report", port: 1, group: g1, now: 201, wantPorts: []int{}},
				{op: "forward", group: g1, port: 3, now: 359, wantPorts: []int{1, 2}},
				{op: "forward", group: g1, port: 3, now: 360, wantPorts: []int{1}},
				{op: "forward", group: g1, port: 3, now: 460, wantPorts: []int{1}},
				{op: "forward", group: g1, port: 3, now: 461, wantPorts: []int{}},
				{op: "drain", now: 461, wantEv: []QueryEvent{gev(0), gev(125), sev(200, g1, 1), sev(201, g1, 1), gev(250), gev(375)}},
			},
		},
		{
			// Leave 只降不升：t=259 Leave 时 min(260,261)=260，
			// 只有 259 的查询发出（260 的随到期取消）。
			name: "leave-never-extends-expiry",
			make: base,
			steps: []step{
				{op: "report", port: 1, group: g1, now: 0, wantPorts: []int{}},
				{op: "report", port: 2, group: g1, now: 100, wantPorts: []int{}},
				{op: "leave", port: 1, group: g1, now: 259},
				{op: "forward", group: g1, port: 3, now: 259, wantPorts: []int{1, 2}},
				{op: "forward", group: g1, port: 3, now: 260, wantPorts: []int{2}},
				{op: "drain", now: 300, wantEv: []QueryEvent{gev(0), gev(125), gev(250), sev(259, g1, 1)}},
			},
		},
		{
			// 题目例二：选举。t=300 Query(4,3) 让位 oq=555；t=400 Query(4,9)
			// 只刷新路由器端口；t=555 恢复并相位重置。
			name: "election-example",
			make: base,
			steps: []step{
				{op: "report", port: 1, group: g1, now: 0, wantPorts: []int{}},
				{op: "report", port: 2, group: g1, now: 100, wantPorts: []int{}},
				{op: "query", port: 4, srcIP: 3, now: 300},
				{op: "query", port: 4, srcIP: 9, now: 400},
				{op: "report", port: 1, group: g1, now: 600, wantPorts: []int{4}},
				{op: "report", port: 1, group: g1, now: 655, wantPorts: []int{}},
				{op: "drain", now: 700, wantEv: []QueryEvent{gev(0), gev(125), gev(250), gev(555), gev(680)}},
			},
		},
		{
			// 成员恰到期：exp=260，259 仍在、260 已到期。
			name: "exact-expiry",
			make: base,
			steps: []step{
				{op: "report", port: 1, group: g1, now: 0, wantPorts: []int{}},
				{op: "forward", group: g1, port: 2, now: 259, wantPorts: []int{1}},
				{op: "forward", group: g1, port: 2, now: 260, wantPorts: []int{}},
			},
		},
		{
			// 重复 Leave 不重排：第二次 Leave 时已有未发完的特定组查询，
			// exp 不变、不新增查询。
			name: "repeated-leave-no-reschedule",
			make: base,
			steps: []step{
				{op: "report", port: 1, group: g1, now: 0, wantPorts: []int{}},
				{op: "leave", port: 1, group: g1, now: 200},
				{op: "leave", port: 1, group: g1, now: 201},
				{op: "forward", group: g1, port: 2, now: 202, wantPorts: []int{}},
				{op: "drain", now: 300, wantEv: []QueryEvent{gev(0), gev(125), sev(200, g1, 1), sev(201, g1, 1), gev(250)}},
			},
		},
		{
			// fastLeave 端口立即移除，不安排任何特定组查询。
			name: "fast-leave",
			make: func(t *testing.T) *Switch {
				return mustNew(t, 4, 5, 125, 10, 2, 1, []bool{true, false, false, false}, false, 16, 16)
			},
			steps: []step{
				{op: "report", port: 1, group: g1, now: 0, wantPorts: []int{}},
				{op: "leave", port: 1, group: g1, now: 200},
				{op: "forward", group: g1, port: 2, now: 200, wantPorts: []int{}},
				{op: "drain", now: 300, wantEv: []QueryEvent{gev(0), gev(125), gev(250)}},
			},
		},
		{
			// 非查询器期间的 Leave 被接受但无任何效果。
			name: "non-querier-leave-noop",
			make: base,
			steps: []step{
				{op: "report", port: 1, group: g1, now: 200, wantPorts: []int{}},
				{op: "report", port: 2, group: g1, now: 250, wantPorts: []int{}},
				{op: "query", port: 4, srcIP: 3, now: 300},
				{op: "leave", port: 1, group: g1, now: 350},
				{op: "forward", group: g1, port: 3, now: 400, wantPorts: []int{1, 2, 4}},
				{op: "drain", now: 600, wantEv: []QueryEvent{gev(0), gev(125), gev(250), gev(555)}},
			},
		},
		{
			// 较大 srcIP 只刷新路由器端口，不刷新 oq。
			name: "larger-srcip-refreshes-router-only",
			make: base,
			steps: []step{
				{op: "query", port: 4, srcIP: 3, now: 300},
				{op: "query", port: 4, srcIP: 9, now: 400},
				{op: "drain", now: 556, wantEv: []QueryEvent{gev(0), gev(125), gev(250), gev(555)}},
				{op: "report", port: 1, group: g1, now: 600, wantPorts: []int{4}},
				{op: "report", port: 2, group: g1, now: 655, wantPorts: []int{}},
			},
		},
		{
			// 恰在 oq 的 Query 视为本机已先恢复并发出一条通用查询，再行让位。
			name: "query-at-oq-recovers-then-yields",
			make: base,
			steps: []step{
				{op: "query", port: 4, srcIP: 3, now: 300},
				{op: "query", port: 4, srcIP: 3, now: 555},
				{op: "drain", now: 940, wantEv: []QueryEvent{gev(0), gev(125), gev(250), gev(555), gev(810), gev(935)}},
			},
		},
		{
			// 让位取消尚未到时刻的特定组查询，同刻查询照发。
			name: "yield-cancels-specific-queries",
			make: base,
			steps: []step{
				{op: "report", port: 1, group: g1, now: 0, wantPorts: []int{}},
				{op: "leave", port: 1, group: g1, now: 200},
				{op: "query", port: 4, srcIP: 3, now: 200},
				{op: "drain", now: 500, wantEv: []QueryEvent{gev(0), gev(125), sev(200, g1, 1), gev(455)}},
			},
		},
		{
			// 端口超限先于组超限，已到期者不计入（GMI=210）。
			name: "limits-port-before-group-expired-not-counted",
			make: func(t *testing.T) *Switch {
				return mustNew(t, 2, 5, 100, 10, 2, 1, nil, false, 1, 1)
			},
			steps: []step{
				{op: "report", port: 1, group: g1, now: 0, wantPorts: []int{}},
				{op: "report", port: 1, group: g2, now: 0, wantErr: ErrPortLimit},
				{op: "report", port: 2, group: g2, now: 0, wantErr: ErrGroupLimit},
				{op: "report", port: 2, group: g2, now: 209, wantErr: ErrGroupLimit},
				{op: "report", port: 2, group: g2, now: 210, wantPorts: []int{}},
				{op: "report", port: 1, group: g2, now: 210, wantPorts: []int{}},
				{op: "report", port: 1, group: g3, now: 210, wantErr: ErrPortLimit},
			},
		},
		{
			// 未知组：floodUnknown 为真时泛洪。
			name: "unknown-group-flood",
			make: func(t *testing.T) *Switch {
				return mustNew(t, 4, 5, 125, 10, 2, 1, nil, true, 16, 16)
			},
			steps: []step{
				{op: "forward", group: g1, port: 1, now: 0, wantPorts: []int{2, 3, 4}},
				{op: "query", port: 2, srcIP: 9, now: 0},
				{op: "forward", group: g1, port: 1, now: 0, wantPorts: []int{2, 3, 4}},
				{op: "forward", group: g1, port: 2, now: 0, wantPorts: []int{1, 3, 4}},
			},
		},
		{
			// 未知组：floodUnknown 为假时仅路由器端口。
			name: "unknown-group-no-flood",
			make: base,
			steps: []step{
				{op: "forward", group: g1, port: 1, now: 0, wantPorts: []int{}},
				{op: "query", port: 2, srcIP: 9, now: 0},
				{op: "forward", group: g1, port: 1, now: 0, wantPorts: []int{2}},
				{op: "forward", group: g1, port: 2, now: 0, wantPorts: []int{}},
			},
		},
		{
			// 本地链路组：Report/Leave 报本地链路组，Forward 泛洪。
			name: "link-local",
			make: base,
			steps: []step{
				{op: "report", port: 1, group: ll, now: 0, wantErr: ErrLinkLocal},
				{op: "leave", port: 1, group: ll, now: 0, wantErr: ErrLinkLocal},
				{op: "forward", group: ll, port: 1, now: 0, wantPorts: []int{2, 3, 4}},
				{op: "forward", group: ll, port: 4, now: 0, wantPorts: []int{1, 2, 3}},
			},
		},
		{
			// 拒绝次序：参数非法 > 时钟回退 > 本地链路组 > 非成员；
			// 被拒绝的操作不改任何状态与时钟。
			name: "rejection-order",
			make: base,
			steps: []step{
				{op: "report", port: 0, group: g1, now: 0, wantErr: ErrParam},
				{op: "report", port: 1, group: 0xF0000001, now: 0, wantErr: ErrParam},
				{op: "report", port: 1, group: g1, now: -1, wantErr: ErrParam},
				{op: "report", port: 1, group: g1, now: 100, wantPorts: []int{}},
				{op: "report", port: 0, group: g1, now: 50, wantErr: ErrParam},
				{op: "report", port: 1, group: ll, now: 50, wantErr: ErrClock},
				{op: "report", port: 1, group: g2, now: 50, wantErr: ErrClock},
				{op: "report", port: 1, group: g2, now: 100, wantPorts: []int{}},
				{op: "leave", port: 1, group: ll, now: 150, wantErr: ErrLinkLocal},
				{op: "leave", port: 1, group: g3, now: 150, wantErr: ErrNotMember},
				{op: "query", port: 1, srcIP: 0, now: 160, wantErr: ErrParam},
				{op: "query", port: 1, srcIP: 5, now: 160, wantErr: ErrParam},
				{op: "forward", group: g1, port: 0, now: 160, wantErr: ErrParam},
				{op: "drain", now: 50, wantErr: ErrClock},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSteps(t, tc.make(t), tc.steps)
		})
	}
}

func TestNewRejectsInvalidParams(t *testing.T) {
	cases := []struct {
		name string
		call func() error
	}{
		{"ok", func() error { _, e := New(4, 5, 125, 10, 2, 1, make([]bool, 4), false, 16, 16); return e }},
		{"nil-fastLeave", func() error { _, e := New(4, 5, 125, 10, 2, 1, nil, false, 16, 16); return e }},
		{"P=0", func() error { _, e := New(0, 5, 125, 10, 2, 1, nil, false, 16, 16); return e }},
		{"P=257", func() error { _, e := New(257, 5, 125, 10, 2, 1, nil, false, 16, 16); return e }},
		{"ownIP=0", func() error { _, e := New(4, 0, 125, 10, 2, 1, nil, false, 16, 16); return e }},
		{"QI=0", func() error { _, e := New(4, 5, 0, 0, 2, 1, nil, false, 16, 16); return e }},
		{"QRI=QI", func() error { _, e := New(4, 5, 125, 125, 2, 1, nil, false, 16, 16); return e }},
		{"LMQI=0", func() error { _, e := New(4, 5, 125, 10, 2, 0, nil, false, 16, 16); return e }},
		{"LMQI=1e6+1", func() error { _, e := New(4, 5, 125, 10, 2, 1_000_001, nil, false, 16, 16); return e }},
		{"Rb=0", func() error { _, e := New(4, 5, 125, 10, 0, 1, nil, false, 16, 16); return e }},
		{"Rb=8", func() error { _, e := New(4, 5, 125, 10, 8, 1, nil, false, 16, 16); return e }},
		{"fastLeave长度不符", func() error { _, e := New(4, 5, 125, 10, 2, 1, make([]bool, 3), false, 16, 16); return e }},
		{"Gmax=0", func() error { _, e := New(4, 5, 125, 10, 2, 1, nil, false, 0, 16); return e }},
		{"Lp=0", func() error { _, e := New(4, 5, 125, 10, 2, 1, nil, false, 16, 0); return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if tc.name == "ok" || tc.name == "nil-fastLeave" {
				if err != nil {
					t.Fatalf("err=%v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrParam) {
				t.Fatalf("err=%v, want %v", err, ErrParam)
			}
		})
	}
}

// 非导出计数器 touched 证明：一次 Forward 触碰的成员记录数不超过该组的
// 成员记录数加路由器端口数加 1，与组总数无关（10 与 10000 个组两档对照）。
func TestForwardTouchedIndependentOfGroupCount(t *testing.T) {
	for _, groups := range []int{10, 10000} {
		t.Run(fmt.Sprintf("groups=%d", groups), func(t *testing.T) {
			s := mustNew(t, 4, 5, 125, 10, 2, 1, nil, false, 10000, 10000)
			for i := 0; i < groups; i++ {
				if _, err := s.Report(1, g1+uint32(i), 0); err != nil {
					t.Fatalf("Report %d: %v", i, err)
				}
			}
			if err := s.Query(2, 9, 0); err != nil {
				t.Fatalf("Query: %v", err)
			}
			out, err := s.Forward(g1, 3, 0)
			if err != nil {
				t.Fatalf("Forward: %v", err)
			}
			if !slices.Equal(out, []int{1, 2}) {
				t.Fatalf("Forward=%v, want [1 2]", out)
			}
			// 判定依据：1 条成员记录 + 1 个路由器端口 + 1 次查表。
			if s.touched != 3 {
				t.Fatalf("touched=%d, want 3（与组总数 %d 无关）", s.touched, groups)
			}
		})
	}
}

func TestConcurrentOpsSerialize(t *testing.T) {
	s := mustNew(t, 8, 5, 10, 5, 2, 1, nil, true, 64, 8)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				now := int64(i)
				_, _ = s.Report(w+1, g1+uint32(i%4), now)
				_ = s.Leave(w+1, g1+uint32(i%4), now)
				_ = s.Query((w+2)%8+1, uint32(100+w), now)
				_, _ = s.Forward(g1, w+1, now)
				_, _ = s.Drain(now)
			}
		}(w)
	}
	wg.Wait()
}

func TestReplayDeterminism(t *testing.T) {
	script := []step{
		{op: "report", port: 1, group: g1, now: 0},
		{op: "report", port: 2, group: g2, now: 3},
		{op: "leave", port: 1, group: g1, now: 5},
		{op: "query", port: 3, srcIP: 2, now: 7},
		{op: "report", port: 1, group: g1, now: 9},
		{op: "forward", group: g1, port: 4, now: 10},
		{op: "drain", now: 12},
		{op: "query", port: 4, srcIP: 9, now: 13},
		{op: "leave", port: 2, group: g2, now: 14},
		{op: "drain", now: 400},
		{op: "forward", group: g2, port: 1, now: 400},
	}
	run := func() []string {
		s := mustNew(t, 4, 5, 10, 4, 2, 1, []bool{true, false, false, true}, true, 4, 2)
		out := make([]string, 0, len(script))
		for _, st := range script {
			p, ev, err := exec(s, st)
			out = append(out, fmt.Sprintf("ports=%v events=%v err=%v", p, ev, err))
		}
		return out
	}
	first, second := run(), run()
	if !slices.Equal(first, second) {
		t.Fatalf("重放结果不一致:\n%v\nvs\n%v", first, second)
	}
}
