package submod

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// chainRepo 构造线性提交链 ids[0] <- ids[1] <- ...，可选把 branch 指向末端。
func chainRepo(store *Store, id RepoID, branch string, ids ...CommitID) *Repo {
	r := NewRepo(id)
	var parents []CommitID
	for _, cid := range ids {
		commit := &Commit{ID: cid, Parents: parents}
		r.AddCommit(commit)
		parents = []CommitID{cid}
	}
	if branch != "" {
		r.Branches[branch] = ids[len(ids)-1]
	}
	store.Add(r)
	return r
}

// newSuper 构造超级仓库，head 提交附带给定子模块表。
func newSuper(t *testing.T, store *Store, records ...SubmoduleRecord) *Coordinator {
	t.Helper()
	tbl, err := NewTable(records...)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	chainRepo(store, "S", "", "s0")
	store.Get("S").Commits["s0"].Table = tbl
	c, err := NewCoordinator(store, "S", "s0")
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	return c
}

func mustStatus(t *testing.T, c *Coordinator, path string) Status {
	t.Helper()
	s, err := c.Status(path)
	if err != nil {
		t.Fatalf("Status(%q): %v", path, err)
	}
	return s
}

func pinnedOf(t *testing.T, c *Coordinator, path string) CommitID {
	t.Helper()
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.headTable()[path]
	if !ok {
		t.Fatalf("no record at %q", path)
	}
	return rec.Pinned
}

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr error
	}{
		{"a/b", "a/b", nil},
		{"/a//b/", "a/b", nil},
		{"./a/./b", "a/b", nil},
		{"a/../b", "b", nil},
		{"  a/b  ", "a/b", nil},
		{"", "", ErrInvalidParam},
		{"/", "", ErrInvalidParam},
		{"   ", "", ErrInvalidParam},
	}
	for _, tc := range cases {
		got, err := NormalizePath(tc.in)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("NormalizePath(%q) err=%v, want %v", tc.in, err, tc.wantErr)
		}
		if tc.wantErr == nil && got != tc.want {
			t.Fatalf("NormalizePath(%q)=%q, want %q", tc.in, got, tc.want)
		}
		t.Logf("输入=%q 实际输出=%q err=%v 判定依据=规范化规则与非法参数错误", tc.in, got, err)
	}
}

func TestTablePathConflict(t *testing.T) {
	rec := func(p string) SubmoduleRecord {
		return SubmoduleRecord{Path: p, Repo: "R", Pinned: "r1"}
	}
	if _, err := NewTable(rec("a"), rec("a")); !errors.Is(err, ErrPathConflict) {
		t.Fatalf("相同路径应报路径冲突, got %v", err)
	}
	t.Logf("输入=两条相同路径记录 实际输出=ErrPathConflict 判定依据=路径相同即冲突")
	if _, err := NewTable(rec("a"), rec("a/b")); !errors.Is(err, ErrPathConflict) {
		t.Fatalf("祖先后代路径应报路径冲突, got %v", err)
	}
	t.Logf("输入=a 与 a/b 实际输出=ErrPathConflict 判定依据=祖先目录即冲突")
	tbl, err := NewTable(rec("a/b"), rec("a/c"), rec("ab"))
	if err != nil {
		t.Fatalf("兄弟路径与共享前缀不应冲突, got %v", err)
	}
	if _, ok := tbl["a/b"]; !ok {
		t.Fatalf("表应按键索引")
	}
	t.Logf("输入=a/b、a/c、ab 实际输出=合法表 判定依据=按段对齐的祖先后代判定，ab 不是 a/b 的祖先")
	if _, err := NewTable(SubmoduleRecord{Path: "x", Repo: "", Pinned: "p"}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("空仓库标识应报参数非法, got %v", err)
	}
	t.Logf("输入=空仓库标识 实际输出=ErrInvalidParam 判定依据=参数非法")
}

