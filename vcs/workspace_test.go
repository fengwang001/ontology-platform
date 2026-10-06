package vcs

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// codeOf 提取错误类别；nil 返回 -1，非 *Error 返回 -2。
func codeOf(err error) ErrCode {
	if err == nil {
		return -1
	}
	if e, ok := err.(*Error); ok {
		if e == nil {
			return -1
		}
		return e.Code
	}
	return -2
}

func mustWrite(t *testing.T, ws *Workspace, p, content string) {
	t.Helper()
	if err := ws.WriteFile(p, []byte(content)); err != nil {
		t.Fatalf("WriteFile(%q) 意外失败: %v", p, err)
	}
}

func mustStage(t *testing.T, ws *Workspace, paths ...string) {
	t.Helper()
	if err := ws.Stage(paths); err != nil {
		t.Fatalf("Stage(%v) 意外失败: %v", paths, err)
	}
}

func mustCommit(t *testing.T, ws *Workspace, allowEmpty bool) int {
	t.Helper()
	seq, err := ws.Commit(allowEmpty)
	if err != nil {
		t.Fatalf("Commit(%v) 意外失败: %v", allowEmpty, err)
	}
	return seq
}

func mustStatus(t *testing.T, ws *Workspace, p string) PathStatus {
	t.Helper()
	ps, err := ws.StatusOf(p)
	if err != nil {
		t.Fatalf("StatusOf(%q) 意外失败: %v", p, err)
	}
	return ps
}

func findStatus(list []PathStatus, p string) (PathStatus, bool) {
	for _, ps := range list {
		if ps.Path == p {
			return ps, true
		}
	}
	return PathStatus{}, false
}

