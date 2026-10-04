package push

import (
	"errors"
	"testing"

	"ontology/group"
)

func gv(s string) group.Value { return group.Value{Val: s} }
func unsetV() group.Value     { return group.Value{Unset: true} }

func cfg(kv ...string) Config {
	c := Config{}
	for i := 0; i+1 < len(kv); i += 2 {
		c[group.Name(kv[i])] = kv[i+1]
	}
	return c
}

func pol(kv ...any) group.Policy {
	p := group.Policy{}
	for i := 0; i+1 < len(kv); i += 2 {
		switch v := kv[i+1].(type) {
		case string:
			p[group.Name(kv[i].(string))] = gv(v)
		case group.Value:
			p[group.Name(kv[i].(string))] = v
		}
	}
	return p
}

func must(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", ctx, err)
	}
}

func wantErrIs(t *testing.T, err error, target error, ctx string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: want %v, got %v", ctx, target, err)
	}
}

func wantCfg(t *testing.T, got, want Config, ctx string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: config len: got %v want %v", ctx, got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: key %q: got %q want %q (full got %v)", ctx, k, got[k], v, got)
		}
	}
}

func wantOpErr(t *testing.T, err error, idx int, cause error, ctx string) {
	t.Helper()
	var oe *group.OpError
	if !errors.As(err, &oe) {
		t.Fatalf("%s: want OpError, got %v", ctx, err)
	}
	if oe.Index != idx || !errors.Is(oe, cause) {
		t.Fatalf("%s: want op[%d] %v, got op[%d] %v", ctx, idx, cause, oe.Index, oe.Err)
	}
}

// setupExample 构造题目示例的公共前置：* / g1(10) / g2(10) / g3(20)。
func setupExample(t *testing.T) *Service {
	t.Helper()
	s := New(16)
	must(t, s.SetPolicy(group.Star, pol("interval", "300", "log", "warn")), "star policy")
	must(t, s.AddGroup("g1", 10), "add g1")
	must(t, s.SetPolicy("g1", pol("interval", "30", "mode", "eco")), "g1 policy")
	must(t, s.AddGroup("g2", 10), "add g2")
	must(t, s.SetPolicy("g2", pol("interval", "60")), "g2 policy")
	must(t, s.AddGroup("g3", 20), "add g3")
	must(t, s.SetPolicy("g3", pol("mode", unsetV())), "g3 policy")
	return s
}

// TestSpecExample1 题目第一个端到端示例。
func TestSpecExample1(t *testing.T) {
	s := setupExample(t)

	must(t, s.AddDevice("d"), "add device")
	if pid := s.PendingID("d"); pid != 1 {
		t.Fatalf("AddDevice: want pushId 1, got %d", pid)
	}
	wantCfg(t, s.EffectiveConfig("d"), cfg("interval", "300", "log", "warn"), "E after AddDevice")

	must(t, s.Apply([]group.Op{
		{Kind: group.AddMember, Group: "g1", Dev: "d"},
		{Kind: group.AddMember, Group: "g2", Dev: "d"},
	}), "batch g1+g2")
	if st := s.TaskStatus(1); st != Superseded {
		t.Fatalf("task1: want Superseded, got %s", st)
	}
	if pid := s.PendingID("d"); pid != 2 {
		t.Fatalf("want new pushId 2, got %d", pid)
	}
	wantCfg(t, s.EffectiveConfig("d"), cfg("interval", "30", "mode", "eco", "log", "warn"), "E g1/g2 tie -> g1")

	must(t, s.Ack("d", 2), "ack 2")
	if st := s.TaskStatus(2); st != Acked {
		t.Fatalf("task2: want Acked, got %s", st)
	}

	must(t, s.AddMember("g3", "d"), "add g3")
	wantCfg(t, s.EffectiveConfig("d"), cfg("interval", "30", "log", "warn"), "Unset masks mode, no fallback")
	if pid := s.PendingID("d"); pid != 3 {
		t.Fatalf("want pushId 3, got %d", pid)
	}

	must(t, s.RemoveMember("g3", "d"), "remove g3 unacked")
	if st := s.TaskStatus(3); st != Cancelled {
		t.Fatalf("task3: want Cancelled, got %s", st)
	}
	if pid := s.PendingID("d"); pid != 0 {
		t.Fatalf("E back to A: want no Pd, got pid %d", pid)
	}
	wantErrIs(t, s.Ack("d", 3), ErrStale, "ack cancelled task")

	before := s.NextPushID()
	must(t, s.Apply([]group.Op{
		{Kind: group.AddMember, Group: "g3", Dev: "d"},
		{Kind: group.RemoveMember, Group: "g3", Dev: "d"},
	}), "batch add+remove g3")
	if s.NextPushID() != before {
		t.Fatalf("no-op batch must not consume pushId: %d -> %d", before, s.NextPushID())
	}
	if pid := s.PendingID("d"); pid != 0 {
		t.Fatalf("no-op batch must leave no Pd, got %d", pid)
	}

	must(t, s.SetPriority("g2", 11), "g2 pr 11")
	if pid := s.PendingID("d"); pid != 4 {
		t.Fatalf("want pushId 4, got %d", pid)
	}
	wantCfg(t, s.EffectiveConfig("d"), cfg("interval", "60", "mode", "eco", "log", "warn"), "interval now g2")
	if msg := s.CheckInvariant(); msg != "" {
		t.Fatalf("invariant: %s", msg)
	}
}