func TestStatusKindsMutualExclusion(t *testing.T) {
	store := NewStore()
	chainRepo(store, "A", "", "a1")
	chainRepo(store, "B", "", "b0", "b1")
	chainRepo(store, "C", "", "c0", "c1")
	chainRepo(store, "D", "", "d1")
	c := newSuper(t, store,
		SubmoduleRecord{Path: "clean", Repo: "A", Pinned: "a1"},
		SubmoduleRecord{Path: "diverged", Repo: "B", Pinned: "b1"},
		SubmoduleRecord{Path: "mod", Repo: "C", Pinned: "c1"},
		SubmoduleRecord{Path: "dangling", Repo: "D", Pinned: "d-missing"},
		SubmoduleRecord{Path: "absent", Repo: "A", Pinned: "a1"},
	)
	mustCheckout := func(p string, id CommitID, dirty bool) {
		if err := c.SetCheckout(p, id, dirty); err != nil {
			t.Fatalf("SetCheckout(%q): %v", p, err)
		}
	}
	mustCheckout("clean", "a1", false)
	mustCheckout("diverged", "b0", false)
	mustCheckout("mod", "c0", true) // 偏离与未提交修改同时存在
	// dangling 与 absent 均无检出；dangling 的固定提交不存在。

	want := map[string]Status{
		"clean":    StatusClean,
		"diverged": StatusDiverged,
		"mod":      StatusDirty, // 未提交修改优先于偏离
		"dangling": StatusDangling,
		"absent":   StatusMissing,
	}
	for path, ws := range want {
		got := mustStatus(t, c, path)
		if got != ws {
			t.Fatalf("Status(%q)=%v, want %v", path, got, ws)
		}
		t.Logf("挂载点=%q 实际输出=%v 判定依据=五种状态互斥，未提交修改优先于偏离", path, got)
	}
	// 悬空固定优先于缺失：dangling 挂载点也没有检出，但报悬空。
	t.Logf("判定依据=优先级 悬空固定>缺失>未提交修改>偏离>一致 全部满足")
}