// TestClassifyTable 用纯函数穷举存在性组合的代表取值，验证十类普通状态
// 各自可构造且每个输入恰好落入一类（classify 单返回值，分支互不重叠）。
func TestClassifyTable(t *testing.T) {
	A, B, C := []byte("A"), []byte("B"), []byte("C")
	cases := []struct {
		name    string
		tr      triple
		ignored bool
		want    Status
	}{
		{"仅工作树→未跟踪", triple{wt: A, hasWt: true}, false, StatusUntracked},
		{"仅工作树+命中忽略→被忽略", triple{wt: A, hasWt: true}, true, StatusIgnored},
		{"暂存新增且工作树一致→已暂存新增", triple{idx: A, hasIdx: true, wt: A, hasWt: true}, false, StatusStagedAdd},
		{"暂存新增后又改→暂存后又修改", triple{idx: A, hasIdx: true, wt: B, hasWt: true}, false, StatusStagedModifiedAgain},
		{"暂存新增后工作树删除→暂存后又修改", triple{idx: A, hasIdx: true}, false, StatusStagedModifiedAgain},
		{"快照有其余无→已暂存删除", triple{snap: A, hasSnap: true}, false, StatusStagedDeleted},
		{"暂存删除后同内容重建→暂存删除后又重建", triple{snap: A, hasSnap: true, wt: A, hasWt: true}, false, StatusStagedDeleteRecreated},
		{"暂存删除后异内容重建→暂存删除后又重建", triple{snap: A, hasSnap: true, wt: B, hasWt: true}, false, StatusStagedDeleteRecreated},
		{"快照暂存一致工作树缺→工作树删除", triple{snap: A, hasSnap: true, idx: A, hasIdx: true}, false, StatusWorktreeDeleted},
		{"暂存修改后工作树删除→暂存后又修改", triple{snap: A, hasSnap: true, idx: B, hasIdx: true}, false, StatusStagedModifiedAgain},
		{"三份一致→未变更", triple{snap: A, hasSnap: true, idx: A, hasIdx: true, wt: A, hasWt: true}, false, StatusUnchanged},
		{"快照暂存一致工作树改→工作树修改", triple{snap: A, hasSnap: true, idx: A, hasIdx: true, wt: B, hasWt: true}, false, StatusWorktreeModified},
		{"暂存与快照不同且工作树等于暂存→已暂存修改", triple{snap: A, hasSnap: true, idx: B, hasIdx: true, wt: B, hasWt: true}, false, StatusStagedModified},
		{"三份皆不同→暂存后又修改", triple{snap: A, hasSnap: true, idx: B, hasIdx: true, wt: C, hasWt: true}, false, StatusStagedModifiedAgain},
		{"暂存修改后改回快照内容→暂存后又修改", triple{snap: A, hasSnap: true, idx: B, hasIdx: true, wt: A, hasWt: true}, false, StatusStagedModifiedAgain},
	}
	seen := map[Status]bool{}
	for _, c := range cases {
		got := c.tr.classify(c.ignored)
		seen[got] = true
		t.Logf("输入=%s 实际=%s 期望=%s 判定依据=存在性组合分派唯一分支(互斥)", c.name, got, c.want)
		if got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
	for _, s := range []Status{StatusUnchanged, StatusStagedAdd, StatusStagedModified, StatusStagedDeleted,
		StatusWorktreeModified, StatusWorktreeDeleted, StatusUntracked, StatusStagedModifiedAgain,
		StatusStagedDeleteRecreated, StatusIgnored} {
		if !seen[s] {
			t.Errorf("状态 %s 未被任何用例构造出来", s)
		}
	}
	t.Logf("完备性: 十类普通状态全部出现; 互斥性: 每行 classify 只返回一个值")
}

// TestStatusCategoriesIntegration 通过真实操作构造十类普通状态。
func TestStatusCategoriesIntegration(t *testing.T) {
	ws := New(NewIgnoreSet("*.log"))
	for _, p := range []string{"u", "sm", "sd", "wm", "wd", "sdr"} {
		mustWrite(t, ws, p, "v1")
	}
	mustStage(t, ws, "u", "sm", "sd", "wm", "wd", "sdr")
	mustCommit(t, ws, false)

	mustWrite(t, ws, "sm", "v2")
	mustStage(t, ws, "sm")
	if err := ws.DeleteFile("sd"); err != nil {
		t.Fatal(err)
	}
	mustStage(t, ws, "sd")
	mustWrite(t, ws, "wm", "v2")
	if err := ws.DeleteFile("wd"); err != nil {
		t.Fatal(err)
	}
	if err := ws.DeleteFile("sdr"); err != nil {
		t.Fatal(err)
	}
	mustStage(t, ws, "sdr")
	mustWrite(t, ws, "sdr", "v2")
	mustWrite(t, ws, "sa", "v1")
	mustStage(t, ws, "sa")
	mustWrite(t, ws, "sma", "v1")
	mustStage(t, ws, "sma")
	mustWrite(t, ws, "sma", "v2")
	mustWrite(t, ws, "ut", "v1")
	mustWrite(t, ws, "ig.log", "v1")

	want := map[string]Status{
		"u": StatusUnchanged, "sm": StatusStagedModified, "sd": StatusStagedDeleted,
		"wm": StatusWorktreeModified, "wd": StatusWorktreeDeleted, "sdr": StatusStagedDeleteRecreated,
		"sa": StatusStagedAdd, "sma": StatusStagedModifiedAgain,
		"ut": StatusUntracked, "ig.log": StatusIgnored,
	}
	for p, s := range want {
		got := mustStatus(t, ws, p)
		t.Logf("输入=路径 %s 实际=%s 期望=%s 判定依据=三态存在性组合", p, got.Status, s)
		if got.Status != s {
			t.Errorf("路径 %s: got %s want %s", p, got.Status, s)
		}
	}
	list := ws.Status()
	if _, ok := findStatus(list, "u"); ok {
		t.Errorf("未变更路径 u 不应出现在整体状态列表")
	}
	t.Logf("整体状态列表=%v 判定依据=未变更路径不列出", list)
}

// TestIgnoreOnlyUntracked 忽略规则只作用于未跟踪路径。
func TestIgnoreOnlyUntracked(t *testing.T) {
	ws := New(NewIgnoreSet("*.log", "build/"))
	mustWrite(t, ws, "a.log", "x")
	mustWrite(t, ws, "b.txt", "x")
	mustWrite(t, ws, "build/out", "x")
	if got := mustStatus(t, ws, "a.log"); got.Status != StatusIgnored {
		t.Errorf("a.log: got %s want 被忽略", got.Status)
	}
	if got := mustStatus(t, ws, "build/out"); got.Status != StatusIgnored {
		t.Errorf("build/out: got %s want 被忽略", got.Status)
	}
	if got := mustStatus(t, ws, "b.txt"); got.Status != StatusUntracked {
		t.Errorf("b.txt: got %s want 未跟踪", got.Status)
	}
	t.Logf("输入=未跟踪的 a.log/build/out 与 b.txt 实际=被忽略/被忽略/未跟踪 判定依据=忽略只改未跟踪分类")

	mustStage(t, ws, "a.log", "build/out")
	if got := mustStatus(t, ws, "a.log"); got.Status != StatusStagedAdd {
		t.Errorf("暂存后 a.log: got %s want 已暂存新增", got.Status)
	}
	mustCommit(t, ws, false)
	mustWrite(t, ws, "a.log", "y")
	if got := mustStatus(t, ws, "a.log"); got.Status != StatusWorktreeModified {
		t.Errorf("提交后修改 a.log: got %s want 工作树修改", got.Status)
	}
	t.Logf("实际=暂存后为已暂存新增、提交修改后为工作树修改 判定依据=被快照或暂存记录的路径不受忽略影响")
}

// TestConflictKindsAndResolveDeleted 四种冲突细分 + 解决为删除 + 提交被阻塞并列出路径。
func TestConflictKindsAndResolveDeleted(t *testing.T) {
	ws := New(nil)
	mustWrite(t, ws, "c1", "v1")
	mustStage(t, ws, "c1")
	mustCommit(t, ws, false)

	mustIntro := func(p string, st ConflictStages) {
		t.Helper()
		if err := ws.IntroduceConflict(p, st); err != nil {
			t.Fatalf("IntroduceConflict(%q): %v", p, err)
		}
	}
	mustIntro("c1", ConflictStages{Base: Str("b"), Ours: Str("o"), Theirs: Str("t")})
	mustIntro("c2", ConflictStages{Ours: Str("o"), Theirs: Str("t")})
	mustIntro("c3", ConflictStages{Base: Str("b"), Theirs: Str("t")})
	mustIntro("c4", ConflictStages{Base: Str("b"), Ours: Str("o")})

	wantKind := map[string]ConflictKind{"c1": ConflictContent, "c2": ConflictBothAdded, "c3": ConflictDeletedByUs, "c4": ConflictDeletedByThem}
	for p, k := range wantKind {
		got := mustStatus(t, ws, p)
		t.Logf("输入=冲突路径 %s 实际=%s/%s 期望细分=%s 判定依据=阶段存在性分派", p, got.Status, got.Conflict, k)
		if got.Status != StatusConflict || got.Conflict != k {
			t.Errorf("%s: got %s/%s want 冲突/%s", p, got.Status, got.Conflict, k)
		}
	}

	if _, err := ws.Commit(false); codeOf(err) != CodeUnresolvedConflicts {
		t.Fatalf("有冲突时提交: got %v want 存在未解决冲突", err)
	} else {
		e := err.(*Error)
		t.Logf("输入=含4个冲突的提交 实际错误=%v 未解决列表=%v 判定依据=冲突未解决不允许提交并列出全部路径", e, e.Paths)
		if !reflect.DeepEqual(e.Paths, []string{"c1", "c2", "c3", "c4"}) {
			t.Errorf("未解决列表=%v", e.Paths)
		}
	}

	// c1 在快照中存在；先删除工作树文件，暂存即解决为删除。
	if err := ws.DeleteFile("c1"); err != nil {
		t.Fatal(err)
	}
	mustStage(t, ws, "c1")
	if got := mustStatus(t, ws, "c1"); got.Status != StatusStagedDeleted {
		t.Errorf("解决为删除后 c1: got %s want 已暂存删除", got.Status)
	}
	t.Logf("输入=工作树缺失的冲突 c1 执行暂存 实际=已暂存删除 判定依据=工作树不存在视为解决为删除")

	// c2 以工作树内容解决；c3、c4 从未入快照且工作树缺失，解决为删除后路径消失。
	mustWrite(t, ws, "c2", "merged")
	mustStage(t, ws, "c2", "c3", "c4")
	if un := ws.Unresolved(); len(un) != 0 {
		t.Errorf("解决后仍有未解决冲突: %v", un)
	}
	if _, err := ws.StatusOf("c3"); codeOf(err) != CodePathNotExist {
		t.Errorf("c3 解决为删除后应消失: got %v", err)
	}
	if got := mustStatus(t, ws, "c2"); got.Status != StatusStagedAdd {
		t.Errorf("c2 解决为内容: got %s want 已暂存新增", got.Status)
	}
	mustCommit(t, ws, false)
	t.Logf("实际=全部冲突解决后提交成功 判定依据=暂存清除全部阶段并以工作树内容入暂存")
}

// TestConflictPrecedence 冲突状态优先于普通分类。
func TestConflictPrecedence(t *testing.T) {
	ws := New(nil)
	mustWrite(t, ws, "p", "v1")
	mustStage(t, ws, "p")
	mustCommit(t, ws, false)
	if err := ws.IntroduceConflict("p", ConflictStages{Base: Str("b"), Ours: Str("o"), Theirs: Str("t")}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, ws, "p", "edited") // 若按普通分类将是工作树修改
	got := mustStatus(t, ws, "p")
	t.Logf("输入=冲突路径 p 且工作树另有修改 实际=%s/%s 判定依据=冲突状态优先于普通分类", got.Status, got.Conflict)
	if got.Status != StatusConflict || got.Conflict != ConflictContent {
		t.Errorf("got %s/%s want 冲突/内容冲突", got.Status, got.Conflict)
	}
}

// TestBatchAtomicAndDirExpansion 三类批操作的全有或全无与目录前缀展开。
func TestBatchAtomicAndDirExpansion(t *testing.T) {
	ws := New(nil)
	mustWrite(t, ws, "d/1", "v1")
	mustWrite(t, ws, "d/2", "v1")
	mustWrite(t, ws, "x", "v1")

	// 暂存：批内含不存在路径 → 整批不生效。
	if err := ws.Stage([]string{"x", "ghost"}); codeOf(err) != CodePathNotExist {
		t.Fatalf("Stage 含不存在路径: got %v want 路径不存在", err)
	}
	if got := mustStatus(t, ws, "x"); got.Status != StatusUntracked {
		t.Errorf("失败后 x 应保持未跟踪, got %s", got.Status)
	}
	t.Logf("输入=Stage[x ghost] 实际=报错且 x 仍未跟踪 判定依据=任一路径出错整批不生效")

	// 目录前缀展开。
	mustStage(t, ws, "d")
	for _, p := range []string{"d/1", "d/2"} {
		if got := mustStatus(t, ws, p); got.Status != StatusStagedAdd {
			t.Errorf("目录展开后 %s: got %s want 已暂存新增", p, got.Status)
		}
	}
	// 展开结果为空 → 路径不存在。
	if err := ws.Stage([]string{"nope"}); codeOf(err) != CodePathNotExist {
		t.Errorf("空展开: got %v want 路径不存在", err)
	}
	t.Logf("输入=Stage[d] 实际=d/1,d/2 均已暂存新增; Stage[nope] 报路径不存在 判定依据=目录前缀展开、空展开报错")

	// 撤销暂存：批内含不存在路径 → 整批不生效。
	if err := ws.Unstage([]string{"d/1", "ghost"}); codeOf(err) != CodePathNotExist {
		t.Fatalf("Unstage 含不存在路径: got %v want 路径不存在", err)
	}
	if got := mustStatus(t, ws, "d/1"); got.Status != StatusStagedAdd {
		t.Errorf("失败后 d/1 应保持已暂存新增, got %s", got.Status)
	}
	if err := ws.Unstage([]string{"d"}); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, ws, "d/2"); got.Status != StatusUntracked {
		t.Errorf("目录撤销后 d/2: got %s want 未跟踪", got.Status)
	}
	t.Logf("输入=Unstage[d/1 ghost] 与 Unstage[d] 实际=前者整批不生效、后者整目录撤销 判定依据=全有或全无+目录展开")

	// 丢弃：批内含不存在路径 → 整批不生效。
	mustStage(t, ws, "x")
	mustCommit(t, ws, false)
	mustWrite(t, ws, "x", "v2")
	if err := ws.Discard([]string{"x", "ghost"}, false); codeOf(err) != CodePathNotExist {
		t.Fatalf("Discard 含不存在路径: got %v want 路径不存在", err)
	}
	if got := mustStatus(t, ws, "x"); got.Status != StatusWorktreeModified {
		t.Errorf("失败后 x 应保持工作树修改, got %s", got.Status)
	}
	if err := ws.Discard([]string{"x"}, false); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, ws, "x"); got.Status != StatusUnchanged {
		t.Errorf("丢弃后 x: got %s want 未变更", got.Status)
	}
	t.Logf("输入=Discard[x ghost] 与 Discard[x] 实际=前者整批不生效、后者恢复未变更 判定依据=全有或全无")

	// 丢弃的目录展开。
	mustWrite(t, ws, "d/1", "v9")
	mustWrite(t, ws, "d/2", "v9")
	mustStage(t, ws, "d")
	mustCommit(t, ws, false)
	mustWrite(t, ws, "d/1", "v10")
	mustWrite(t, ws, "d/2", "v10")
	if err := ws.Discard([]string{"d"}, false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"d/1", "d/2"} {
		if got := mustStatus(t, ws, p); got.Status != StatusUnchanged {
			t.Errorf("目录丢弃后 %s: got %s want 未变更", p, got.Status)
		}
	}
	t.Logf("输入=Discard[d] 实际=d/1,d/2 均恢复未变更 判定依据=目录前缀展开")
}

