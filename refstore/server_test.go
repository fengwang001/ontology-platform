package refstore

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// newFixture 构建标准提交图：
//
//	c1 -> c2 -> ... -> c10   （主链）
//	c1 -> d1 -> d2           （分叉侧支）
func newFixture(t *testing.T) (*Server, *Graph) {
	t.Helper()
	g := NewGraph()
	prev := ""
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("c%d", i)
		var parents []string
		if prev != "" {
			parents = []string{prev}
		}
		if err := g.Add(id, parents...); err != nil {
			t.Fatalf("建图失败: %v", err)
		}
		prev = id
	}
	if err := g.Add("d1", "c1"); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("d2", "d1"); err != nil {
		t.Fatal(err)
	}
	return NewServer(g), g
}

// mustPush 推送并要求整批应用成功。
func mustPush(t *testing.T, s *Server, user string, ins ...Instruction) BatchResult {
	t.Helper()
	res := s.Push(user, ins)
	if !res.Applied {
		t.Fatalf("预期成功的推送被拒绝: %v", fmtVerdicts(res.Verdicts))
	}
	return res
}

func fmtVerdicts(vs []Verdict) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		if v.OK {
			parts[i] = "通过"
		} else {
			parts[i] = v.Reason.String()
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// expect 断言整批裁决与预期原因序列一致，并打印输入、实际输出与判定依据。
func expect(t *testing.T, input string, res BatchResult, want []Reason, rationale string) {
	t.Helper()
	t.Logf("输入: %s", input)
	t.Logf("实际输出: applied=%v verdicts=%v", res.Applied, fmtVerdicts(res.Verdicts))
	t.Logf("判定依据: %s", rationale)
	if len(res.Verdicts) != len(want) {
		t.Fatalf("裁决数量不符: got %d want %d", len(res.Verdicts), len(want))
	}
	for i, w := range want {
		got := res.Verdicts[i]
		if got.OK != (got.Reason == ReasonNone) {
			t.Errorf("指令 %d: OK 与 Reason 不一致: %+v", i, got)
		}
		if got.Reason != w {
			t.Errorf("指令 %d: got %v want %v", i, got.Reason, w)
		}
	}
}

// TestRefNameValidation 覆盖引用名各类非法形态与命名空间判定。
func TestRefNameValidation(t *testing.T) {
	cases := []struct {
		ref  string
		want Reason
		why  string
	}{
		{"refs/heads/ok", ReasonNone, "合法分支名"},
		{"refs/heads/a/b/c", ReasonNone, "合法多级分支名"},
		{"refs/tags/v1.0", ReasonNone, "合法标签名（分段中间的点允许）"},
		{"", ReasonInvalidArgument, "空名不在任何命名空间"},
		{"heads/x", ReasonInvalidArgument, "缺少 refs/ 前缀，命名空间之外"},
		{"refs/heads", ReasonInvalidArgument, "缺少尾部分隔符，命名空间之外"},
		{"refs/unknown/x", ReasonInvalidArgument, "未知命名空间"},
		{"refs/heads/", ReasonInvalidRefName, "名字为空（以分隔符结尾）"},
		{"refs/heads/a/", ReasonInvalidRefName, "以分隔符结尾"},
		{"refs/heads/a//b", ReasonInvalidRefName, "连续分隔符"},
		{"refs/heads/.hidden", ReasonInvalidRefName, "分段以点开头"},
		{"refs/heads/a/.b", ReasonInvalidRefName, "中间分段以点开头"},
		{"refs/heads/a.lock", ReasonInvalidRefName, "分段以 .lock 结尾"},
		{"refs/heads/a.lock/x", ReasonInvalidRefName, "中间分段以 .lock 结尾"},
		{"refs/heads/a\x01b", ReasonInvalidRefName, "含控制字节 0x01"},
		{"refs/heads/a\x7fb", ReasonInvalidRefName, "含控制字节 0x7f"},
	}
	for _, tc := range cases {
		s, _ := newFixture(t)
		in := Instruction{Ref: tc.ref, New: "c1"}
		res := s.Push("alice", []Instruction{in})
		expect(t, fmt.Sprintf("创建 %+v", in), res, []Reason{tc.want}, tc.why)
	}
}

// TestAncestorPrefixConflict 覆盖祖先分段前缀冲突的各种形态。
func TestAncestorPrefixConflict(t *testing.T) {
	t.Run("创建与既有名字冲突", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/a", New: "c1"})
		in := Instruction{Ref: "refs/heads/a/b", New: "c2"}
		res := s.Push("alice", []Instruction{in})
		expect(t, "既有 refs/heads/a，创建 refs/heads/a/b", res,
			[]Reason{ReasonBatchConflict}, "批内创建名是既有名的后代分段，冲突")
	})
	t.Run("创建与既有名字反向冲突", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/a/b", New: "c1"})
		in := Instruction{Ref: "refs/heads/a", New: "c2"}
		res := s.Push("alice", []Instruction{in})
		expect(t, "既有 refs/heads/a/b，创建 refs/heads/a", res,
			[]Reason{ReasonBatchConflict}, "批内创建名是既有名的祖先分段，冲突")
	})
	t.Run("批内两个创建互相冲突", func(t *testing.T) {
		s, _ := newFixture(t)
		ins := []Instruction{
			{Ref: "refs/heads/x", New: "c1"},
			{Ref: "refs/heads/x/y", New: "c2"},
		}
		res := s.Push("alice", ins)
		expect(t, fmt.Sprintf("同批创建 %+v", ins), res,
			[]Reason{ReasonBatchConflict, ReasonBatchConflict}, "批内两个创建名互为祖先分段前缀")
	})
	t.Run("创建与批内更新指令冲突", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/m", New: "c1"})
		ins := []Instruction{
			{Ref: "refs/heads/m/n", New: "c2"},
			{Ref: "refs/heads/m", Old: "c1", New: "c3"},
		}
		res := s.Push("alice", ins)
		expect(t, fmt.Sprintf("创建与批内更新 %+v", ins), res,
			[]Reason{ReasonBatchConflict, ReasonBatchConflict},
			"创建名与批内其它指令的名字构成祖先分段前缀，两条都标记批内冲突")
	})
	t.Run("跨命名空间不冲突", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/p", New: "c1"})
		in := Instruction{Ref: "refs/tags/p", New: "c1"}
		res := s.Push("alice", []Instruction{in})
		expect(t, "既有 refs/heads/p，创建 refs/tags/p", res,
			[]Reason{ReasonNone}, "命名空间不同，完整名字的分段序列不同，不冲突")
	})
	t.Run("字符串前缀但非分段前缀不冲突", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/ab", New: "c1"})
		in := Instruction{Ref: "refs/heads/abc", New: "c2"}
		res := s.Push("alice", []Instruction{in})
		expect(t, "既有 refs/heads/ab，创建 refs/heads/abc", res,
			[]Reason{ReasonNone}, "ab 不是 abc 的祖先分段（无分隔符边界），不冲突")
	})
}