// TestSpecExample2 撤销后再漂移不复用编号；Gmax 与批内次序。
func TestSpecExample2(t *testing.T) {
	s := setupExample(t)
	must(t, s.AddDevice("d"), "add")
	must(t, s.Apply([]group.Op{
		{Kind: group.AddMember, Group: "g1", Dev: "d"},
		{Kind: group.AddMember, Group: "g2", Dev: "d"},
	}), "join g1 g2")
	must(t, s.Ack("d", s.PendingID("d")), "ack E")
	must(t, s.AddMember("g3", "d"), "join g3")
	must(t, s.RemoveMember("g3", "d"), "leave g3")
	must(t, s.SetPriority("g2", 11), "g2 -> 11: task 4")
	if pid := s.PendingID("d"); pid != 4 {
		t.Fatalf("want 4, got %d", pid)
	}

	must(t, s.SetPriority("g2", 9), "g2 -> 9: E back to A, cancel 4")
	if st := s.TaskStatus(4); st != Cancelled {
		t.Fatalf("task4: want Cancelled, got %s", st)
	}
	if pid := s.PendingID("d"); pid != 0 {
		t.Fatalf("want no Pd, got %d", pid)
	}

	must(t, s.SetPriority("g2", 11), "g2 -> 11 again: new task 5, not reuse 4")
	if pid := s.PendingID("d"); pid != 5 {
		t.Fatalf("want 5, got %d", pid)
	}
	if st := s.TaskStatus(4); st != Cancelled {
		t.Fatalf("task4 must stay Cancelled, got %s", st)
	}

	// Gmax=2：先加后删在下标 0 超限；次序对调则通过。
	g := New(2)
	must(t, g.AddDevice("d"), "gmax add dev")
	for _, n := range []group.Name{"g1", "g2", "g3"} {
		must(t, g.AddGroup(n, 1), "gmax add "+string(n))
	}
	must(t, g.AddMember("g1", "d"), "join g1")
	must(t, g.AddMember("g2", "d"), "join g2")
	err := g.Apply([]group.Op{
		{Kind: group.AddMember, Group: "g3", Dev: "d"},
		{Kind: group.RemoveMember, Group: "g1", Dev: "d"},
	})
	wantOpErr(t, err, 0, group.ErrTooManyGroups, "add-before-remove")

	must(t, g.Apply([]group.Op{
		{Kind: group.RemoveMember, Group: "g1", Dev: "d"},
		{Kind: group.AddMember, Group: "g3", Dev: "d"},
	}), "remove-before-add must pass")
	if pid := g.PendingID("d"); pid != 0 {
		t.Fatalf("empty policies => E==A, no Pd, got %d", pid)
	}
}

