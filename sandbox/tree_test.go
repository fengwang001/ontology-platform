package sandbox

import (
	"errors"
	"fmt"
	"testing"
)

func mustOp(t *testing.T, desc string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", desc, err)
	}
}

func wantErr(t *testing.T, desc string, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: want %v, got %v", desc, want, got)
	}
}

// TestDotDotAfterSymlink verifies physical ".." semantics: when a link
// points at x/y, walking through the link and then ".." lands at x, not at
// the link's own directory.
func TestDotDotAfterSymlink(t *testing.T) {
	tree := New()
	mustOp(t, "mkdir x", tree.Mkdir("x"))
	mustOp(t, "mkdir x/y", tree.Mkdir("x/y"))
	mustOp(t, "mkdir a", tree.Mkdir("a"))

	// Absolute target variant: the link lives anywhere, target /x/y.
	mustOp(t, "symlink a/link -> /x/y", tree.Symlink("/x/y", "a/link"))
	res, err := tree.Resolve("a/link/..", true)
	mustOp(t, "resolve a/link/..", err)
	t.Logf("input=a/link/.. linkTarget=/x/y -> output=%q kind=%v 依据: 进入 /x/y 后 .. 回到物理父目录 /x",
		res.Path, res.Node.Kind())
	if res.Path != "/x" || !res.Node.IsDir() {
		t.Fatalf("want /x directory, got %q", res.Path)
	}

	// Relative target variant: a link in x pointing at x/y (target "y"),
	// so x/rel/.. must also resolve to x.
	mustOp(t, "symlink x/rel -> y", tree.Symlink("y", "x/rel"))
	res2, err := tree.Resolve("x/rel/..", true)
	mustOp(t, "resolve x/rel/..", err)
	t.Logf("input=x/rel/.. linkTarget=y -> output=%q 依据: 相对目标相对链接所在目录展开为 /x/y，.. 回到 /x",
		res2.Path)
	if res2.Path != "/x" {
		t.Fatalf("want /x, got %q", res2.Path)
	}
}

// TestDotDotAtRoot checks that ".." cannot escape the sandbox root.
func TestDotDotAtRoot(t *testing.T) {
	tree := New()
	mustOp(t, "mkdir a", tree.Mkdir("a"))
	mustOp(t, "mkdir b", tree.Mkdir("b"))
	for _, p := range []string{"/..", "/../../..", "a/../..", "/a/../../b/../.."} {
		res, err := tree.Resolve(p, true)
		mustOp(t, "resolve "+p, err)
		t.Logf("input=%q -> output=%q 依据: 根处 .. 停在沙箱根，不得越出根", p, res.Path)
		if res.Path != "/" {
			t.Fatalf("%s: want /, got %q", p, res.Path)
		}
	}

	// A link targeting /../../etc cannot escape: absolute targets are
	// rooted at the sandbox root and ".." is clamped there.
	mustOp(t, "symlink escape -> /../../etc", tree.Symlink("/../../etc", "escape"))
	_, err := tree.Resolve("escape", true)
	wantErr(t, "resolve escape", err, ErrNotExist)
	t.Logf("input=escape linkTarget=/../../etc -> output=ErrNotExist 依据: 绝对目标以沙箱根为起点，.. 在根处被钳制，etc 不存在")
}

// TestAbsoluteLinkStaysInsideSandbox builds an absolute link and confirms
// resolution stays inside the sandbox.
func TestAbsoluteLinkStaysInsideSandbox(t *testing.T) {
	tree := New()
	mustOp(t, "mkdir deep", tree.Mkdir("deep"))
	mustOp(t, "mkdir deep/dir", tree.Mkdir("deep/dir"))
	mustOp(t, "symlink rootlink -> /deep/dir", tree.Symlink("/deep/dir", "rootlink"))
	res, err := tree.Resolve("rootlink/../..", true)
	mustOp(t, "resolve rootlink/../..", err)
	t.Logf("input=rootlink/../.. linkTarget=/deep/dir -> output=%q 依据: 链接展开后位于 /deep/dir，两级 .. 回到 /",
		res.Path)
	if res.Path != "/" {
		t.Fatalf("want /, got %q", res.Path)
	}
}