// buildNestedThree 构造三层嵌套：S/a -> A/a1，a1 内 b -> B/b1，b1 内 c -> C/c1。
func buildNestedThree(t *testing.T) (*Coordinator, *Store) {
	t.Helper()
	store := NewStore()
	chainRepo(store, "A", "", "a1")
	chainRepo(store, "B", "", "b1")
	chainRepo(store, "C", "", "c1")
	chainRepo(store, "D", "", "d1")
	tblC, err := NewTable(SubmoduleRecord{Path: "d", Repo: "D", Pinned: "d1"})
	if err != nil {
		t.Fatal(err)
	}
	store.Get("C").Commits["c1"].Table = tblC
	tblB, err := NewTable(SubmoduleRecord{Path: "c", Repo: "C", Pinned: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	store.Get("B").Commits["b1"].Table = tblB
	tblA, err := NewTable(SubmoduleRecord{Path: "b", Repo: "B", Pinned: "b1"})
	if err != nil {
		t.Fatal(err)
	}
	store.Get("A").Commits["a1"].Table = tblA
	c := newSuper(t, store, SubmoduleRecord{Path: "a", Repo: "A", Pinned: "a1"})
	return c, store
}

func TestAlignNestedThreeLevels(t *testing.T) {
	c, _ := buildNestedThree(t)
	// 所有层都偏离固定点。
	for _, p := range []string{"a", "a/b", "a/b/c", "a/b/c/d"} {
		if err := c.SetCheckout(p, CommitID("stale-"+p), false); err != nil {
			t.Fatalf("SetCheckout(%q): %v", p, err)
		}
	}
	t.Logf("输入=四层挂载树 a、a/b、a/b/c、a/b/c/d 全部偏离固定点")
	if err := c.Align(); err != nil {
		t.Fatalf("Align: %v", err)
	}
	want := map[string]CommitID{"a": "a1", "a/b": "b1", "a/b/c": "c1", "a/b/c/d": "d1"}
	for p, pin := range want {
		wt, ok := c.Checkout(p)
		if !ok || wt.Checkout != pin {
			t.Fatalf("Checkout(%q)=%v,%v want %q", p, wt, ok, pin)
		}
		if s := mustStatus(t, c, p); s != StatusClean {
			t.Fatalf("Status(%q)=%v want clean", p, s)
		}
		t.Logf("挂载点=%q 实际输出=检出 %q 状态 clean 判定依据=递归对齐后检出==固定", p, pin)
	}
}

func TestSameRepoSiblingVsAncestorChain(t *testing.T) {
	// 同一仓库挂在互不为祖先的两个挂载点：不是循环。
	store := NewStore()
	chainRepo(store, "X", "", "x1")
	c := newSuper(t, store,
		SubmoduleRecord{Path: "p", Repo: "X", Pinned: "x1"},
		SubmoduleRecord{Path: "q", Repo: "X", Pinned: "x1"},
	)
	if err := c.Align(); err != nil {
		t.Fatalf("并列挂载同仓库不应视为循环, got %v", err)
	}
	t.Logf("输入=仓库 X 并列挂在 p 与 q 实际输出=Align 成功 判定依据=并列挂载不算循环")

	// 同一仓库出现在自己的祖先挂载链上：循环。
	store2 := NewStore()
	x := chainRepo(store2, "X", "", "x1")
	tbl, err := NewTable(SubmoduleRecord{Path: "r", Repo: "X", Pinned: "x1"})
	if err != nil {
		t.Fatal(err)
	}
	x.Commits["x1"].Table = tbl
	c2 := newSuper(t, store2, SubmoduleRecord{Path: "p", Repo: "X", Pinned: "x1"})
	if err := c2.Align(); !errors.Is(err, ErrCycle) {
		t.Fatalf("祖先链上重复出现应报循环挂载, got %v", err)
	}
	if _, err := c2.Statuses(); !errors.Is(err, ErrCycle) {
		t.Fatalf("Statuses 应报循环挂载, got %v", err)
	}
	if _, err := c2.Status("p/r"); !errors.Is(err, ErrCycle) {
		t.Fatalf("Status(p/r) 应报循环挂载, got %v", err)
	}
	t.Logf("输入=X 固定在 p，其提交 x1 又把 X 挂在 r 实际输出=ErrCycle 判定依据=祖先链重复即循环")
}

func TestAlignRejectsDanglingAndDirtyAtomically(t *testing.T) {
	store := NewStore()
	chainRepo(store, "A", "", "a1")
	chainRepo(store, "B", "", "b1")
	chainRepo(store, "C", "", "c1")
	c := newSuper(t, store,
		SubmoduleRecord{Path: "ok", Repo: "A", Pinned: "a1"},
		SubmoduleRecord{Path: "dang", Repo: "B", Pinned: "b-missing"},
		SubmoduleRecord{Path: "mod", Repo: "C", Pinned: "c1"},
	)
	if err := c.SetCheckout("ok", "stale", false); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCheckout("mod", "c1", true); err != nil {
		t.Fatal(err)
	}
	gen0 := c.Generation()
	// 悬空固定与未提交修改同时存在：报次序更靠前的悬空固定。
	if err := c.Align(); !errors.Is(err, ErrDanglingPin) {
		t.Fatalf("应先报悬空固定, got %v", err)
	}
	t.Logf("输入=悬空(dang)+脏(mod)+偏离(ok) 实际输出=ErrDanglingPin 判定依据=悬空优先于未提交修改")
	if wt, _ := c.Checkout("ok"); wt.Checkout != "stale" {
		t.Fatalf("整体拒绝后任何挂载点都不应变动, ok=%q", wt.Checkout)
	}
	if c.Generation() != gen0 {
		t.Fatalf("被拒绝的调用不得改变序号")
	}
	// 修复悬空后报未提交修改，仍然整体不动。
	store.Get("B").AddCommit(&Commit{ID: "b-missing"})
	if err := c.Align(); !errors.Is(err, ErrDirty) {
		t.Fatalf("应报未提交修改, got %v", err)
	}
	t.Logf("输入=脏(mod)+偏离(ok) 实际输出=ErrDirty 判定依据=未提交修改整体拒绝递归更新")
	if wt, _ := c.Checkout("ok"); wt.Checkout != "stale" {
		t.Fatalf("整体拒绝后任何挂载点都不应变动, ok=%q", wt.Checkout)
	}
	if c.Generation() != gen0 {
		t.Fatalf("被拒绝的调用不得改变序号")
	}
	// 清理脏标志后整体落地。
	if err := c.SetCheckout("mod", "c1", false); err != nil {
		t.Fatal(err)
	}
	if err := c.Align(); err != nil {
		t.Fatalf("Align: %v", err)
	}
	for p, pin := range map[string]CommitID{"ok": "a1", "dang": "b-missing", "mod": "c1"} {
		if wt, _ := c.Checkout(p); wt.Checkout != pin {
			t.Fatalf("Checkout(%q)=%q want %q", p, wt.Checkout, pin)
		}
	}
	t.Logf("实际输出=全部对齐 判定依据=一次递归更新要么全部落地要么完全不发生")
}

func TestAdvanceFastForwardAndBatchRejection(t *testing.T) {
	store := NewStore()
	chainRepo(store, "R", "main", "r1", "r2", "r3")
	store.Get("R").Branches["main"] = "r2"
	chainRepo(store, "Q", "dev", "q1", "q2")
	c := newSuper(t, store,
		SubmoduleRecord{Path: "m", Repo: "R", Pinned: "r1", Tracking: "main"},
		SubmoduleRecord{Path: "n", Repo: "Q", Pinned: "q1", Tracking: "dev"},
		SubmoduleRecord{Path: "u", Repo: "Q", Pinned: "q1"}, // 不跟踪，不参与推进
	)
	gen0 := c.Generation()
	head0 := c.Head()
	if err := c.Advance(); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got := pinnedOf(t, c, "m"); got != "r2" {
		t.Fatalf("m 固定点=%q want r2", got)
	}
	if got := pinnedOf(t, c, "n"); got != "q2" {
		t.Fatalf("n 固定点=%q want q2", got)
	}
	if got := pinnedOf(t, c, "u"); got != "q1" {
		t.Fatalf("未配置跟踪分支的记录不应推进, got %q", got)
	}
	if c.Generation() != gen0+1 || c.Head() == head0 {
		t.Fatalf("推进应产生一次超级仓库子模块表变更")
	}
	t.Logf("输入=m(r1->main@r2)、n(q1->dev@q2)、u(不跟踪) 实际输出=m=r2,n=q2,u=q1,gen+1 判定依据=快进推进且整批落地")

	// 非快进：main 移到不是 r2 后代的提交。
	store.Get("R").AddCommit(&Commit{ID: "r9", Parents: []CommitID{"r1"}})
	store.Get("R").Branches["main"] = "r9"
	gen1 := c.Generation()
	if err := c.Advance(); !errors.Is(err, ErrNonFastForward) {
		t.Fatalf("应报跟踪分支非快进, got %v", err)
	}
	if got := pinnedOf(t, c, "m"); got != "r2" {
		t.Fatalf("整批拒绝后 m 不应变, got %q", got)
	}
	if got := pinnedOf(t, c, "n"); got != "q2" {
		t.Fatalf("整批拒绝后 n 不应变（即使它可快进）, got %q", got)
	}
	if c.Generation() != gen1 {
		t.Fatalf("被拒绝的调用不得改变序号")
	}
	t.Logf("输入=m 非快进、n 可快进 实际输出=ErrNonFastForward 且两者固定点均不变 判定依据=任何一条被拒整批不变")

	// 分支不存在。
	delete(store.Get("R").Branches, "main")
	if err := c.Advance(); !errors.Is(err, ErrBranchNotFound) {
		t.Fatalf("应报分支不存在, got %v", err)
	}
	t.Logf("输入=跟踪分支 main 被删除 实际输出=ErrBranchNotFound 判定依据=分支不存在可区分")
}

func TestRemoveMount(t *testing.T) {
	store := NewStore()
	chainRepo(store, "A", "", "a1")
	c := newSuper(t, store, SubmoduleRecord{Path: "x", Repo: "A", Pinned: "a1"})
	if err := c.SetCheckout("x", "a1", true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetCheckout("x/y", "n1", false); err != nil {
		t.Fatal(err)
	}
	gen0 := c.Generation()
	if err := c.RemoveMount("x", false); !errors.Is(err, ErrDirty) {
		t.Fatalf("有未提交修改应拒绝移除, got %v", err)
	}
	if c.Generation() != gen0 {
		t.Fatalf("被拒绝的调用不得改变序号")
	}
	t.Logf("输入=挂载点 x 有未提交修改, force=false 实际输出=ErrDirty 判定依据=未提交修改拒绝移除")
	if err := c.RemoveMount("x", true); err != nil {
		t.Fatalf("强制移除应成功, got %v", err)
	}
	if _, err := c.Status("x"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("移除后挂载点不应存在, got %v", err)
	}
	if _, ok := c.Checkout("x"); ok {
		t.Fatalf("强制移除应丢弃检出")
	}
	if _, ok := c.Checkout("x/y"); ok {
		t.Fatalf("强制移除应级联丢弃嵌套检出")
	}
	t.Logf("输入=force=true 实际输出=记录与嵌套检出均被丢弃, gen+1 判定依据=强制标志允许丢弃")
	if err := c.RemoveMount("nope", true); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("移除不存在的挂载点应报参数非法, got %v", err)
	}
}

// TestErrorPrecedenceAdjacentPairs 覆盖错误次序中每对相邻错误的优先关系：
// 参数非法 > 循环挂载 > 路径冲突 > 仓库不存在 > 分支不存在 > 悬空固定 > 有未提交修改 > 跟踪分支非快进。
func TestErrorPrecedenceAdjacentPairs(t *testing.T) {
	type setup struct {
		name string
		pair string
		run  func(t *testing.T) error
		want error
	}
	cases := []setup{
		{
			name: "参数非法>循环挂载",
			pair: "空路径 + 目标仓库是超级仓库自身",
			run: func(t *testing.T) error {
				store := NewStore()
				c := newSuper(t, store)
				return c.AddMount("", "S", "s0", "")
			},
			want: ErrInvalidParam,
		},
		{
			name: "循环挂载>路径冲突",
			pair: "目标仓库是超级仓库自身 + 路径与既有记录冲突",
			run: func(t *testing.T) error {
				store := NewStore()
				chainRepo(store, "A", "", "a1")
				c := newSuper(t, store, SubmoduleRecord{Path: "a", Repo: "A", Pinned: "a1"})
				return c.AddMount("a", "S", "s0", "")
			},
			want: ErrCycle,
		},
		{
			name: "路径冲突>仓库不存在",
			pair: "路径与既有记录冲突 + 目标仓库不存在",
			run: func(t *testing.T) error {
				store := NewStore()
				chainRepo(store, "A", "", "a1")
				c := newSuper(t, store, SubmoduleRecord{Path: "a", Repo: "A", Pinned: "a1"})
				return c.AddMount("a/b", "ghost-repo", "p1", "")
			},
			want: ErrPathConflict,
		},
		{
			name: "仓库不存在>分支不存在",
			pair: "记录1仓库缺失 + 记录2分支缺失",
			run: func(t *testing.T) error {
				store := NewStore()
				chainRepo(store, "Q", "dev", "q1")
				c := newSuper(t, store,
					SubmoduleRecord{Path: "m1", Repo: "ghost-repo", Pinned: "p1", Tracking: "main"},
					SubmoduleRecord{Path: "m2", Repo: "Q", Pinned: "q1", Tracking: "ghost"},
				)
				return c.Advance()
			},
			want: ErrRepoNotFound,
		},
		{
			name: "分支不存在>悬空固定",
			pair: "记录1分支缺失 + 记录2固定提交缺失",
			run: func(t *testing.T) error {
				store := NewStore()
				chainRepo(store, "Q", "dev", "q1")
				c := newSuper(t, store,
					SubmoduleRecord{Path: "m1", Repo: "Q", Pinned: "q1", Tracking: "ghost"},
					SubmoduleRecord{Path: "m2", Repo: "Q", Pinned: "q-missing", Tracking: "dev"},
				)
				return c.Advance()
			},
			want: ErrBranchNotFound,
		},
		{
			name: "悬空固定>有未提交修改",
			pair: "记录1固定提交缺失 + 记录2工作区脏",
			run: func(t *testing.T) error {
				store := NewStore()
				chainRepo(store, "Q", "", "q1")
				c := newSuper(t, store,
					SubmoduleRecord{Path: "m1", Repo: "Q", Pinned: "q-missing"},
					SubmoduleRecord{Path: "m2", Repo: "Q", Pinned: "q1"},
				)
				if err := c.SetCheckout("m2", "q1", true); err != nil {
					t.Fatal(err)
				}
				return c.Align()
			},
			want: ErrDanglingPin,
		},
		{
			name: "有未提交修改>跟踪分支非快进",
			pair: "记录1工作区脏(可快进) + 记录2非快进",
			run: func(t *testing.T) error {
				store := NewStore()
				chainRepo(store, "R", "main", "r1", "r2")
				chainRepo(store, "Q", "dev", "q1", "q2")
				store.Get("Q").Branches["dev"] = "q1" // q2 固定，dev 顶端 q1：非快进
				c := newSuper(t, store,
					SubmoduleRecord{Path: "m1", Repo: "R", Pinned: "r1", Tracking: "main"},
					SubmoduleRecord{Path: "m2", Repo: "Q", Pinned: "q2", Tracking: "dev"},
				)
				if err := c.SetCheckout("m1", "r1", true); err != nil {
					t.Fatal(err)
				}
				return c.Advance()
			},
			want: ErrDirty,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			t.Logf("场景=%s 实际输出=%v 判定依据=全局错误次序中 %s 更靠前", tc.pair, err, tc.name)
		})
	}
}

func TestAddMountDistinguishableErrors(t *testing.T) {
	store := NewStore()
	chainRepo(store, "A", "", "a1")
	c := newSuper(t, store, SubmoduleRecord{Path: "a", Repo: "A", Pinned: "a1"})
	if err := c.AddMount("b", "ghost", "p1", ""); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("目标仓库不存在, got %v", err)
	}
	t.Logf("输入=仓库 ghost 不存在 实际输出=ErrRepoNotFound 判定依据=仓库不存在可区分")
	if err := c.AddMount("b", "A", "a-missing", ""); !errors.Is(err, ErrDanglingPin) {
		t.Fatalf("固定提交不存在, got %v", err)
	}
	t.Logf("输入=提交 a-missing 不存在 实际输出=ErrDanglingPin 判定依据=固定提交不存在可区分")
	gen0 := c.Generation()
	if err := c.AddMount("b", "A", "a1", ""); err != nil {
		t.Fatalf("合法添加应成功, got %v", err)
	}
	if c.Generation() != gen0+1 {
		t.Fatalf("添加应产生一次变更")
	}
	if got := pinnedOf(t, c, "b"); got != "a1" {
		t.Fatalf("b 固定点=%q want a1", got)
	}
	t.Logf("输入=合法记录 实际输出=gen+1 且固定点落地 判定依据=添加产生一次子模块表变更")
}