// TestPolicySemantics 表驱动：并列取名小、Unset 不回落、空串语义。
func TestPolicySemantics(t *testing.T) {
	cases := []struct {
		name  string
		setup func(s *Service)
		join  []group.Name
		want  Config
	}{
		{
			name: "tie picks lexicographically smaller group name",
			setup: func(s *Service) {
				must(t, s.AddGroup("g1", 10), "")
				must(t, s.AddGroup("g2", 10), "")
				must(t, s.SetPolicy("g1", pol("k", "aaa")), "")
				must(t, s.SetPolicy("g2", pol("k", "bbb")), "")
			},
			join: []group.Name{"g1", "g2"},
			want: cfg("k", "aaa"),
		},
		{
			name: "byte order g10 smaller than g9",
			setup: func(s *Service) {
				must(t, s.AddGroup("g9", 10), "")
				must(t, s.AddGroup("g10", 10), "")
				must(t, s.SetPolicy("g9", pol("k", "nine")), "")
				must(t, s.SetPolicy("g10", pol("k", "ten")), "")
			},
			join: []group.Name{"g9", "g10"},
			want: cfg("k", "ten"),
		},
		{
			name: "Unset masks without falling back to lower pr",
			setup: func(s *Service) {
				must(t, s.SetPolicy(group.Star, pol("k", "star")), "")
				must(t, s.AddGroup("lo", 5), "")
				must(t, s.SetPolicy("lo", pol("k", "low")), "")
				must(t, s.AddGroup("hi", 9), "")
				must(t, s.SetPolicy("hi", pol("k", unsetV())), "")
			},
			join: []group.Name{"lo", "hi"},
			want: Config{},
		},
		{
			name: "empty string is a real value beating star",
			setup: func(s *Service) {
				must(t, s.SetPolicy(group.Star, pol("k", "star")), "")
				must(t, s.AddGroup("g", 5), "")
				must(t, s.SetPolicy("g", pol("k", "")), "")
			},
			join: []group.Name{"g"},
			want: cfg("k", ""),
		},
		{
			name: "empty string kept while sibling key Unset on another group",
			setup: func(s *Service) {
				must(t, s.SetPolicy(group.Star, pol("a", "", "b", "x")), "")
				must(t, s.AddGroup("g", 5), "")
				must(t, s.SetPolicy("g", pol("b", unsetV())), "")
			},
			join: []group.Name{"g"},
			want: cfg("a", ""),
		},
		{
			name: "higher pr wins regardless of name order",
			setup: func(s *Service) {
				must(t, s.AddGroup("zzz", 20), "")
				must(t, s.AddGroup("aaa", 10), "")
				must(t, s.SetPolicy("zzz", pol("k", "z")), "")
				must(t, s.SetPolicy("aaa", pol("k", "a")), "")
			},
			join: []group.Name{"zzz", "aaa"},
			want: cfg("k", "z"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(16)
			c.setup(s)
			must(t, s.AddDevice("d"), "")
			for _, g := range c.join {
				must(t, s.AddMember(g, "d"), "")
			}
			wantCfg(t, s.EffectiveConfig("d"), c.want, c.name)
		})
	}
}