// TestUnstageKeepsWorktree 撤销暂存不动工作树。
func TestUnstageKeepsWorktree(t *testing.T) {
	ws := New(nil)
	mustWrite(t, ws, "f", "v1")
	mustStage(t, ws, "f")
	mustCommit(t, ws, false)
	mustWrite(t, ws, "f", "v2")
	mustStage(t, ws, "f")
	if got := mustStatus(t, ws, "f"); got.Status != StatusStagedModified {
		t.Fatalf("前置: got %s want 已暂存修改", got.Status)
	}
	if err := ws.Unstage([]string{"f"}); err != nil {
		t.Fatal(err)
	}
	got := mustStatus(t, ws, "f")
	t.Logf("输入=已暂存修改的 f 撤销暂存 实际=%s 判定依据=暂存回到快照而工作树保留 v2 → 工作树修改", got.Status)
	if got.Status != StatusWorktreeModified {
		t.Errorf("got %s want 工作树修改(工作树未被触动)", got.Status)
	}
}

// TestDiscardUntrackedNeedsForce 丢弃未跟踪路径须带强制标志。
func TestDiscardUntrackedNeedsForce(t *testing.T) {
	ws := New(nil)
	mustWrite(t, ws, "u", "v1")
	if err := ws.Discard([]string{"u"}, false); codeOf(err) != CodeUntrackedNeedsForce {
		t.Fatalf("got %v want 未跟踪需强制", err)
	}
	if got := mustStatus(t, ws, "u"); got.Status != StatusUntracked {
		t.Errorf("被拒后 u 应保留: got %s", got.Status)
	}
	if err := ws.Discard([]string{"u"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.StatusOf("u"); codeOf(err) != CodePathNotExist {
		t.Errorf("强制丢弃后 u 应消失: got %v", err)
	}
	t.Logf("输入=未跟踪 u 分别不带/带强制丢弃 实际=先报未跟踪需强制且文件保留, 后删除成功 判定依据=强制标志语义")
}

// TestEmptyCommitFlag 空提交标志。
func TestEmptyCommitFlag(t *testing.T) {
	ws := New(nil)
	if _, err := ws.Commit(false); codeOf(err) != CodeNothingToCommit {
		t.Fatalf("空工作区提交: got %v want 无可提交内容", err)
	}
	seq, err := ws.Commit(true)
	if err != nil || seq != 1 {
		t.Fatalf("允许空提交: seq=%d err=%v", seq, err)
	}
	t.Logf("输入=暂存区与快照一致 实际=不带标志报无可提交内容、带标志提交序号=1 判定依据=允许空提交标志")
	mustWrite(t, ws, "a", "v1")
	mustStage(t, ws, "a")
	if seq := mustCommit(t, ws, false); seq != 2 {
		t.Errorf("正常提交序号: got %d want 2", seq)
	}
	if got := mustStatus(t, ws, "a"); got.Status != StatusUnchanged {
		t.Errorf("提交后 a: got %s want 未变更", got.Status)
	}
}

// TestErrorPrecedenceAdjacentPairs 错误次序中每对相邻错误的优先关系。
func TestErrorPrecedenceAdjacentPairs(t *testing.T) {
	newWs := func() *Workspace { return New(nil) }

	// 参数非法 < 路径不存在：同一批内同时触发。
	ws := newWs()
	mustWrite(t, ws, "ok", "v")
	err := ws.Stage([]string{"../bad", "ghost"})
	t.Logf("对1 输入=Stage[../bad ghost] 实际=%v 判定依据=参数非法优先于路径不存在", err)
	if codeOf(err) != CodeInvalidPath {
		t.Errorf("对1: got %v want 参数非法", err)
	}

	// 路径不存在 < 冲突中不可撤销：同一批撤销内同时触发。
	ws = newWs()
	if err := ws.IntroduceConflict("cf", ConflictStages{Base: Str("b"), Ours: Str("o")}); err != nil {
		t.Fatal(err)
	}
	err = ws.Unstage([]string{"ghost", "cf"})
	t.Logf("对2 输入=Unstage[ghost cf] 实际=%v 判定依据=路径不存在优先于冲突中不可撤销", err)
	if codeOf(err) != CodePathNotExist {
		t.Errorf("对2: got %v want 路径不存在", err)
	}

	// 冲突中不可撤销 < 未跟踪需强制：同一批丢弃内同时触发。
	mustWrite(t, ws, "ut", "v")
	err = ws.Discard([]string{"cf", "ut"}, false)
	t.Logf("对3 输入=Discard[cf ut] 无强制 实际=%v 判定依据=冲突中不可撤销优先于未跟踪需强制", err)
	if codeOf(err) != CodeInConflict {
		t.Errorf("对3: got %v want 冲突中不可撤销", err)
	}

	// 未跟踪需强制 < 存在未解决冲突：前者仅丢弃产生、后者仅提交产生，
	// 无任何单一调用可同时触发，故在全局裁决函数上验证相对次序。
	got := prioritize([]*Error{
		{Code: CodeUnresolvedConflicts, Detail: "提交"},
		{Code: CodeUntrackedNeedsForce, Detail: "丢弃"},
	})
	t.Logf("对4 输入=prioritize(存在未解决冲突, 未跟踪需强制) 实际=%v 判定依据=全局 Code 序即优先级", got.Code)
	if got.Code != CodeUntrackedNeedsForce {
		t.Errorf("对4: got %v want 未跟踪需强制", got.Code)
	}

	// 存在未解决冲突 < 无可提交内容：有冲突且暂存区与快照一致时提交。
	ws = newWs()
	if err := ws.IntroduceConflict("cf", ConflictStages{Ours: Str("o"), Theirs: Str("t")}); err != nil {
		t.Fatal(err)
	}
	_, err = ws.Commit(false)
	t.Logf("对5 输入=有冲突且暂存等于快照时 Commit 实际=%v 判定依据=存在未解决冲突优先于无可提交内容", err)
	if codeOf(err) != CodeUnresolvedConflicts {
		t.Errorf("对5: got %v want 存在未解决冲突", err)
	}
}

// TestPathValidation 路径规范化与祖先后代冲突的参数非法判定。
func TestPathValidation(t *testing.T) {
	ws := New(nil)
	for _, p := range []string{"", "/", "..", "../x", "a/../b", "."} {
		err := ws.WriteFile(p, []byte("v"))
		t.Logf("输入=WriteFile(%q) 实际=%v 判定依据=空/逃出根目录/规范化后为空为参数非法", p, err)
		if codeOf(err) != CodeInvalidPath {
			t.Errorf("WriteFile(%q): got %v want 参数非法", p, err)
		}
	}
	mustWrite(t, ws, "dir/f", "v")
	if err := ws.WriteFile("dir", []byte("v")); codeOf(err) != CodeInvalidPath {
		t.Errorf("dir 是已存在路径的前缀: got %v want 参数非法", err)
	}
	if err := ws.WriteFile("dir/f/g", []byte("v")); codeOf(err) != CodeInvalidPath {
		t.Errorf("dir/f 是已存在文件: got %v want 参数非法", err)
	}
	t.Logf("输入=WriteFile(dir) 与 WriteFile(dir/f/g) 实际=均参数非法 判定依据=与已存在路径互为祖先后代")

	if err := ws.Stage([]string{"a", "a"}); codeOf(err) != CodeInvalidPath {
		t.Errorf("批内重复: got %v want 参数非法", err)
	}
	if err := ws.Stage([]string{"dir/", "dir"}); codeOf(err) != CodeInvalidPath {
		t.Errorf("规范化后重复: got %v want 参数非法", err)
	}
	if err := ws.IntroduceConflict("c", ConflictStages{}); codeOf(err) != CodeInvalidPath {
		t.Errorf("无阶段冲突: got %v want 参数非法", err)
	}
	t.Logf("输入=重复批路径/空阶段冲突 实际=均参数非法 判定依据=批内重复与空冲突为参数非法")
}

// TestConcurrentSerialEquivalence 并发批操作与查询的串行等价：
// 各 goroutine 只在互不相交的目录上执行确定性脚本，最终状态必等于
// 任一串行顺序的结果；监视协程持续校验批操作原子性不变量。
func TestConcurrentSerialEquivalence(t *testing.T) {
	const workers = 6
	const rounds = 40

	script := func(ws *Workspace, i int) {
		dir := fmt.Sprintf("w%d", i)
		for k := 0; k < rounds; k++ {
			mustNoErr(t, ws.WriteFile(dir+"/a", []byte(fmt.Sprintf("%d-%d-a", i, k))))
			mustNoErr(t, ws.WriteFile(dir+"/b", []byte(fmt.Sprintf("%d-%d-b", i, k))))
			mustNoErr(t, ws.Stage([]string{dir}))
			mustNoErr(t, ws.Unstage([]string{dir + "/a"}))
			mustNoErr(t, ws.Discard([]string{dir + "/b"}, false))
			mustNoErr(t, ws.Stage([]string{dir + "/a"}))
			if k%5 == 4 {
				mustNoErr(t, ws.Unstage([]string{dir}))
			}
		}
	}

	ws := New(nil)
	// 原子性观测对：提交后由专用协程成对批操作。
	mustWrite(t, ws, "atom/x", "v0")
	mustWrite(t, ws, "atom/y", "v0")
	mustStage(t, ws, "atom/x", "atom/y")
	mustCommit(t, ws, false)
	// 预备工作树修改，使暂存/撤销在两个状态间往返（写入在监视开始前完成）。
	mustWrite(t, ws, "atom/x", "v1")
	mustWrite(t, ws, "atom/y", "v1")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	monitorDone := make(chan struct{})
	var atomicErr sync.Map

	// 监视：在同一个 Status() 快照内 atom/x 与 atom/y 的状态必须一致，
	// 否则说明观察到批操作只完成一半。
	go func() {
		defer close(monitorDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			list := ws.Status()
			sx, _ := findStatus(list, "atom/x")
			sy, _ := findStatus(list, "atom/y")
			if sx.Status != sy.Status {
				atomicErr.Store("mismatch", fmt.Sprintf("x=%s y=%s", sx.Status, sy.Status))
				return
			}
		}
	}()

	// 原子对操作协程：成对暂存/撤销，两路径状态应始终同步。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for k := 0; k < rounds; k++ {
			if err := ws.Stage([]string{"atom/x", "atom/y"}); err != nil {
				return
			}
			if err := ws.Unstage([]string{"atom/x", "atom/y"}); err != nil {
				return
			}
		}
	}()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			script(ws, i)
		}(i)
	}
	wg.Wait()
	close(stop)
	<-monitorDone

	if v, ok := atomicErr.Load("mismatch"); ok {
		t.Fatalf("观察到批操作半完成状态: %v", v)
	}

	// 串行重放同一批脚本：目录互不相交，结果与并发执行必然一致。
	seq := New(nil)
	mustWrite(t, seq, "atom/x", "v0")
	mustWrite(t, seq, "atom/y", "v0")
	mustStage(t, seq, "atom/x", "atom/y")
	mustCommit(t, seq, false)
	mustWrite(t, seq, "atom/x", "v1")
	mustWrite(t, seq, "atom/y", "v1")
	for k := 0; k < rounds; k++ {
		_ = seq.Stage([]string{"atom/x", "atom/y"})
		_ = seq.Unstage([]string{"atom/x", "atom/y"})
	}
	for i := 0; i < workers; i++ {
		script(seq, i)
	}
	got, want := ws.Status(), seq.Status()
	t.Logf("输入=%d 协程×%d 轮确定性脚本并发执行 实际状态条目=%d 串行重放条目=%d 判定依据=二者完全一致即串行等价",
		workers, rounds, len(got), len(want))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("并发结果与串行重放不一致:\n并发=%v\n串行=%v", got, want)
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Error(err)
	}
}