// TestConcurrentAlignAdvanceSerialEquivalence 同一挂载点上并发的对齐与推进
// 不得交错出任何一方都未请求过的 (固定点, 检出) 组合。
func TestConcurrentAlignAdvanceSerialEquivalence(t *testing.T) {
	for i := 0; i < 100; i++ {
		store := NewStore()
		chainRepo(store, "R", "main", "r1", "r2")
		c := newSuper(t, store, SubmoduleRecord{Path: "m", Repo: "R", Pinned: "r1", Tracking: "main"})
		if err := c.SetCheckout("m", "r1", false); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var alignErr, advErr error
		wg.Add(2)
		go func() { defer wg.Done(); alignErr = c.Align() }()
		go func() { defer wg.Done(); advErr = c.Advance() }()
		wg.Wait()
		if alignErr != nil || advErr != nil {
			t.Fatalf("iter %d: align=%v advance=%v", i, alignErr, advErr)
		}
		pin := pinnedOf(t, c, "m")
		wt, _ := c.Checkout("m")
		// 串行结果只有两种：先对齐后推进 (pin=r2, co=r1)；先推进后对齐 (pin=r2, co=r2)。
		ok := (pin == "r2" && wt.Checkout == "r1") || (pin == "r2" && wt.Checkout == "r2")
		if !ok {
			t.Fatalf("iter %d: 出现非串行组合 pin=%q checkout=%q", i, pin, wt.Checkout)
		}
		if c.Generation() != 2 {
			t.Fatalf("iter %d: gen=%d want 2（两次成功变更各计一次）", i, c.Generation())
		}
	}
	t.Logf("输入=100 轮并发 Align+Advance 实际输出=每轮 (pin,checkout) 均为两种串行结果之一且 gen==2 判定依据=串行等价")
}