// TestPdLifecycle Pd 内容不变不换号；Nack 计数；RemoveDevice 撤销；Ack 后清空。
func TestPdLifecycle(t *testing.T) {
	s := New(16)
	must(t, s.AddGroup("g", 10), "")
	must(t, s.SetPolicy("g", pol("k", "v1")), "")
	must(t, s.AddDevice("d"), "")
	must(t, s.AddMember("g", "d"), "")
	pid := s.PendingID("d")

	// 先确认，使 A={k:v1}。
	must(t, s.Ack("d", pid), "ack initial")

	// 触发对齐但 E 不变（加入一个空策略分组）：E==A，无 Pd，不产生编号。
	must(t, s.AddGroup("empty", 10), "empty-policy group")
	must(t, s.AddMember("empty", "d"), "realign with E==A: no task")
	if s.PendingID("d") != 0 {
		t.Fatalf("E==A: no Pd, got %d", s.PendingID("d"))
	}

	// E 变化建新号；再使 E 恰好回到 A：旧号 Cancelled。
	must(t, s.SetPolicy(group.Star, pol("other", "x")), "drift E: new task")
	if s.PendingID("d") != pid+1 {
		t.Fatalf("changed E should create task %d, got %d", pid+1, s.PendingID("d"))
	}
	drifted := s.PendingID("d")
	must(t, s.SetPolicy(group.Star, pol()), "E back to A: cancel")
	if s.PendingID("d") != 0 {
		t.Fatalf("E==A must clear Pd, got %d", s.PendingID("d"))
	}
	if s.TaskStatus(drifted) != Cancelled {
		t.Fatalf("drifted task %d: want Cancelled, got %s", drifted, s.TaskStatus(drifted))
	}
	// Pd 内容与 E 相同则不换号：漂移后加入空策略组触发对齐，编号保持。
	must(t, s.SetPolicy("g", pol("k", "v1", "x", "y")), "drift again")
	pid = s.PendingID("d")
	must(t, s.RemoveMember("empty", "d"), "remove empty group: E unchanged, id kept")
	if s.PendingID("d") != pid {
		t.Fatalf("identical-E realign must keep %d, got %d", pid, s.PendingID("d"))
	}

	must(t, s.Nack("d", pid), "nack current")
	must(t, s.Nack("d", pid), "nack current again")
	wantErrIs(t, s.Nack("d", pid+999), ErrStale, "nack wrong id")
	wantErrIs(t, s.Nack("nodev", pid), ErrStale, "nack missing dev")
	if s.PendingID("d") != pid {
		t.Fatalf("Nack must keep Pd, got %d", s.PendingID("d"))
	}

	must(t, s.Ack("d", pid), "ack")
	if s.PendingID("d") != 0 || s.TaskStatus(pid) != Acked {
		t.Fatalf("after ack: pid=%v status=%s", s.PendingID("d"), s.TaskStatus(pid))
	}
	wantCfg(t, s.AckedConfig("d"), cfg("k", "v1", "x", "y"), "A after ack")
	wantErrIs(t, s.Ack("d", pid), ErrStale, "double ack stale")

	must(t, s.SetPolicy("g", pol("k", "v2")), "drift -> new task")
	old := s.PendingID("d")
	must(t, s.RemoveDevice("d"), "remove device")
	if s.TaskStatus(old) != Cancelled || s.HasDevice("d") {
		t.Fatalf("remove: status=%s has=%v", s.TaskStatus(old), s.HasDevice("d"))
	}
	wantErrIs(t, s.RemoveDevice("d"), group.ErrNotFound, "remove again")
}

// TestPushIDOrder 同一变更内多设备按设备名字节序占号，只有实际建号才占号。
func TestPushIDOrder(t *testing.T) {
	s := New(16)
	// 空星策略下三台设备：空 E == 空 A，均不占号。
	must(t, s.AddDevice("devC"), "devC no task")
	must(t, s.AddDevice("devB"), "devB no task")
	must(t, s.AddDevice("devA"), "devA no task")
	if s.NextPushID() != 0 {
		t.Fatalf("empty E: no id consumed, got next=%d", s.NextPushID())
	}
	// 一次 SetPolicy 波及全部设备，按设备名字节序占 1/2/3。
	must(t, s.SetPolicy(group.Star, pol("k", "v1")), "one batch affects all")
	if s.PendingID("devA") != 1 || s.PendingID("devB") != 2 || s.PendingID("devC") != 3 {
		t.Fatalf("byte-order ids: A=%d B=%d C=%d", s.PendingID("devA"), s.PendingID("devB"), s.PendingID("devC"))
	}
	// 只让 devA、devC 漂移（devB 的 E 保持）：仍按字节序连续占号 4、5。
	must(t, s.Ack("devA", 1), "")
	must(t, s.Ack("devB", 2), "")
	must(t, s.Ack("devC", 3), "")
	must(t, s.AddGroup("gac", 10), "group joined by A and C only")
	must(t, s.Apply([]group.Op{
		{Kind: group.AddMember, Group: "gac", Dev: "devC"},
		{Kind: group.AddMember, Group: "gac", Dev: "devA"},
		{Kind: group.SetPolicy, Group: "gac", Policy: pol("k", "v2")},
	}), "single batch: C joins, A joins, policy set")
	if s.PendingID("devA") != 4 || s.PendingID("devB") != 0 || s.PendingID("devC") != 5 {
		t.Fatalf("ids after selective drift: A=%d B=%d C=%d",
			s.PendingID("devA"), s.PendingID("devB"), s.PendingID("devC"))
	}
	if st := s.TaskStatus(1); st != Acked {
		t.Fatalf("task1 want Acked, got %s", st)
	}
}