// TestSymlinkCycles covers self-referential and mutually referential links.
func TestSymlinkCycles(t *testing.T) {
	tree := New()
	mustOp(t, "symlink self -> self", tree.Symlink("self", "self"))

	_, err := tree.Resolve("self", true)
	wantErr(t, "resolve self", err, ErrTooManyLinks)
	t.Logf("input=self followFinal=true -> output=ErrTooManyLinks 依据: 自指链接每次跟随计数+1，第 41 次超过上限")

	// lstat-style resolution must not follow, so no cycle error.
	res, err := tree.Resolve("self", false)
	mustOp(t, "lstat self", err)
	t.Logf("input=self followFinal=false -> output=%q kind=%v 依据: 末段不跟随，直接返回链接节点",
		res.Path, res.Node.Kind())
	if res.Node.Kind() != KindSymlink {
		t.Fatalf("want symlink node, got %v", res.Node.Kind())
	}

	mustOp(t, "symlink a -> b", tree.Symlink("b", "a"))
	mustOp(t, "symlink b -> a", tree.Symlink("a", "b"))
	_, err = tree.Resolve("a", true)
	wantErr(t, "resolve mutual a", err, ErrTooManyLinks)
	_, err = tree.Resolve("b/x", true)
	wantErr(t, "resolve mutual b/x", err, ErrTooManyLinks)
	t.Logf("input=a(->b->a...) / b/x(->a->b...) -> output=ErrTooManyLinks 依据: 互指链接累计跟随后超限")
}

// TestFollowLimitBoundary builds a chain of exactly 40 links ending at a
// directory and verifies 40 follows succeed while a 41st fails.
func TestFollowLimitBoundary(t *testing.T) {
	tree := New()
	mustOp(t, "mkdir target", tree.Mkdir("target"))

	// Chain: l0 -> l1 -> ... -> l39 -> /target  (40 links).
	for i := 0; i < 40; i++ {
		target := fmt.Sprintf("l%d", i+1)
		if i == 39 {
			target = "/target"
		}
		mustOp(t, fmt.Sprintf("symlink l%d -> %s", i, target),
			tree.Symlink(target, fmt.Sprintf("l%d", i)))
	}

	res, err := tree.Resolve("l0", true)
	mustOp(t, "resolve chain of 40", err)
	t.Logf("input=l0 (40 个链接 -> /target) -> output=%q 依据: 恰好跟随 40 次，未超限", res.Path)
	if res.Path != "/target" {
		t.Fatalf("want /target, got %q", res.Path)
	}

	// One more link at the front => 41 follows.
	mustOp(t, "symlink start -> l0", tree.Symlink("l0", "start"))
	_, err = tree.Resolve("start", true)
	wantErr(t, "resolve chain of 41", err, ErrTooManyLinks)
	t.Logf("input=start (41 个链接) -> output=ErrTooManyLinks 依据: 单次解析累计跟随 41 次 > 40")
}

// TestErrorsDistinguishable checks each rejection reason and that rejected
// operations do not mutate the tree.
func TestErrorsDistinguishable(t *testing.T) {
	tree := New()
	mustOp(t, "mkdir d", tree.Mkdir("d"))
	mustOp(t, "create d/f", tree.CreateFile("d/f"))
	mustOp(t, "mkdir d/sub", tree.Mkdir("d/sub"))
	mustOp(t, "create d/sub/g", tree.CreateFile("d/sub/g"))

	cases := []struct {
		name string
		fn   func() error
		want error
		why  string
	}{
		{"empty path resolve", func() error { _, e := tree.Resolve("", true); return e }, ErrEmptyPath, "路径为空"},
		{"empty path mkdir", func() error { return tree.Mkdir("") }, ErrEmptyPath, "路径为空"},
		{"empty final name", func() error { return tree.Mkdir("/") }, ErrInvalidName, "路径没有名字段，名字为空"},
		{"intermediate not directory", func() error { return tree.Mkdir("d/f/x") }, ErrNotDirectory, "中间段 d/f 是文件"},
		{"missing parent segment", func() error { return tree.Mkdir("nope/x") }, ErrNotExist, "父段 nope 不存在"},
		{"resolve missing final", func() error { _, e := tree.Resolve("d/missing", true); return e }, ErrNotExist, "末段不存在"},
		{"mkdir existing", func() error { return tree.Mkdir("d") }, ErrExists, "名字已存在"},
		{"create existing", func() error { return tree.CreateFile("d/f") }, ErrExists, "名字已存在"},
		{"symlink existing", func() error { return tree.Symlink("/d", "d/f") }, ErrExists, "名字已存在"},
		{"remove nonempty dir", func() error { return tree.Remove("d/sub") }, ErrDirectoryNotEmpty, "目录非空"},
		{"symlink empty target", func() error { return tree.Symlink("", "ln") }, ErrInvalidArgument, "链接目标为空"},
		{"create under missing dir", func() error { return tree.CreateFile("x/y") }, ErrNotExist, "父目录不存在"},
		{"remove missing", func() error { return tree.Remove("d/none") }, ErrNotExist, "删除目标不存在"},
		{"rename missing source", func() error { return tree.Rename("d/none", "z") }, ErrNotExist, "改名源不存在"},
		{"rename through file", func() error { return tree.Rename("d/f/x", "z") }, ErrNotDirectory, "源路径中间段是文件"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			wantErr(t, tc.name, err, tc.want)
			t.Logf("输入=%s -> output=%v 依据: %s", tc.name, err, tc.why)
		})
	}

	// Tree unchanged after rejections.
	res, err := tree.Resolve("d/sub/g", false)
	mustOp(t, "post-rejection resolve", err)
	if res.Path != "/d/sub/g" || res.Node.Kind() != KindFile {
		t.Fatalf("tree mutated by rejected operations: %q %v", res.Path, res.Node.Kind())
	}
	t.Logf("拒绝操作后解析 d/sub/g -> %q 依据: 所有拒绝在校验阶段返回，未改动树", res.Path)
}