// TestCrudAndOldValueCombinations 覆盖创建/更新/删除与旧值四种组合。
func TestCrudAndOldValueCombinations(t *testing.T) {
	cases := []struct {
		name   string
		preset *Instruction // 非空则先建立该引用
		in     Instruction
		want   Reason
		why    string
	}{
		{"创建/期望空/实际空", nil,
			Instruction{Ref: "refs/heads/r", New: "c1"}, ReasonNone, "创建成功"},
		{"创建/期望空/实际非空", &Instruction{Ref: "refs/heads/r", New: "c1"},
			Instruction{Ref: "refs/heads/r", New: "c2"}, ReasonOldValueMismatch, "期望不存在但已存在"},
		{"创建/期望非空/实际空", nil,
			Instruction{Ref: "refs/heads/r", Old: "c1", New: "c2"}, ReasonOldValueMismatch, "期望旧值但引用不存在"},
		{"更新/期望匹配", &Instruction{Ref: "refs/heads/r", New: "c1"},
			Instruction{Ref: "refs/heads/r", Old: "c1", New: "c2"}, ReasonNone, "旧值匹配的快进更新"},
		{"更新/期望非空/实际不同", &Instruction{Ref: "refs/heads/r", New: "c1"},
			Instruction{Ref: "refs/heads/r", Old: "c3", New: "c2"}, ReasonOldValueMismatch, "实际旧值与期望不符"},
		{"更新/期望空/实际非空", &Instruction{Ref: "refs/heads/r", New: "c1"},
			Instruction{Ref: "refs/heads/r", New: "c2"}, ReasonOldValueMismatch, "按创建写期望但引用已存在"},
		{"更新/期望非空/实际空", nil,
			Instruction{Ref: "refs/heads/r", Old: "c1", New: "c2"}, ReasonOldValueMismatch, "期望旧值但引用不存在"},
		{"删除/期望匹配", &Instruction{Ref: "refs/heads/r", New: "c1"},
			Instruction{Ref: "refs/heads/r", Old: "c1"}, ReasonNone, "旧值匹配的删除"},
		{"删除/期望空/实际空", nil,
			Instruction{Ref: "refs/heads/r"}, ReasonInvalidArgument, "新旧值都空为参数非法"},
		{"删除/期望空/实际非空", &Instruction{Ref: "refs/heads/r", New: "c1"},
			Instruction{Ref: "refs/heads/r"}, ReasonInvalidArgument,
			"删除指令期望旧值为空即新旧都空，参数非法优先于旧值不匹配"},
		{"删除/期望非空/实际不同", &Instruction{Ref: "refs/heads/r", New: "c1"},
			Instruction{Ref: "refs/heads/r", Old: "c3"}, ReasonOldValueMismatch, "实际旧值与期望不符"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newFixture(t)
			if tc.preset != nil {
				mustPush(t, s, "alice", *tc.preset)
			}
			res := s.Push("alice", []Instruction{tc.in})
			expect(t, fmt.Sprintf("%+v", tc.in), res, []Reason{tc.want}, tc.why)
		})
	}
}