// TestSinglePathStatusCost 单路径分类开销不随路径总数增长（以分类计数器证明）。
func TestSinglePathStatusCost(t *testing.T) {
	for _, n := range []int{200, 20000} {
		ws := New(nil)
		for i := 0; i < n; i++ {
			p := fmt.Sprintf("dir/f%06d", i)
			if err := ws.WriteFile(p, []byte("x")); err != nil {
				t.Fatal(err)
			}
		}
		mustStage(t, ws, "dir")
		mustCommit(t, ws, false)
		mustWrite(t, ws, "dir/f000000", "y")
		before, _ := ws.Stats()
		got := mustStatus(t, ws, "dir/f000000")
		after, _ := ws.Stats()
		delta := after - before
		t.Logf("输入=总数 %d 的工作区查询单路径 实际分类次数=%d 状态=%s 判定依据=计数恒为 1, 与总数无关", n, delta, got.Status)
		if delta != 1 {
			t.Errorf("总数 %d: 单路径查询分类次数=%d want 1", n, delta)
		}
		if got.Status != StatusWorktreeModified {
			t.Errorf("got %s want 工作树修改", got.Status)
		}
	}
}

// TestStatusListCache 连续两次之间无变化的整体查询不重新分类任何路径。
func TestStatusListCache(t *testing.T) {
	const n = 3000
	ws := New(nil)
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("dir/f%06d", i)
		if err := ws.WriteFile(p, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	mustStage(t, ws, "dir")
	mustCommit(t, ws, false)
	for i := 0; i < 5; i++ {
		mustWrite(t, ws, fmt.Sprintf("dir/f%06d", i), "y")
	}

	before, _ := ws.Stats()
	first := ws.Status()
	after, _ := ws.Stats()
	t.Logf("输入=总数 %d 首次整体查询 实际分类次数=%d 判定依据=有变化时全量重算一次", n, after-before)
	if after-before != n {
		t.Errorf("首次查询分类次数=%d want %d", after-before, n)
	}

	before, _ = ws.Stats()
	second := ws.Status()
	after, _ = ws.Stats()
	t.Logf("输入=无变化的第二次整体查询 实际分类次数=%d 判定依据=缓存命中, 不随路径总数增长", after-before)
	if after-before != 0 {
		t.Errorf("无变化重复查询分类次数=%d want 0", after-before)
	}
	if !reflect.DeepEqual(first, second) || len(second) != 5 {
		t.Errorf("两次结果应一致且仅含 5 个变更路径: %v vs %v", first, second)
	}

	mustWrite(t, ws, "dir/f000005", "y")
	before, _ = ws.Stats()
	third := ws.Status()
	after, _ = ws.Stats()
	t.Logf("输入=单路径变化后的整体查询 实际分类次数=%d 判定依据=失效后仅重算一次", after-before)
	if after-before != n {
		t.Errorf("变化后查询分类次数=%d want %d", after-before, n)
	}
	if len(third) != 6 {
		t.Errorf("变化后列表长度=%d want 6", len(third))
	}
}