func opPolicy33(g group.Name) group.Op {
	p := group.Policy{}
	for i := 0; i < 33; i++ {
		p[group.Name("k"+itoa(i))] = gv("x")
	}
	return group.Op{Kind: group.SetPolicy, Group: g, Policy: p}
}

// TestRejectOrder 拒绝原因优先级与首个失败下标。
func TestRejectOrder(t *testing.T) {
	s := New(1)
	must(t, s.AddGroup("g", 10), "")
	must(t, s.AddGroup("h", 10), "")
	must(t, s.AddDevice("d"), "")
	must(t, s.AddMember("g", "d"), "") // gmax 已满

	type row struct {
		name string
		op   group.Op
		want error
	}
	rows := []row{
		{"empty name device", group.Op{Kind: group.AddDevice, Dev: ""}, group.ErrInvalid},
		{"65 byte name", group.Op{Kind: group.AddGroup, Group: group.Name(string(make([]byte, 65))), PR: 1}, group.ErrInvalid},
		{"star remove", group.Op{Kind: group.RemoveGroup, Group: group.Star}, group.ErrInvalid},
		{"star setpriority", group.Op{Kind: group.SetPriority, Group: group.Star, PR: 1}, group.ErrInvalid},
		{"star addmember", group.Op{Kind: group.AddMember, Group: group.Star, Dev: "d"}, group.ErrInvalid},
		{"star removemember", group.Op{Kind: group.RemoveMember, Group: group.Star, Dev: "d"}, group.ErrInvalid},
		{"pr -1", group.Op{Kind: group.AddGroup, Group: "h", PR: -1}, group.ErrInvalid},
		{"pr 1001", group.Op{Kind: group.SetPriority, Group: "g", PR: 1001}, group.ErrInvalid},
		{"policy 33 keys", opPolicy33("h"), group.ErrInvalid},
		{"remove missing group", group.Op{Kind: group.RemoveGroup, Group: "nope"}, group.ErrNotFound},
		{"setpolicy missing group", group.Op{Kind: group.SetPolicy, Group: "nope", Policy: pol()}, group.ErrNotFound},
		{"member missing group", group.Op{Kind: group.AddMember, Group: "nope", Dev: "d"}, group.ErrNotFound},
		{"member missing dev", group.Op{Kind: group.AddMember, Group: "g", Dev: "nope"}, group.ErrNotFound},
		{"add existing device", group.Op{Kind: group.AddDevice, Dev: "d"}, group.ErrExists},
		{"add existing group", group.Op{Kind: group.AddGroup, Group: "g", PR: 1}, group.ErrExists},
		{"duplicate member beats gmax", group.Op{Kind: group.AddMember, Group: "g", Dev: "d"}, group.ErrExists},
		{"remove nonempty group", group.Op{Kind: group.RemoveGroup, Group: "g"}, group.ErrNotEmpty},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			wantOpErr(t, s.Apply([]group.Op{r.op}), 0, r.want, r.name)
		})
	}

	wantOpErr(t, s.Apply([]group.Op{{Kind: group.AddMember, Group: "h", Dev: "d"}}),
		0, group.ErrTooManyGroups, "gmax exceeded")

	wantOpErr(t, s.Apply(nil), -1, group.ErrInvalid, "empty batch")
	big := make([]group.Op, 257)
	for i := range big {
		big[i] = group.Op{Kind: group.AddDevice, Dev: group.Name("x")}
	}
	wantOpErr(t, s.Apply(big), -1, group.ErrInvalid, "257 ops")

	err := s.Apply([]group.Op{
		{Kind: group.AddGroup, Group: "tmp", PR: 1},
		{Kind: group.RemoveGroup, Group: "missing"},
		{Kind: group.AddDevice, Dev: "d"},
	})
	wantOpErr(t, err, 1, group.ErrNotFound, "first failure index")
}