// TestFastForwardMatrix 覆盖快进/非快进与有无强制标志的全部组合。
func TestFastForwardMatrix(t *testing.T) {
	cases := []struct {
		name string
		in   Instruction
		want Reason
		why  string
	}{
		{"快进/无强制", Instruction{Ref: "refs/heads/m", Old: "c2", New: "c4"},
			ReasonNone, "c4 是 c2 的后代，快进"},
		{"快进/带强制", Instruction{Ref: "refs/heads/m", Old: "c2", New: "c4", Force: true},
			ReasonNone, "快进本来就不需要强制标志"},
		{"相等/无强制", Instruction{Ref: "refs/heads/m", Old: "c2", New: "c2"},
			ReasonNone, "新旧值相等视为快进（含相等），且无操作"},
		{"回退/无强制", Instruction{Ref: "refs/heads/m", Old: "c2", New: "c1"},
			ReasonNonFastForward, "c1 不是 c2 的后代，非快进且未带强制标志"},
		{"回退/带强制", Instruction{Ref: "refs/heads/m", Old: "c2", New: "c1", Force: true},
			ReasonNone, "非快进但带强制标志，允许"},
		{"分叉/无强制", Instruction{Ref: "refs/heads/m", Old: "c2", New: "d2"},
			ReasonNonFastForward, "d2 与 c2 分叉，非快进且未带强制标志"},
		{"分叉/带强制", Instruction{Ref: "refs/heads/m", Old: "c2", New: "d2", Force: true},
			ReasonNone, "分叉非快进但带强制标志，允许"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newFixture(t)
			mustPush(t, s, "alice", Instruction{Ref: "refs/heads/m", Old: "", New: "c2"})
			res := s.Push("alice", []Instruction{tc.in})
			expect(t, fmt.Sprintf("当前值 c2，%+v", tc.in), res, []Reason{tc.want}, tc.why)
		})
	}
}

