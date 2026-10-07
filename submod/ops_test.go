package submod

import (
	"testing"
)

// mustRepo 建一个仓库并加入给定提交（parents 顺序即提交顺序）。
func mustRepo(t testing.TB, id string, commits ...*Commit) *Repo {
	t.Helper()
	r := NewRepo(id)
	for _, c := range commits {
		if err := r.AddCommit(c.ID, c.Parents, c.Table); err != nil {
			t.Fatalf("AddCommit %s/%s: %v", id, c.ID, err)
		}
	}
	return r
}

// cm 构造一个提交的便捷函数。
func cm(id string, parents []string, table Table) *Commit {
	return &Commit{ID: id, Parents: parents, Table: table}
}

// mustCoord 构造协调器，超级仓库为 super，分支为 main。
func mustCoord(t testing.TB, repos map[string]*Repo, superID string) *Coordinator {
	t.Helper()
	c, err := NewCoordinator(repos, superID, "main")
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	return c
}

// TestPathNormalizationAndConflicts 覆盖路径规范化与祖先后代冲突。
func TestPathNormalizationAndConflicts(t *testing.T) {
	cases := []struct {
		in   string
		want string // 空串表示期望报错
	}{
		{"a/b", "a/b"},
		{"a//b///c", "a/b/c"},
		{"./a/./b/", "a/b"},
		{`a\b\c`, "a/b/c"},
		{"a/../b", "b"},
		{"a/b/../../c", "c"},
		{"", ""},
		{".", ""},
		{"/", ""},
		{"..", ""},
		{"a/../..", ""},
	}
	for _, tc := range cases {
		got, err := NormalizePath(tc.in)
		if tc.want == "" {
			t.Logf("输入=%q 实际输出=错误(%v) 判定依据=空路径或越界..应报参数非法", tc.in, err)
			if err == nil {
				t.Fatalf("NormalizePath(%q) 期望错误，得到 %q", tc.in, got)
			}
			continue
		}
		t.Logf("输入=%q 实际输出=%q 判定依据=规范化结果应为 %q", tc.in, got, tc.want)
		if err != nil || got != tc.want {
			t.Fatalf("NormalizePath(%q) = %q, %v；期望 %q", tc.in, got, err, tc.want)
		}
	}

	// 祖先后代冲突：表内含 "a" 与 "a/b"，载入应拒绝。
	bad := mustRepo(t, "S", cm("s0", nil, Table{
		"a":   {Repo: "X", Commit: "x0"},
		"a/b": {Repo: "X", Commit: "x0"},
	}))
	if err := bad.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	repos := map[string]*Repo{"S": bad, "X": mustRepo(t, "X", cm("x0", nil, nil))}
	_, err := NewCoordinator(repos, "S", "main")
	t.Logf("输入=表{a, a/b} 实际输出=%v 判定依据=祖先后代冲突应报子模块表非法", err)
	se, ok := err.(*Error)
	if !ok || se.Code != ErrCodeInvalidTable {
		t.Fatalf("期望 ErrCodeInvalidTable，得到 %v", err)
	}

	// 规范化后重复："a//b" 与 "a/b" 冲突。
	dup := mustRepo(t, "S2", cm("s0", nil, Table{
		"a//b": {Repo: "X", Commit: "x0"},
		"a/b":  {Repo: "X", Commit: "x0"},
	}))
	if err := dup.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	_, err = NewCoordinator(map[string]*Repo{"S2": dup, "X": repos["X"]}, "S2", "main")
	t.Logf("输入=表{a//b, a/b} 实际输出=%v 判定依据=规范化后重复应报子模块表非法", err)
	if se, ok := err.(*Error); !ok || se.Code != ErrCodeInvalidTable {
		t.Fatalf("期望 ErrCodeInvalidTable，得到 %v", err)
	}
}