// TestBatchFailureRollback 批失败零变化：快照、pushId 计数、任务结局全部不变。
func TestBatchFailureRollback(t *testing.T) {
	s := New(16)
	must(t, s.SetPolicy(group.Star, pol("k", "v0")), "")
	must(t, s.AddDevice("d"), "")
	must(t, s.Ack("d", 1), "")
	must(t, s.AddGroup("g", 10), "")

	beforeSnap := s.Store().Snapshot()
	beforeNext := s.NextPushID()
	beforeAck := s.AckedConfig("d")

	err := s.Apply([]group.Op{
		{Kind: group.SetPolicy, Group: "g", Policy: pol("k", "v1")},
		{Kind: group.AddMember, Group: "g", Dev: "d"},
		{Kind: group.AddMember, Group: "ghost", Dev: "d"},
	})
	wantOpErr(t, err, 2, group.ErrNotFound, "batch fails")

	if s.NextPushID() != beforeNext {
		t.Fatalf("pushId counter changed: %d -> %d", beforeNext, s.NextPushID())
	}
	wantCfg(t, s.AckedConfig("d"), beforeAck, "A unchanged")
	if s.PendingID("d") != 0 {
		t.Fatalf("no Pd before and after, got %d", s.PendingID("d"))
	}
	after := s.Store().Snapshot()
	if !snapEqual(beforeSnap, after) {
		t.Fatalf("snapshot changed after rollback")
	}
}

func snapEqual(a, b group.Snapshot) bool {
	if len(a.Devices) != len(b.Devices) || len(a.Groups) != len(b.Groups) {
		return false
	}
	for d, da := range a.Devices {
		db, ok := b.Devices[d]
		if !ok || !namesEqual(da.Groups, db.Groups) {
			return false
		}
	}
	for n, ga := range a.Groups {
		gb, ok := b.Groups[n]
		if !ok {
			return false
		}
		if ga.PR != gb.PR || ga.Builtin != gb.Builtin ||
			!namesEqual(ga.Members, gb.Members) || !policyEqual(ga.Policy, gb.Policy) {
			return false
		}
	}
	return true
}

func namesEqual(a, b []group.Name) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func policyEqual(a, b group.Policy) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestGroupDirectAPI group 包直接调用的回滚与 Gmax 行为。
func TestGroupDirectAPI(t *testing.T) {
	s := group.New()
	if err := s.Apply(nil, 16); err == nil {
		t.Fatalf("empty batch must fail")
	}
	err := s.Apply([]group.Op{
		{Kind: group.AddDevice, Dev: "d"},
		{Kind: group.AddGroup, Group: "g", PR: 10},
		{Kind: group.AddMember, Group: "g", Dev: "d"},
		{Kind: group.AddMember, Group: "missing", Dev: "d"},
	}, 2)
	var oe *group.OpError
	if !errors.As(err, &oe) || oe.Index != 3 {
		t.Fatalf("want op[3], got %v", err)
	}
	snap := s.Snapshot()
	if _, ok := snap.Devices["d"]; ok {
		t.Fatalf("device d should be rolled back")
	}
	if _, ok := snap.Groups["g"]; ok {
		t.Fatalf("group g should be rolled back")
	}
}