// TestTagSemantics 覆盖标签不可更新与同批删除重建。
func TestTagSemantics(t *testing.T) {
	t.Run("标签不可更新", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/tags/v1", New: "c1"})
		in := Instruction{Ref: "refs/tags/v1", Old: "c1", New: "c2", Force: true}
		res := s.Push("alice", []Instruction{in})
		expect(t, fmt.Sprintf("标签当前 c1，%+v", in), res,
			[]Reason{ReasonTagNoUpdate}, "标签一旦存在永远不允许更新，强制标志也无效")
	})
	t.Run("标签等值无操作", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/tags/v1", New: "c1"})
		in := Instruction{Ref: "refs/tags/v1", Old: "c1", New: "c1"}
		res := s.Push("alice", []Instruction{in})
		expect(t, fmt.Sprintf("标签当前 c1，%+v", in), res,
			[]Reason{ReasonNone}, "新旧值相等不是更新，无操作通过")
	})
	t.Run("删除后分批重建", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/tags/v1", New: "c1"})
		mustPush(t, s, "alice", Instruction{Ref: "refs/tags/v1", Old: "c1"})
		in := Instruction{Ref: "refs/tags/v1", New: "c2"}
		res := s.Push("alice", []Instruction{in})
		expect(t, "删除后下一批重建 refs/tags/v1 -> c2", res,
			[]Reason{ReasonNone}, "标签允许删除后在另一批中重建")
	})
	t.Run("同批删除重建", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/tags/v1", New: "c1"})
		ins := []Instruction{
			{Ref: "refs/tags/v1", Old: "c1"}, // 删除
			{Ref: "refs/tags/v1", New: "c2"}, // 同批重建
		}
		res := s.Push("alice", ins)
		expect(t, fmt.Sprintf("同批删除重建 %+v", ins), res,
			[]Reason{ReasonNotExecuted, ReasonOldValueMismatch},
			"逐条判定时重建指令期望不存在但标签当前存在，旧值不匹配；"+
				"删除指令被连坐，整批拒绝，效果等同禁止同批删除重建")
		if v, ok := s.Get("refs/tags/v1"); !ok || v != "c1" {
			t.Errorf("被拒绝的推送改变了引用: %q %v", v, ok)
		}
	})
	t.Run("同批重复创建同一引用", func(t *testing.T) {
		s, _ := newFixture(t)
		ins := []Instruction{
			{Ref: "refs/tags/v2", New: "c1"},
			{Ref: "refs/tags/v2", New: "c2"},
		}
		res := s.Push("alice", ins)
		expect(t, fmt.Sprintf("同批双创建 %+v", ins), res,
			[]Reason{ReasonBatchConflict, ReasonBatchConflict},
			"同一引用在批内出现两次，两条都标记批内冲突")
	})
}