// TestStatusMutualExclusionAndPrecedence 覆盖五种状态的互斥与优先级。
func TestStatusMutualExclusionAndPrecedence(t *testing.T) {
	mk := func(id string, commits ...string) *Repo {
		var cs []*Commit
		for _, c := range commits {
			cs = append(cs, cm(c, nil, nil))
		}
		return mustRepo(t, id, cs...)
	}
	super := mustRepo(t, "S", cm("s0", nil, Table{
		"m1": {Repo: "A", Commit: "a1"},   // 一致
		"m2": {Repo: "B", Commit: "b1"},   // 检出偏离
		"m3": {Repo: "C", Commit: "c1"},   // 未提交修改
		"m4": {Repo: "D", Commit: "nope"}, // 悬空固定
		"m5": {Repo: "E", Commit: "e1"},   // 缺失
		"m6": {Repo: "F", Commit: "nope"}, // 悬空+脏 -> 悬空优先
		"m7": {Repo: "G", Commit: "g1"},   // 偏离+脏 -> 脏优先
	}))
	if err := super.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	repos := map[string]*Repo{
		"S": super,
		"A": mk("A", "a1"), "B": mk("B", "b1", "b2"), "C": mk("C", "c1"),
		"D": mk("D", "d1"), "E": mk("E", "e1"), "F": mk("F", "f1"),
		"G": mk("G", "g1", "g2"),
	}
	c := mustCoord(t, repos, "S")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := c.SetCheckout("m1", "a1")
	must(err)
	_, err = c.SetCheckout("m2", "b2")
	must(err)
	_, err = c.SetCheckout("m3", "c1")
	must(err)
	_, err = c.SetDirty("m3", true)
	must(err)
	_, err = c.SetCheckout("m6", "f1")
	must(err)
	_, err = c.SetDirty("m6", true)
	must(err)
	_, err = c.SetCheckout("m7", "g2")
	must(err)
	_, err = c.SetDirty("m7", true)
	must(err)

	got, err := c.StatusAll()
	must(err)
	want := map[string]Status{
		"m1": StatusConsistent, "m2": StatusDiverged, "m3": StatusDirty,
		"m4": StatusDangling, "m5": StatusMissing,
		"m6": StatusDangling, "m7": StatusDirty,
	}
	for p, w := range want {
		t.Logf("挂载点=%s 实际输出=%s 判定依据=期望 %s（互斥优先级：悬空>脏>偏离>缺失>一致）", p, got[p], w)
		if got[p] != w {
			t.Fatalf("挂载点 %s 状态=%s，期望 %s", p, got[p], w)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("状态数=%d，期望 %d", len(got), len(want))
	}
}

// mustSet 批量调用工作区事件并断言成功。
func mustSet(t *testing.T, f func() (uint64, error)) {
	t.Helper()
	if _, err := f(); err != nil {
		t.Fatal(err)
	}
}

// TestRecursiveUpdateThreeLevels 覆盖嵌套三层以上的递归对齐。
func TestRecursiveUpdateThreeLevels(t *testing.T) {
	d := mustRepo(t, "D", cm("d1", nil, nil))
	cRepo := mustRepo(t, "C", cm("c1", nil, Table{"d": {Repo: "D", Commit: "d1"}}))
	b := mustRepo(t, "B", cm("b1", nil, Table{"c": {Repo: "C", Commit: "c1"}}))
	a := mustRepo(t, "A",
		cm("a0", nil, nil),
		cm("a1", []string{"a0"}, Table{"b": {Repo: "B", Commit: "b1"}}))
	super := mustRepo(t, "S", cm("s0", nil, Table{"a": {Repo: "A", Commit: "a1"}}))
	if err := super.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	c := mustCoord(t, map[string]*Repo{"S": super, "A": a, "B": b, "C": cRepo, "D": d}, "S")

	// 工作区：a 偏离到 a0，a/b 缺失，a/b/c 偏离，a/b/c/d 缺失。
	mustSet(t, func() (uint64, error) { return c.SetCheckout("a", "a0") })
	mustSet(t, func() (uint64, error) { return c.SetCheckout("a/b/c", "c0") })
	revBefore := c.Revision()

	rev, err := c.UpdateToPins()
	if err != nil {
		t.Fatalf("UpdateToPins: %v", err)
	}
	got, _ := c.StatusAll()
	for _, p := range []string{"a", "a/b", "a/b/c", "a/b/c/d"} {
		co, _, _ := c.CheckoutState(p)
		t.Logf("挂载点=%s 实际输出=检出%s/状态%s 判定依据=递归对齐后四层都应一致", p, co, got[p])
		if got[p] != StatusConsistent {
			t.Fatalf("挂载点 %s 状态=%s，期望一致", p, got[p])
		}
	}
	t.Logf("输入=四层嵌套+两个偏离两个缺失 实际输出=序号%d->%d 判定依据=成功更新序号加一", revBefore, rev)
	if rev != revBefore+1 {
		t.Fatalf("序号=%d，期望 %d", rev, revBefore+1)
	}
}

// TestCycleVsSiblingMounts 区分同一仓库在并列挂载点与在祖先链上。
func TestCycleVsSiblingMounts(t *testing.T) {
	a := mustRepo(t, "A", cm("a1", nil, nil), cm("a2", nil, nil))
	super := mustRepo(t, "S", cm("s0", nil, Table{
		"x": {Repo: "A", Commit: "a1"},
		"y": {Repo: "A", Commit: "a2"},
	}))
	if err := super.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	c := mustCoord(t, map[string]*Repo{"S": super, "A": a}, "S")
	got, err := c.StatusAll()
	t.Logf("输入=同仓库并列挂载 x,y 实际输出=%v 判定依据=并列挂载不算循环", got)
	if err != nil || len(got) != 2 {
		t.Fatalf("并列挂载应合法，得到 %v, %v", got, err)
	}

	// 祖先链：S 挂 A，A 的固定提交又挂 S -> 循环。
	super2 := mustRepo(t, "S",
		cm("s0", nil, Table{"a": {Repo: "A", Commit: "a1"}}))
	a2repo := mustRepo(t, "A",
		cm("a1", nil, Table{"s": {Repo: "S", Commit: "s0"}}))
	if err := super2.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	cyc := mustCoord(t, map[string]*Repo{"S": super2, "A": a2repo}, "S")
	ops := map[string]func() error{
		"StatusAll":       func() error { _, e := cyc.StatusAll(); return e },
		"UpdateToPins":    func() error { _, e := cyc.UpdateToPins(); return e },
		"AdvanceTracking": func() error { _, e := cyc.AdvanceTracking(); return e },
		"AddMount":        func() error { _, e := cyc.AddMount("z", "A", "a1", ""); return e },
		"RemoveMount":     func() error { _, e := cyc.RemoveMount("a", true); return e },
	}
	for name, op := range ops {
		err := op()
		t.Logf("操作=%s 实际输出=%v 判定依据=循环挂载时递归操作一律拒绝", name, err)
		se, ok := err.(*Error)
		if !ok || se.Code != ErrCodeCircularMount {
			t.Fatalf("%s 期望循环挂载错误，得到 %v", name, err)
		}
	}
}

// TestDanglingAndDirtyRejectUpdateAtomically 覆盖悬空固定与未提交修改
// 对递归更新的整体拒绝：一个挂载点都不动。
func TestDanglingAndDirtyRejectUpdateAtomically(t *testing.T) {
	mk := func(id string, commits ...string) *Repo {
		var cs []*Commit
		for _, c := range commits {
			cs = append(cs, cm(c, nil, nil))
		}
		return mustRepo(t, id, cs...)
	}
	super := mustRepo(t, "S", cm("s0", nil, Table{
		"m1": {Repo: "A", Commit: "a1"},
		"m2": {Repo: "B", Commit: "nope"}, // 悬空
	}))
	if err := super.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	c := mustCoord(t, map[string]*Repo{"S": super, "A": mk("A", "a0", "a1"), "B": mk("B", "b1")}, "S")
	mustSet(t, func() (uint64, error) { return c.SetCheckout("m1", "a0") }) // 偏离
	revBefore := c.Revision()

	_, err := c.UpdateToPins()
	co1, _, _ := c.CheckoutState("m1")
	t.Logf("输入=m1偏离+m2悬空 实际输出=%v, m1检出=%s, 序号=%d 判定依据=悬空固定整体拒绝且一个挂载点都不动",
		err, co1, c.Revision())
	if se, ok := err.(*Error); !ok || se.Code != ErrCodeDanglingPin {
		t.Fatalf("期望悬空固定错误，得到 %v", err)
	}
	if co1 != "a0" || c.Revision() != revBefore {
		t.Fatalf("被拒绝的调用改变了状态：检出=%s 序号=%d", co1, c.Revision())
	}

	// 修复悬空（改超级仓库表），再制造脏：脏同样整体拒绝。
	super2 := mustRepo(t, "S", cm("s0", nil, Table{
		"m1": {Repo: "A", Commit: "a1"},
		"m2": {Repo: "B", Commit: "b1"},
	}))
	if err := super2.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	c2 := mustCoord(t, map[string]*Repo{"S": super2, "A": mk("A", "a0", "a1"), "B": mk("B", "b0", "b1")}, "S")
	mustSet(t, func() (uint64, error) { return c2.SetCheckout("m1", "a0") })
	mustSet(t, func() (uint64, error) { return c2.SetCheckout("m2", "b0") })
	mustSet(t, func() (uint64, error) { return c2.SetDirty("m1", true) })
	revBefore = c2.Revision()
	_, err = c2.UpdateToPins()
	co2, _, _ := c2.CheckoutState("m2")
	t.Logf("输入=m1脏+m2偏离 实际输出=%v, m2检出=%s, 序号=%d 判定依据=未提交修改整体拒绝且一个挂载点都不动",
		err, co2, c2.Revision())
	if se, ok := err.(*Error); !ok || se.Code != ErrCodeDirtyWorkspace {
		t.Fatalf("期望未提交修改错误，得到 %v", err)
	}
	if co2 != "b0" || c2.Revision() != revBefore {
		t.Fatalf("被拒绝的调用改变了状态：检出=%s 序号=%d", co2, c2.Revision())
	}

	// 清理脏标记后更新成功，两个挂载点都对齐。
	mustSet(t, func() (uint64, error) { return c2.SetDirty("m1", false) })
	if _, err := c2.UpdateToPins(); err != nil {
		t.Fatalf("UpdateToPins: %v", err)
	}
	got, _ := c2.StatusAll()
	t.Logf("输入=清理脏后更新 实际输出=%v 判定依据=全部对齐为一致", got)
	if got["m1"] != StatusConsistent || got["m2"] != StatusConsistent {
		t.Fatalf("期望全部一致，得到 %v", got)
	}
}

// TestAdvanceTracking 覆盖跟踪分支快进、非快进、分支不存在与整批拒绝。
func TestAdvanceTracking(t *testing.T) {
	// A: a1 -> a2（可快进）；B: b1 与 b2 无祖先关系（非快进），b3 是 b1 的后代。
	a := mustRepo(t, "A", cm("a1", nil, nil), cm("a2", []string{"a1"}, nil))
	b := mustRepo(t, "B",
		cm("b1", nil, nil), cm("b2", nil, nil), cm("b3", []string{"b1"}, nil))
	cRepo := mustRepo(t, "C", cm("c1", nil, nil))
	super := mustRepo(t, "S", cm("s0", nil, Table{
		"a": {Repo: "A", Commit: "a1", Track: "main"},
		"b": {Repo: "B", Commit: "b1", Track: "main"},
		"c": {Repo: "C", Commit: "c1"}, // 不跟踪
	}))
	if err := super.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetBranch("main", "a2"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetBranch("main", "b2"); err != nil {
		t.Fatal(err)
	}
	repos := map[string]*Repo{"S": super, "A": a, "B": b, "C": cRepo}
	c := mustCoord(t, repos, "S")
	revBefore := c.Revision()
	tipBefore := super.Branches["main"]

	// b 非快进 -> 整批拒绝，a 也不推进。
	_, err := c.AdvanceTracking()
	t.Logf("输入=a可快进+b非快进 实际输出=%v 判定依据=任一被拒整批不变", err)
	if se, ok := err.(*Error); !ok || se.Code != ErrCodeNonFastForward {
		t.Fatalf("期望跟踪分支非快进，得到 %v", err)
	}
	tab := c.SuperTable()
	t.Logf("实际输出=固定点a=%s b=%s 超级顶端=%s 序号=%d 判定依据=整批不变",
		tab["a"].Commit, tab["b"].Commit, super.Branches["main"], c.Revision())
	if tab["a"].Commit != "a1" || tab["b"].Commit != "b1" ||
		super.Branches["main"] != tipBefore || c.Revision() != revBefore {
		t.Fatalf("整批拒绝后状态被改变: %v", tab)
	}

	// 把 B 的分支移到 b3（b1 的后代）后整批推进成功。
	if err := b.SetBranch("main", "b3"); err != nil {
		t.Fatal(err)
	}
	rev, err := c.AdvanceTracking()
	if err != nil {
		t.Fatalf("AdvanceTracking: %v", err)
	}
	tab = c.SuperTable()
	newTip := super.Branches["main"]
	parentOK := len(super.Commits[newTip].Parents) == 1 && super.Commits[newTip].Parents[0] == tipBefore
	t.Logf("输入=b分支移到b3 实际输出=固定点a=%s b=%s c=%s 新超级提交=%s(父=%v) 序号=%d 判定依据=快进推进产生一次表变更",
		tab["a"].Commit, tab["b"].Commit, tab["c"].Commit, newTip, parentOK, rev)
	if tab["a"].Commit != "a2" || tab["b"].Commit != "b3" || tab["c"].Commit != "c1" {
		t.Fatalf("推进结果错误: %v", tab)
	}
	if !parentOK || rev != revBefore+1 {
		t.Fatalf("表变更或序号错误: 父链=%v 序号=%d", parentOK, rev)
	}

	// 分支不存在。
	super2 := mustRepo(t, "S2", cm("s0", nil, Table{
		"a": {Repo: "A", Commit: "a1", Track: "ghost"},
	}))
	if err := super2.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	c2 := mustCoord(t, map[string]*Repo{"S2": super2, "A": a}, "S2")
	_, err = c2.AdvanceTracking()
	t.Logf("输入=跟踪分支ghost 实际输出=%v 判定依据=分支不存在", err)
	if se, ok := err.(*Error); !ok || se.Code != ErrCodeBranchNotFound {
		t.Fatalf("期望分支不存在，得到 %v", err)
	}
}

// TestAddMountErrors 覆盖添加挂载的可区分错误与成功路径。
func TestAddMountErrors(t *testing.T) {
	a := mustRepo(t, "A", cm("a1", nil, nil))
	b := mustRepo(t, "B", cm("b1", nil, nil))
	super := mustRepo(t, "S", cm("s0", nil, Table{"m": {Repo: "A", Commit: "a1"}}))
	if err := super.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	c := mustCoord(t, map[string]*Repo{"S": super, "A": a, "B": b}, "S")

	cases := []struct {
		name                   string
		path, repo, com, track string
		want                   ErrCode
	}{
		{"空路径", "", "A", "a1", "", ErrCodeInvalidParam},
		{"空仓库标识", "z", "", "a1", "", ErrCodeInvalidParam},
		{"路径相同", "m", "A", "a1", "", ErrCodePathConflict},
		{"路径为后代", "m/sub", "B", "b1", "", ErrCodePathConflict},
		{"后代且同仓库", "m/sub", "A", "a1", "", ErrCodeCircularMount},
		{"路径为祖先", "", "A", "a1", "", ErrCodeInvalidParam}, // 占位，下面单独测
		{"仓库不存在", "z", "ghost", "a1", "", ErrCodeRepoNotFound},
		{"分支不存在", "z", "A", "a1", "ghost", ErrCodeBranchNotFound},
		{"固定提交不存在", "z", "A", "nope", "", ErrCodeDanglingPin},
	}
	for _, tc := range cases {
		if tc.name == "路径为祖先" {
			continue
		}
		_, err := c.AddMount(tc.path, tc.repo, tc.com, tc.track)
		t.Logf("用例=%s 输入=(%q,%q,%q,%q) 实际输出=%v 判定依据=期望%s",
			tc.name, tc.path, tc.repo, tc.com, tc.track, err, tc.want)
		se, ok := err.(*Error)
		if !ok || se.Code != tc.want {
			t.Fatalf("用例 %s 期望 %s，得到 %v", tc.name, tc.want, err)
		}
	}
	// 祖先方向冲突：先有 "d/e"，再加 "d"。
	if _, err := c.AddMount("d/e", "A", "a1", ""); err != nil {
		t.Fatal(err)
	}
	_, err := c.AddMount("d", "A", "a1", "")
	t.Logf("用例=路径为祖先 实际输出=%v 判定依据=期望路径冲突", err)
	if se, ok := err.(*Error); !ok || se.Code != ErrCodePathConflict {
		t.Fatalf("期望路径冲突，得到 %v", err)
	}

	// 成功：新记录落地为一次超级表变更。
	revBefore := c.Revision()
	rev, err := c.AddMount("z", "A", "a1", "")
	if err != nil {
		t.Fatalf("AddMount: %v", err)
	}
	tab := c.SuperTable()
	t.Logf("输入=添加z 实际输出=表%v 序号=%d 判定依据=成功添加且序号加一", tab, rev)
	if tab["z"].Repo != "A" || tab["z"].Commit != "a1" || rev != revBefore+1 {
		t.Fatalf("添加失败: %v 序号=%d", tab, rev)
	}
}

// TestRemoveMountForce 覆盖强制移除。
func TestRemoveMountForce(t *testing.T) {
	a := mustRepo(t, "A", cm("a1", nil, nil))
	super := mustRepo(t, "S", cm("s0", nil, Table{
		"m1": {Repo: "A", Commit: "a1"},
		"m2": {Repo: "A", Commit: "a1"},
	}))
	if err := super.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	c := mustCoord(t, map[string]*Repo{"S": super, "A": a}, "S")
	mustSet(t, func() (uint64, error) { return c.SetCheckout("m1", "a1") })
	mustSet(t, func() (uint64, error) { return c.SetDirty("m1", true) })

	_, err := c.RemoveMount("m1", false)
	t.Logf("输入=移除脏挂载m1(非强制) 实际输出=%v 判定依据=有未提交修改拒绝", err)
	if se, ok := err.(*Error); !ok || se.Code != ErrCodeDirtyWorkspace {
		t.Fatalf("期望未提交修改错误，得到 %v", err)
	}
	if _, ok := c.SuperTable()["m1"]; !ok {
		t.Fatal("被拒绝的移除改变了表")
	}

	rev, err := c.RemoveMount("m1", true)
	_, _, ok := c.CheckoutState("m1")
	t.Logf("输入=强制移除m1 实际输出=%v 检出残留=%v 序号=%d 判定依据=强制允许丢弃且清理检出", err, ok, rev)
	if err != nil || ok {
		t.Fatalf("强制移除失败: %v 检出残留=%v", err, ok)
	}

	if _, err := c.RemoveMount("m2", false); err != nil {
		t.Fatalf("干净挂载移除应成功: %v", err)
	}
	_, err = c.RemoveMount("m2", false)
	t.Logf("输入=重复移除m2 实际输出=%v 判定依据=挂载点不存在", err)
	if se, ok := err.(*Error); !ok || se.Code != ErrCodeMountNotFound {
		t.Fatalf("期望挂载点不存在，得到 %v", err)
	}
}

// TestErrorPrecedencePairs 覆盖错误次序中每对相邻错误的优先关系。
func TestErrorPrecedencePairs(t *testing.T) {
	// 循环树：S 挂 A，A 挂回 S。
	cycSuper := mustRepo(t, "S", cm("s0", nil, Table{"a": {Repo: "A", Commit: "a1"}}))
	cycA := mustRepo(t, "A", cm("a1", nil, Table{"s": {Repo: "S", Commit: "s0"}}))
	if err := cycSuper.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	cyc := mustCoord(t, map[string]*Repo{"S": cycSuper, "A": cycA}, "S")

	// 普通树：m1 干净挂载，adv 系列用于推进场景。
	a := mustRepo(t, "A",
		cm("a1", nil, nil), cm("a2", []string{"a1"}, nil))
	b := mustRepo(t, "B", cm("b1", nil, nil), cm("b2", nil, nil))
	if err := a.SetBranch("main", "a2"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetBranch("main", "b2"); err != nil {
		t.Fatal(err)
	}
	newPlain := func(table Table) *Coordinator {
		super := mustRepo(t, "S", cm("s0", nil, table))
		if err := super.SetBranch("main", "s0"); err != nil {
			t.Fatal(err)
		}
		return mustCoord(t, map[string]*Repo{"S": super, "A": a, "B": b}, "S")
	}

	type pair struct {
		name string
		run  func() error
		want ErrCode
		why  string
	}
	pairs := []pair{
		{"参数非法<循环挂载", func() error {
			_, e := cyc.AddMount("", "A", "a1", "")
			return e
		}, ErrCodeInvalidParam, "空路径优先于循环挂载"},
		{"循环挂载<路径冲突", func() error {
			_, e := cyc.AddMount("a", "B", "b1", "")
			return e
		}, ErrCodeCircularMount, "循环树中路径冲突不报"},
		{"路径冲突<仓库不存在", func() error {
			c := newPlain(Table{"m": {Repo: "A", Commit: "a1"}})
			_, e := c.AddMount("m", "ghost", "x", "")
			return e
		}, ErrCodePathConflict, "路径冲突优先于仓库不存在"},
		{"仓库不存在<分支不存在", func() error {
			c := newPlain(Table{
				"r1": {Repo: "ghost", Commit: "x", Track: "br"},
				"r2": {Repo: "A", Commit: "a1", Track: "ghost"},
			})
			_, e := c.AdvanceTracking()
			return e
		}, ErrCodeRepoNotFound, "仓库不存在优先于分支不存在"},
		{"分支不存在<悬空固定", func() error {
			c := newPlain(Table{
				"r1": {Repo: "A", Commit: "a1", Track: "ghost"},
				"r2": {Repo: "B", Commit: "nope", Track: "main"},
			})
			_, e := c.AdvanceTracking()
			return e
		}, ErrCodeBranchNotFound, "分支不存在优先于悬空固定"},
		{"悬空固定<未提交修改", func() error {
			c := newPlain(Table{
				"r1": {Repo: "A", Commit: "nope"},
				"r2": {Repo: "B", Commit: "b1"},
			})
			if _, e := c.SetCheckout("r2", "b1"); e != nil {
				t.Fatal(e)
			}
			if _, e := c.SetDirty("r2", true); e != nil {
				t.Fatal(e)
			}
			_, e := c.UpdateToPins()
			return e
		}, ErrCodeDanglingPin, "悬空固定优先于未提交修改"},
		{"未提交修改<跟踪分支非快进", func() error {
			c := newPlain(Table{
				"r1": {Repo: "A", Commit: "a1", Track: "main"}, // 可快进但脏
				"r2": {Repo: "B", Commit: "b1", Track: "main"}, // 非快进
			})
			if _, e := c.SetCheckout("r1", "a1"); e != nil {
				t.Fatal(e)
			}
			if _, e := c.SetDirty("r1", true); e != nil {
				t.Fatal(e)
			}
			_, e := c.AdvanceTracking()
			return e
		}, ErrCodeDirtyWorkspace, "未提交修改优先于跟踪分支非快进"},
	}
	for _, p := range pairs {
		err := p.run()
		t.Logf("相邻对=%s 实际输出=%v 判定依据=%s", p.name, err, p.why)
		se, ok := err.(*Error)
		if !ok || se.Code != p.want {
			t.Fatalf("相邻对 %s 期望 %s，得到 %v", p.name, p.want, err)
		}
	}
}