// TestConcurrentMixedOps 混合并发：成功变更数必须等于序号增量，状态始终可解析。
func TestConcurrentMixedOps(t *testing.T) {
	store := NewStore()
	chainRepo(store, "R", "main", "r1", "r2")
	chainRepo(store, "Q", "dev", "q1", "q2")
	c := newSuper(t, store,
		SubmoduleRecord{Path: "m", Repo: "R", Pinned: "r1", Tracking: "main"},
		SubmoduleRecord{Path: "n", Repo: "Q", Pinned: "q1", Tracking: "dev"},
	)
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	record := func(err error) {
		if err == nil {
			mu.Lock()
			successes++
			mu.Unlock()
		}
	}
	for i := 0; i < 8; i++ {
		wg.Add(4)
		go func() { defer wg.Done(); record(c.Align()) }()
		go func() { defer wg.Done(); record(c.Advance()) }()
		go func(i int) {
			defer wg.Done()
			record(c.AddMount(fmt.Sprintf("extra%d", i), "Q", "q1", ""))
		}(i)
		go func() { defer wg.Done(); _, _ = c.Statuses() }()
	}
	wg.Wait()
	if got := c.Generation(); int(got) != successes {
		t.Fatalf("gen=%d 与成功变更数=%d 不一致", got, successes)
	}
	st, err := c.Statuses()
	if err != nil {
		t.Fatalf("并发后状态应可解析: %v", err)
	}
	t.Logf("输入=8 组并发 Align/Advance/AddMount/Statuses 实际输出=gen=%d==成功数, 状态=%v 判定依据=并发等价于某串行顺序", c.Generation(), st)
}