// TestProtectedRules 覆盖受保护四种限制及其并集。
func TestProtectedRules(t *testing.T) {
	t.Run("禁止创建", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/frozen/x", New: "c1"})
		s.SetRules([]Rule{{Pattern: "refs/heads/frozen/*", NoCreate: true}})
		in := Instruction{Ref: "refs/heads/frozen/y", New: "c1"}
		res := s.Push("alice", []Instruction{in})
		expect(t, fmt.Sprintf("%+v", in), res, []Reason{ReasonProtectedNoCreate},
			"规则禁止创建命中引用")
		up := Instruction{Ref: "refs/heads/frozen/x", Old: "c1", New: "c2"}
		res = s.Push("alice", []Instruction{up})
		expect(t, fmt.Sprintf("%+v", up), res, []Reason{ReasonNone},
			"禁止创建不影响对既有引用的更新")
	})
	t.Run("禁止删除", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/keep", New: "c1"})
		s.SetRules([]Rule{{Pattern: "refs/heads/keep", NoDelete: true}})
		in := Instruction{Ref: "refs/heads/keep", Old: "c1"}
		res := s.Push("alice", []Instruction{in})
		expect(t, fmt.Sprintf("%+v", in), res, []Reason{ReasonProtectedNoDelete},
			"规则禁止删除命中引用")
	})
	t.Run("禁止非快进", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/stable", New: "c2"})
		s.SetRules([]Rule{{Pattern: "refs/heads/stable", NoNonFF: true}})
		in := Instruction{Ref: "refs/heads/stable", Old: "c2", New: "c1", Force: true}
		res := s.Push("alice", []Instruction{in})
		expect(t, fmt.Sprintf("%+v", in), res, []Reason{ReasonProtectedNoNonFF},
			"禁止非快进优先于强制标志")
		ff := Instruction{Ref: "refs/heads/stable", Old: "c2", New: "c3"}
		res = s.Push("alice", []Instruction{ff})
		expect(t, fmt.Sprintf("%+v", ff), res, []Reason{ReasonNone}, "快进不受禁止非快进影响")
	})
	t.Run("仅限名单", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/app", New: "c1"})
		s.SetRules([]Rule{{Pattern: "refs/heads/app", AllowOnly: true, Users: []string{"alice"}}})
		in := Instruction{Ref: "refs/heads/app", Old: "c1", New: "c2"}
		res := s.Push("bob", []Instruction{in})
		expect(t, fmt.Sprintf("bob 推送 %+v", in), res, []Reason{ReasonProtectedAllowlist},
			"bob 不在名单内")
		res = s.Push("alice", []Instruction{in})
		expect(t, fmt.Sprintf("alice 推送 %+v", in), res, []Reason{ReasonNone},
			"alice 在名单内，允许更新")
	})
	t.Run("多规则限制并集", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/release/v1", New: "c2"})
		s.SetRules([]Rule{
			{Pattern: "refs/heads/release/*", NoDelete: true},
			{Pattern: "refs/heads/*/v1", NoNonFF: true},
		})
		del := Instruction{Ref: "refs/heads/release/v1", Old: "c2"}
		res := s.Push("alice", []Instruction{del})
		expect(t, fmt.Sprintf("%+v", del), res, []Reason{ReasonProtectedNoDelete},
			"规则一贡献禁止删除")
		nff := Instruction{Ref: "refs/heads/release/v1", Old: "c2", New: "c1", Force: true}
		res = s.Push("alice", []Instruction{nff})
		expect(t, fmt.Sprintf("%+v", nff), res, []Reason{ReasonProtectedNoNonFF},
			"规则二贡献禁止非快进，并集生效")
	})
	t.Run("名单并集", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/app", New: "c1"})
		s.SetRules([]Rule{
			{Pattern: "refs/heads/app", AllowOnly: true, Users: []string{"alice"}},
			{Pattern: "refs/*/*", AllowOnly: true, Users: []string{"bob"}},
		})
		in := Instruction{Ref: "refs/heads/app", Old: "c1", New: "c2"}
		res := s.Push("bob", []Instruction{in})
		expect(t, "bob 推送更新", res, []Reason{ReasonNone}, "bob 在规则二名单内")
		s2, _ := newFixture(t)
		mustPush(t, s2, "alice", Instruction{Ref: "refs/heads/app", New: "c1"})
		s2.SetRules([]Rule{
			{Pattern: "refs/heads/app", AllowOnly: true, Users: []string{"alice"}},
			{Pattern: "refs/*/*", AllowOnly: true, Users: []string{"bob"}},
		})
		res = s2.Push("carol", []Instruction{in})
		expect(t, "carol 推送更新", res, []Reason{ReasonProtectedAllowlist},
			"carol 不在任一命中规则的名单内")
	})
	t.Run("规则只在裁决时读取", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/keep", New: "c1"})
		s.SetRules([]Rule{{Pattern: "refs/heads/keep", NoDelete: true}})
		in := Instruction{Ref: "refs/heads/keep", Old: "c1"}
		res := s.Push("alice", []Instruction{in})
		expect(t, "有规则时删除", res, []Reason{ReasonProtectedNoDelete}, "规则生效")
		s.SetRules(nil)
		res = s.Push("alice", []Instruction{in})
		expect(t, "清除规则后删除", res, []Reason{ReasonNone}, "规则按批裁决时的快照生效")
	})
}