// TestRenameRules covers the directory-into-descendant rejection and basic
// moves.
func TestRenameRules(t *testing.T) {
	tree := New()
	mustOp(t, "mkdir a", tree.Mkdir("a"))
	mustOp(t, "mkdir a/b", tree.Mkdir("a/b"))
	mustOp(t, "mkdir a/b/c", tree.Mkdir("a/b/c"))

	wantErr(t, "rename a into a/b/a", tree.Rename("a", "a/b/a"), ErrRenameIntoSelf)
	t.Logf("rename a -> a/b/a : ErrRenameIntoSelf 依据: 目录不能移入自身子孙之下")
	wantErr(t, "rename a into itself", tree.Rename("a", "a"), ErrRenameIntoSelf)
	t.Logf("rename a -> a : ErrRenameIntoSelf 依据: 目录不能移到自身")
	wantErr(t, "rename a/b under a/b/c", tree.Rename("a/b", "a/b/c/b"), ErrRenameIntoSelf)
	t.Logf("rename a/b -> a/b/c/b : ErrRenameIntoSelf 依据: 目标父目录在源子树内")

	// Rejected rename leaves structure intact.
	_, err := tree.Resolve("a/b/c", true)
	mustOp(t, "c still present", err)

	// Legal rename: move c next to a.
	mustOp(t, "rename a/b/c -> c", tree.Rename("a/b/c", "c"))
	res, err := tree.Resolve("c", true)
	mustOp(t, "resolve moved c", err)
	t.Logf("rename a/b/c -> c 后解析 c -> %q 依据: 合法移动，节点保留物理位置", res.Path)
	if res.Path != "/c" {
		t.Fatalf("want /c, got %q", res.Path)
	}
	if _, err := tree.Resolve("a/b/c", true); !errors.Is(err, ErrNotExist) {
		t.Fatalf("old path should be gone, got %v", err)
	}

	// Renaming a file onto itself is a no-op.
	mustOp(t, "create f", tree.CreateFile("f"))
	mustOp(t, "rename f -> f", tree.Rename("f", "f"))
	t.Logf("rename f -> f : 成功(no-op) 依据: 文件同名移动无副作用")

	// Rename onto an existing name is rejected.
	mustOp(t, "create g", tree.CreateFile("g"))
	wantErr(t, "rename f onto g", tree.Rename("f", "g"), ErrExists)
	t.Logf("rename f -> g : ErrExists 依据: 目标名字已存在，整体拒绝")
}

// TestRepeatability resolves the same path repeatedly and demands identical
// results when the tree is quiescent.
func TestRepeatability(t *testing.T) {
	tree := New()
	mustOp(t, "mkdir x", tree.Mkdir("x"))
	mustOp(t, "mkdir x/y", tree.Mkdir("x/y"))
	mustOp(t, "symlink l -> /x/y", tree.Symlink("/x/y", "l"))

	first, err := tree.Resolve("l/..", true)
	mustOp(t, "first resolve", err)
	for i := 0; i < 20; i++ {
		got, err := tree.Resolve("l/..", true)
		mustOp(t, "repeat resolve", err)
		if got.Path != first.Path || got.Node != first.Node {
			t.Fatalf("iteration %d: got %q, want %q", i, got.Path, first.Path)
		}
	}
	t.Logf("同一棵树同一路径 l/.. 重复解析 21 次均为 %q 依据: 快照式解析，树不变则结果不变", first.Path)
}