// TestStatusCostIndependentOfMountCount 单挂载点状态判定开销与挂载点总数无关：
// 用解析计数器证明，而非依赖不稳定的耗时断言。
func TestStatusCostIndependentOfMountCount(t *testing.T) {
	counts := map[int]int64{}
	for _, n := range []int{10, 1000, 20000} {
		store := NewStore()
		chainRepo(store, "A", "", "a1")
		records := make([]SubmoduleRecord, 0, n)
		for i := 0; i < n; i++ {
			records = append(records, SubmoduleRecord{Path: fmt.Sprintf("m%06d", i), Repo: "A", Pinned: "a1"})
		}
		c := newSuper(t, store, records...)
		c.ResetStats()
		if _, err := c.Status("m000005"); err != nil {
			t.Fatalf("Status: %v", err)
		}
		scanned, _ := c.StatsSnapshot()
		counts[n] = scanned
		t.Logf("挂载点总数=%d 实际输出=记录探测数 %d 判定依据=探测数不随总数增长", n, scanned)
	}
	if counts[10] != counts[1000] || counts[1000] != counts[20000] {
		t.Fatalf("状态判定开销随挂载点总数增长: %v", counts)
	}
}

// TestCycleCheckCostIndependentOfBranchWidth 循环判定只走当前祖先链：
// 无关分支规模不影响探测数。
func TestCycleCheckCostIndependentOfBranchWidth(t *testing.T) {
	counts := map[int]int64{}
	for _, width := range []int{10, 1000, 20000} {
		store := NewStore()
		chainRepo(store, "A", "", "a1")
		chainRepo(store, "B", "", "b1")
		chainRepo(store, "X", "", "x1")
		tblB, err := NewTable(SubmoduleRecord{Path: "x", Repo: "X", Pinned: "x1"})
		if err != nil {
			t.Fatal(err)
		}
		store.Get("B").Commits["b1"].Table = tblB
		tblA, err := NewTable(SubmoduleRecord{Path: "b", Repo: "B", Pinned: "b1"})
		if err != nil {
			t.Fatal(err)
		}
		store.Get("A").Commits["a1"].Table = tblA
		records := []SubmoduleRecord{{Path: "a", Repo: "A", Pinned: "a1"}}
		for i := 0; i < width; i++ {
			records = append(records, SubmoduleRecord{Path: fmt.Sprintf("w%06d", i), Repo: "X", Pinned: "x1"})
		}
		c := newSuper(t, store, records...)
		c.ResetStats()
		if _, err := c.Status("a/b/x"); err != nil {
			t.Fatalf("Status: %v", err)
		}
		scanned, _ := c.StatsSnapshot()
		counts[width] = scanned
		t.Logf("无关分支规模=%d 实际输出=记录探测数 %d 判定依据=链式解析只探测当前路径", width, scanned)
	}
	if counts[10] != counts[1000] || counts[1000] != counts[20000] {
		t.Fatalf("循环/链解析开销随无关分支规模增长: %v", counts)
	}
}

func BenchmarkStatusSingleMount(b *testing.B) {
	for _, n := range []int{100, 10000} {
		b.Run(fmt.Sprintf("mounts=%d", n), func(b *testing.B) {
			store := NewStore()
			chainRepo(store, "A", "", "a1")
			records := make([]SubmoduleRecord, 0, n)
			for i := 0; i < n; i++ {
				records = append(records, SubmoduleRecord{Path: fmt.Sprintf("m%06d", i), Repo: "A", Pinned: "a1"})
			}
			tbl, err := NewTable(records...)
			if err != nil {
				b.Fatal(err)
			}
			chainRepo(store, "S", "", "s0")
			store.Get("S").Commits["s0"].Table = tbl
			c, err := NewCoordinator(store, "S", "s0")
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.Status("m000007"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