// TestReasonPriority 覆盖拒绝原因优先次序中每一对相邻原因的优先关系。
func TestReasonPriority(t *testing.T) {
	run := func(name string, setup func(*Server), user string, ins []Instruction, want []Reason, why string) {
		t.Run(name, func(t *testing.T) {
			s, _ := newFixture(t)
			if setup != nil {
				setup(s)
			}
			res := s.Push(user, ins)
			expect(t, fmt.Sprintf("%+v", ins), res, want, why)
		})
	}
	preset := func(ins ...Instruction) func(*Server) {
		return func(s *Server) {
			res := s.Push("alice", ins)
			if !res.Applied {
				panic("预设推送失败")
			}
		}
	}
	rules := func(rs ...Rule) func(*Server) { return func(s *Server) { s.SetRules(rs) } }

	run("参数非法>引用名非法", nil, "alice",
		[]Instruction{{Ref: "refs/heads/a//b"}},
		[]Reason{ReasonInvalidArgument},
		"名字非法且新旧值都空，参数非法在前")
	run("引用名非法>对象不存在", nil, "alice",
		[]Instruction{{Ref: "refs/heads/a//b", New: "missing"}},
		[]Reason{ReasonInvalidRefName},
		"名字非法且新值指向不存在的提交，引用名非法在前")
	run("对象不存在>旧值不匹配",
		preset(Instruction{Ref: "refs/heads/m", New: "c1"}), "alice",
		[]Instruction{{Ref: "refs/heads/m", Old: "c9", New: "missing"}},
		[]Reason{ReasonObjectNotFound},
		"新值对象不存在且期望旧值错误，对象不存在在前")
	run("旧值不匹配>受保护禁止创建",
		rules(Rule{Pattern: "refs/heads/r/*", NoCreate: true}), "alice",
		[]Instruction{{Ref: "refs/heads/r/x", Old: "c1", New: "c2"}},
		[]Reason{ReasonOldValueMismatch},
		"创建指令期望旧值非空（实际不存在）且命中禁止创建，旧值不匹配在前")
	run("受保护禁止创建与禁止删除互斥",
		func(s *Server) {
			mustPush(t, s, "alice", Instruction{Ref: "refs/heads/p/x", New: "c1"})
			s.SetRules([]Rule{{Pattern: "refs/heads/p/*", NoCreate: true, NoDelete: true}})
		}, "alice",
		[]Instruction{
			{Ref: "refs/heads/p/y", New: "c1"},
			{Ref: "refs/heads/p/x", Old: "c1"},
		},
		[]Reason{ReasonProtectedNoCreate, ReasonProtectedNoDelete},
		"创建与删除在同一条指令上不可能同时成立，该对无法同条触发；"+
			"同批各发一条验证两种原因各自生效，相对次序由裁决顺序保证")
	run("受保护禁止删除>受保护仅限名单",
		func(s *Server) {
			mustPush(t, s, "alice", Instruction{Ref: "refs/heads/m", New: "c1"})
			s.SetRules([]Rule{{Pattern: "refs/heads/m", NoDelete: true, AllowOnly: true, Users: []string{"alice"}}})
		}, "bob",
		[]Instruction{{Ref: "refs/heads/m", Old: "c1"}},
		[]Reason{ReasonProtectedNoDelete},
		"bob 删除同时命中禁止删除与仅限名单，禁止删除在前")
	run("受保护仅限名单>标签不可更新",
		func(s *Server) {
			mustPush(t, s, "alice", Instruction{Ref: "refs/tags/v1", New: "c1"})
			s.SetRules([]Rule{{Pattern: "refs/tags/v1", AllowOnly: true, Users: []string{"alice"}}})
		}, "bob",
		[]Instruction{{Ref: "refs/tags/v1", Old: "c1", New: "c2"}},
		[]Reason{ReasonProtectedAllowlist},
		"bob 更新标签同时触发仅限名单与标签不可更新，仅限名单在前")
	run("标签不可更新>受保护禁止非快进",
		func(s *Server) {
			mustPush(t, s, "alice", Instruction{Ref: "refs/tags/v1", New: "c2"})
			s.SetRules([]Rule{{Pattern: "refs/tags/*", NoNonFF: true}})
		}, "alice",
		[]Instruction{{Ref: "refs/tags/v1", Old: "c2", New: "c1"}},
		[]Reason{ReasonTagNoUpdate},
		"标签更新到新值非旧值后代且命中禁止非快进，标签不可更新在前")
	run("受保护禁止非快进>未带强制的非快进",
		func(s *Server) {
			mustPush(t, s, "alice", Instruction{Ref: "refs/heads/m", New: "c2"})
			s.SetRules([]Rule{{Pattern: "refs/heads/m", NoNonFF: true}})
		}, "alice",
		[]Instruction{{Ref: "refs/heads/m", Old: "c2", New: "c1", Force: true}},
		[]Reason{ReasonProtectedNoNonFF},
		"强制标志只能抵消「未带强制的非快进」，受保护禁止非快进在前且不受强制影响")
	run("未带强制的非快进>批内冲突",
		preset(Instruction{Ref: "refs/heads/m", New: "c2"}), "alice",
		[]Instruction{
			{Ref: "refs/heads/m", Old: "c2", New: "c1"},
			{Ref: "refs/heads/m", Old: "c2", New: "c1"},
		},
		[]Reason{ReasonNonFastForward, ReasonNonFastForward},
		"同一引用出现两次且每条都是非快进，逐条判定先于批内检查，报非快进")
}

// TestBatchCollateral 覆盖整批连坐标记与被拒绝推送的零副作用。
func TestBatchCollateral(t *testing.T) {
	t.Run("他项拒绝导致通过项未执行", func(t *testing.T) {
		s, _ := newFixture(t)
		ins := []Instruction{
			{Ref: "refs/heads/good", New: "c1"},
			{Ref: "refs/heads/bad", New: "missing"},
		}
		res := s.Push("alice", ins)
		expect(t, fmt.Sprintf("%+v", ins), res,
			[]Reason{ReasonNotExecuted, ReasonObjectNotFound},
			"第二条对象不存在整批拒绝，第一条通过项被连坐标记")
		if _, ok := s.Get("refs/heads/good"); ok {
			t.Error("被拒绝的推送改变了引用")
		}
		if got := len(s.Audit()); got != 0 {
			t.Errorf("被拒绝的推送产生了审计记录: %d 条", got)
		}
	})
	t.Run("批内冲突连坐", func(t *testing.T) {
		s, _ := newFixture(t)
		mustPush(t, s, "alice", Instruction{Ref: "refs/heads/m", New: "c1"})
		ins := []Instruction{
			{Ref: "refs/heads/x", New: "c1"},
			{Ref: "refs/heads/x/y", New: "c2"},
			{Ref: "refs/heads/m", Old: "c1", New: "c2"},
		}
		res := s.Push("alice", ins)
		expect(t, fmt.Sprintf("%+v", ins), res,
			[]Reason{ReasonBatchConflict, ReasonBatchConflict, ReasonNotExecuted},
			"前两条构成祖先分段前缀冲突，第三条通过项被连坐")
		if v, _ := s.Get("refs/heads/m"); v != "c1" {
			t.Errorf("被拒绝的推送改变了引用: %q", v)
		}
	})
}

// TestAuditSequence 覆盖审计记录内容与序号严格递增无空洞。
func TestAuditSequence(t *testing.T) {
	s, _ := newFixture(t)
	r1 := mustPush(t, s, "alice",
		Instruction{Ref: "refs/heads/a", New: "c1"},
		Instruction{Ref: "refs/heads/b", New: "c2"})
	// 中间夹一批被拒绝的推送，不得消耗序号。
	s.Push("bob", []Instruction{{Ref: "refs/heads/a", Old: "c9", New: "c3"}})
	r2 := mustPush(t, s, "carol", Instruction{Ref: "refs/heads/a", Old: "c1", New: "c3"})

	audit := s.Audit()
	t.Logf("输入: 两批成功推送夹一批失败推送")
	t.Logf("实际输出: %+v", audit)
	t.Logf("判定依据: 序号严格递增无空洞，记录含全部更新前后值与推送者")
	if r1.AuditSeq != 1 || r2.AuditSeq != 2 || len(audit) != 2 {
		t.Fatalf("序号出现空洞: r1=%d r2=%d 记录数=%d", r1.AuditSeq, r2.AuditSeq, len(audit))
	}
	want0 := []RefUpdate{{Ref: "refs/heads/a", Old: "", New: "c1"}, {Ref: "refs/heads/b", Old: "", New: "c2"}}
	if audit[0].User != "alice" || len(audit[0].Updates) != 2 || audit[0].Updates[0] != want0[0] || audit[0].Updates[1] != want0[1] {
		t.Errorf("审计记录 1 内容不符: %+v", audit[0])
	}
	if audit[1].User != "carol" || len(audit[1].Updates) != 1 ||
		audit[1].Updates[0] != (RefUpdate{Ref: "refs/heads/a", Old: "c1", New: "c3"}) {
		t.Errorf("审计记录 2 内容不符: %+v", audit[1])
	}
}

// TestConcurrentSameOldValue 覆盖并发同旧值更新只成一批。
func TestConcurrentSameOldValue(t *testing.T) {
	s, _ := newFixture(t)
	mustPush(t, s, "alice", Instruction{Ref: "refs/heads/m", New: "c2"})

	const n = 8
	results := make(chan BatchResult, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 全部基于相同旧值 c2，各自指向不同的后代提交。
			newVal := fmt.Sprintf("c%d", 3+i)
			results <- s.Push(fmt.Sprintf("user%d", i),
				[]Instruction{{Ref: "refs/heads/m", Old: "c2", New: newVal}})
		}(i)
	}
	// 并发读引用与审计，只能看到整批前或后的状态（由 -race 与最终断言保证）。
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Get("refs/heads/m")
			s.Audit()
		}()
	}
	wg.Wait()
	close(results)

	applied, mismatched := 0, 0
	for res := range results {
		if res.Applied {
			applied++
		} else if res.Verdicts[0].Reason == ReasonOldValueMismatch {
			mismatched++
		}
	}
	t.Logf("输入: %d 个并发推送基于相同旧值 c2 更新同一引用", n)
	t.Logf("实际输出: 成功 %d 批，旧值不匹配 %d 批", applied, mismatched)
	t.Logf("判定依据: 推送串行化，恰好一批成功，其余看到新实际值报旧值不匹配")
	if applied != 1 || mismatched != n-1 {
		t.Errorf("并发结果不符: 成功 %d 批，旧值不匹配 %d 批", applied, mismatched)
	}
	if got := len(s.Audit()); got != 2 { // 预设 1 条 + 并发胜者 1 条
		t.Errorf("审计记录数不符: %d", got)
	}
}

// TestGraphPerformance 证明快进判定开销与无关提交数无关。
func TestGraphPerformance(t *testing.T) {
	g := NewGraph()
	prev := ""
	for i := 0; i < 2000; i++ {
		id := fmt.Sprintf("c%d", i)
		var parents []string
		if prev != "" {
			parents = []string{prev}
		}
		if err := g.Add(id, parents...); err != nil {
			t.Fatal(err)
		}
		prev = id
	}
	if !g.IsDescendantOrEqual("c0", "c1999") {
		t.Fatal("c1999 应是 c0 的后代")
	}
	before := g.LastVisited
	// 加入 20000 个与判定两点都无关的提交。
	for i := 0; i < 20000; i++ {
		if err := g.Add(fmt.Sprintf("u%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if !g.IsDescendantOrEqual("c0", "c1999") {
		t.Fatal("c1999 应是 c0 的后代")
	}
	after := g.LastVisited
	t.Logf("输入: 2000 提交主链 + 20000 无关提交，判定 c0->c1999")
	t.Logf("实际输出: 加入无关提交前访问 %d 个提交，之后访问 %d 个", before, after)
	t.Logf("判定依据: 代数剪枝使访问数不随无关提交增长，两次计数必须相等")
	if before != after {
		t.Errorf("访问数随无关提交增长: %d -> %d", before, after)
	}
	// 反向判定由代数剪枝直接拒绝，不访问任何提交。
	if g.IsDescendantOrEqual("c1999", "c0") {
		t.Fatal("c0 不是 c1999 的后代")
	}
	t.Logf("反向判定 c1999->c0 访问 %d 个提交（代数剪枝直接拒绝）", g.LastVisited)
	if g.LastVisited != 0 {
		t.Errorf("代数剪枝失效: 访问了 %d 个提交", g.LastVisited)
	}
	// 分叉非快进只访问两点之间的提交。
	ga := NewGraph()
	_ = ga.Add("r")
	pa, pb := "r", "r"
	for i := 0; i < 500; i++ {
		a, b := fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)
		_ = ga.Add(a, pa)
		_ = ga.Add(b, pb)
		pa, pb = a, b
	}
	if ga.IsDescendantOrEqual("a499", "b499") {
		t.Fatal("分叉侧支互不为后代")
	}
	t.Logf("分叉判定 a499->b499 访问 %d 个提交（图规模 %d）", ga.LastVisited, ga.Size())
	if ga.LastVisited > 2 {
		t.Errorf("分叉判定访问数异常: %d", ga.LastVisited)
	}
}

// TestRuleIndexPerformance 证明规则匹配开销与规则总数无关。
func TestRuleIndexPerformance(t *testing.T) {
	build := func(n int) *RuleIndex {
		rules := make([]Rule, n)
		for i := range rules {
			rules[i] = Rule{Pattern: fmt.Sprintf("refs/heads/feature-%d/*", i), NoDelete: true}
		}
		return NewRuleIndex(rules)
	}
	const target = "refs/heads/feature-2500/x"
	small := build(5000)
	rest := small.Match(target)
	if !rest.NoDelete {
		t.Fatal("应命中禁止删除规则")
	}
	before := small.LastVisited
	large := build(20000)
	large.Match(target)
	after := large.LastVisited
	t.Logf("输入: 5000 与 20000 条规则，匹配 %q", target)
	t.Logf("实际输出: 访问 trie 节点数 %d 与 %d", before, after)
	t.Logf("判定依据: 分段 trie 只沿命中路径下行，访问数不随规则总数增长")
	if before != after || before > 10 {
		t.Errorf("匹配开销随规则总数增长: %d -> %d", before, after)
	}
}
