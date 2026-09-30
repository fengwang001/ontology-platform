package vfs

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// must 辅助：失败即终止并打印操作。
func must(t *testing.T, op string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 意外失败: %v", op, err)
	}
	t.Logf("操作 %s -> 成功", op)
}

// 搭建 /x/y（含文件 f）与 /a/link -> /x/y 的基本树。
func buildBasicTree(t *testing.T) *Tree {
	t.Helper()
	tr := NewTree()
	must(t, "Mkdir / x", tr.Mkdir("/", "x"))
	must(t, "Mkdir /x y", tr.Mkdir("/x", "y"))
	must(t, "CreateFile /x/y f", tr.CreateFile("/x/y", "f"))
	must(t, "Mkdir / a", tr.Mkdir("/", "a"))
	must(t, "Symlink /a link -> /x/y", tr.Symlink("/a", "link", "/x/y"))
	return tr
}

// 判定依据：符号链接按物理语义解析，经过链接后的 ".." 回到链接目标的父目录。
func TestDotDotThroughSymlink(t *testing.T) {
	tr := buildBasicTree(t)

	entry, err := tr.Resolve("/a/link/..", true)
	t.Logf("输入: Resolve(%q, followLast=true)", "/a/link/..")
	t.Logf("输出: entry=%+v err=%v", entry, err)
	t.Logf("判定依据: link 指向 /x/y，物理 .. 应落在链接目标的父目录 /x，而非链接所在目录 /a")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if entry.Kind != Dir || entry.Path != "/x" {
		t.Fatalf("期望 {dir /x}，得到 %+v", entry)
	}

	entry, err = tr.Resolve("/a/link/../..", true)
	t.Logf("输入: Resolve(%q, followLast=true)", "/a/link/../..")
	t.Logf("输出: entry=%+v err=%v", entry, err)
	t.Logf("判定依据: /x/y 向上两级是根 /")
	if err != nil || entry.Path != "/" {
		t.Fatalf("期望根目录，得到 %+v err=%v", entry, err)
	}
}

// 判定依据：根处的 ".." 停在根，多个 ".." 越过根也停在根。
func TestDotDotPastRoot(t *testing.T) {
	tr := buildBasicTree(t)
	for _, in := range []string{"/..", "/../../..", "/x/../../..", "/./..//..//"} {
		entry, err := tr.Resolve(in, true)
		t.Logf("输入: Resolve(%q, true) -> 输出: %+v err=%v", in, entry, err)
		if err != nil {
			t.Fatalf("Resolve(%q) 失败: %v", in, err)
		}
		if entry.Path != "/" || entry.Kind != Dir {
			t.Fatalf("Resolve(%q) 期望停在根，得到 %+v", in, entry)
		}
	}
	t.Log("判定依据: 所有越过根的 .. 均停在根，结果 Path 恒为 /")
}

// 判定依据：以斜杠开头的链接目标以沙箱根为起点，解析结果不会越出根目录。
func TestAbsoluteSymlinkStaysInSandbox(t *testing.T) {
	tr := buildBasicTree(t)
	must(t, "Symlink / abs -> /x/y", tr.Symlink("/", "abs", "/x/y"))
	must(t, "Symlink / esc -> /../../../x/y", tr.Symlink("/", "esc", "/../../../x/y"))

	entry, err := tr.Resolve("/abs/f", true)
	t.Logf("输入: Resolve(%q, true) -> 输出: %+v err=%v", "/abs/f", entry, err)
	t.Logf("判定依据: 绝对目标 /x/y 以沙箱根为起点，/abs/f 应解析到 /x/y/f")
	if err != nil || entry.Path != "/x/y/f" || entry.Kind != File {
		t.Fatalf("期望 {file /x/y/f}，得到 %+v err=%v", entry, err)
	}

	entry, err = tr.Resolve("/esc", true)
	t.Logf("输入: Resolve(%q, true) -> 输出: %+v err=%v", "/esc", entry, err)
	t.Logf("判定依据: 目标中的 .. 越过根时停在根，/esc 应解析到沙箱内 /x/y")
	if err != nil || entry.Path != "/x/y" {
		t.Fatalf("期望 {dir /x/y}，得到 %+v err=%v", entry, err)
	}
}

// 判定依据：相对链接目标相对于链接所在目录解析。
func TestRelativeSymlinkTarget(t *testing.T) {
	tr := buildBasicTree(t)
	must(t, "Symlink /x rel -> y/f", tr.Symlink("/x", "rel", "y/f"))
	must(t, "Symlink /x/y up -> ../rel", tr.Symlink("/x/y", "up", "../rel"))

	entry, err := tr.Resolve("/x/rel", true)
	t.Logf("输入: Resolve(%q, true) -> 输出: %+v err=%v", "/x/rel", entry, err)
	if err != nil || entry.Path != "/x/y/f" {
		t.Fatalf("期望 {file /x/y/f}，得到 %+v err=%v", entry, err)
	}

	entry, err = tr.Resolve("/x/y/up", true)
	t.Logf("输入: Resolve(%q, true) -> 输出: %+v err=%v", "/x/y/up", entry, err)
	t.Logf("判定依据: ../rel 相对于 /x/y 解析为 /x/rel，再跟随到 /x/y/f")
	if err != nil || entry.Path != "/x/y/f" {
		t.Fatalf("期望 {file /x/y/f}，得到 %+v err=%v", entry, err)
	}
}

// 判定依据：末段链接是否跟随由调用方指定；中间段链接总是跟随。
func TestFollowLastFlag(t *testing.T) {
	tr := buildBasicTree(t)

	entry, err := tr.Resolve("/a/link", false)
	t.Logf("输入: Resolve(%q, followLast=false) -> 输出: %+v err=%v", "/a/link", entry, err)
	if err != nil || entry.Kind != Symlink || entry.Target != "/x/y" || entry.Path != "/a/link" {
		t.Fatalf("期望未跟随的链接自身，得到 %+v err=%v", entry, err)
	}

	entry, err = tr.Resolve("/a/link", true)
	t.Logf("输入: Resolve(%q, followLast=true) -> 输出: %+v err=%v", "/a/link", entry, err)
	if err != nil || entry.Kind != Dir || entry.Path != "/x/y" {
		t.Fatalf("期望跟随到 /x/y，得到 %+v err=%v", entry, err)
	}

	entry, err = tr.Resolve("/a/link/f", false)
	t.Logf("输入: Resolve(%q, followLast=false) -> 输出: %+v err=%v", "/a/link/f", entry, err)
	t.Logf("判定依据: 中间段 link 总是跟随，末段 f 是普通文件")
	if err != nil || entry.Path != "/x/y/f" {
		t.Fatalf("期望 {file /x/y/f}，得到 %+v err=%v", entry, err)
	}
}

// 判定依据：自指与互指链接在累计跟随超过 40 次后判为循环。
func TestCyclicLinks(t *testing.T) {
	tr := NewTree()
	must(t, "Symlink / self -> /self", tr.Symlink("/", "self", "/self"))
	must(t, "Symlink / ping -> /pong", tr.Symlink("/", "ping", "/pong"))
	must(t, "Symlink / pong -> /ping", tr.Symlink("/", "pong", "/ping"))

	for _, in := range []string{"/self", "/ping", "/pong"} {
		_, err := tr.Resolve(in, true)
		t.Logf("输入: Resolve(%q, true) -> 输出: err=%v", in, err)
		if !errors.Is(err, ErrTooManyLinks) {
			t.Fatalf("Resolve(%q) 期望 ErrTooManyLinks，得到 %v", in, err)
		}
	}
	t.Log("判定依据: 自指与互指链接均触发跟随上限，报 ErrTooManyLinks")
}

// 判定依据：单次解析累计跟随恰好 40 次成功，第 41 次失败。
func TestFollowLimitBoundary(t *testing.T) {
	tr := NewTree()
	must(t, "CreateFile / real", tr.CreateFile("/", "real"))
	// 链 l1 -> l2 -> ... -> l41 -> /real
	for i := 41; i >= 1; i-- {
		target := fmt.Sprintf("/l%d", i+1)
		if i == 41 {
			target = "/real"
		}
		must(t, fmt.Sprintf("Symlink / l%d -> %s", i, target), tr.Symlink("/", fmt.Sprintf("l%d", i), target))
	}

	entry, err := tr.Resolve("/l2", true)
	t.Logf("输入: Resolve(%q, true) -> 输出: %+v err=%v", "/l2", entry, err)
	t.Logf("判定依据: l2..l41 共 40 次跟随，恰好等于上限，应成功")
	if err != nil || entry.Path != "/real" {
		t.Fatalf("40 次跟随应成功，得到 %+v err=%v", entry, err)
	}

	_, err = tr.Resolve("/l1", true)
	t.Logf("输入: Resolve(%q, true) -> 输出: err=%v", "/l1", err)
	t.Logf("判定依据: l1..l41 共 41 次跟随，超过上限，应失败")
	if !errors.Is(err, ErrTooManyLinks) {
		t.Fatalf("41 次跟随应报 ErrTooManyLinks，得到 %v", err)
	}
}

// 判定依据：把目录移到其自身或子孙之下必须整体拒绝，且树不变。
func TestMoveIntoDescendantRejected(t *testing.T) {
	tr := NewTree()
	must(t, "Mkdir / d", tr.Mkdir("/", "d"))
	must(t, "Mkdir /d sub", tr.Mkdir("/d", "sub"))
	must(t, "CreateFile /d/sub f", tr.CreateFile("/d/sub", "f"))

	cases := []struct{ src, dstDir, name string }{
		{"/d", "/d", "renamed"}, // 移入自身
		{"/d", "/d/sub", "d"},   // 移入子孙
		{"/d", "/d/sub", "sub"}, // 移入子孙且名字冲突
	}
	for _, c := range cases {
		err := tr.Rename(c.src, c.dstDir, c.name)
		t.Logf("输入: Rename(%q, %q, %q) -> 输出: err=%v", c.src, c.dstDir, c.name, err)
		if !errors.Is(err, ErrMoveIntoSelf) && !errors.Is(err, ErrExist) {
			t.Fatalf("期望 ErrMoveIntoSelf/ErrExist，得到 %v", err)
		}
	}
	t.Log("判定依据: 目标目录位于源目录子树内，拒绝且树保持原状")

	entry, err := tr.Resolve("/d/sub/f", true)
	t.Logf("校验: Resolve(%q, true) -> %+v err=%v", "/d/sub/f", entry, err)
	if err != nil || entry.Path != "/d/sub/f" {
		t.Fatalf("被拒绝的改名改变了树: %+v err=%v", entry, err)
	}
}

// 判定依据：各类非法操作整体拒绝并给出可区分原因，且树不变。
func TestRejections(t *testing.T) {
	tr := buildBasicTree(t)

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"空路径", func() error { _, err := tr.Resolve("", true); return err }, ErrEmptyPath},
		{"中间段不是目录", func() error { _, err := tr.Resolve("/x/y/f/g", true); return err }, ErrNotDir},
		{"段不存在", func() error { _, err := tr.Resolve("/x/nope", true); return err }, ErrNotExist},
		{"链接目标不存在", func() error { _, err := tr.Resolve("/a/link/nope", true); return err }, ErrNotExist},
		{"名字为空", func() error { return tr.Mkdir("/", "") }, ErrInvalidName},
		{"名字含斜杠", func() error { return tr.CreateFile("/", "a/b") }, ErrInvalidName},
		{"名字为点", func() error { return tr.Mkdir("/", ".") }, ErrInvalidName},
		{"名字为点点", func() error { return tr.Mkdir("/", "..") }, ErrInvalidName},
		{"创建已存在", func() error { return tr.Mkdir("/", "x") }, ErrExist},
		{"删除非空目录", func() error { return tr.Remove("/x") }, ErrDirNotEmpty},
		{"删除根目录", func() error { return tr.Remove("/") }, ErrRoot},
		{"父路径不是目录", func() error { return tr.Mkdir("/x/y/f", "g") }, ErrNotDir},
		{"改名目标已存在", func() error { return tr.Rename("/x", "/", "a") }, ErrExist},
	}
	for _, c := range cases {
		err := c.op()
		t.Logf("输入: %s -> 输出: err=%v (期望 %v)", c.name, err, c.want)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: 期望 %v，得到 %v", c.name, c.want, err)
		}
	}
	t.Log("判定依据: 每种拒绝都有独立的哨兵错误，可用 errors.Is 区分")

	entry, err := tr.Resolve("/a/link/f", true)
	t.Logf("校验: Resolve(%q, true) -> %+v err=%v", "/a/link/f", entry, err)
	if err != nil || entry.Path != "/x/y/f" {
		t.Fatalf("被拒绝的操作改变了树: %+v err=%v", entry, err)
	}
}

// 判定依据：同一棵树与同一路径反复解析结果完全相同。
func TestDeterministicResolve(t *testing.T) {
	tr := buildBasicTree(t)
	first, err := tr.Resolve("/a/link/../y/f", true)
	must(t, "Resolve /a/link/../y/f", err)
	for i := 0; i < 100; i++ {
		got, err := tr.Resolve("/a/link/../y/f", true)
		if err != nil || got != first {
			t.Fatalf("第 %d 次解析结果漂移: %+v err=%v，首次为 %+v", i, got, err, first)
		}
	}
	t.Logf("输入: Resolve(%q, true) x100 -> 输出恒定: %+v", "/a/link/../y/f", first)
}

// 判定依据：并发改名与解析被串行化，每次解析结果等于改名前或改名后
// 之一的串行结果，绝不解析出任何时刻都不存在的位置。
func TestConcurrentRenameAndResolve(t *testing.T) {
	tr := NewTree()
	must(t, "Mkdir / a", tr.Mkdir("/", "a"))
	must(t, "CreateFile /a f", tr.CreateFile("/a", "f"))

	const workers = 8
	const rounds = 500
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 改名方：/a <-> /b 来回移动。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if err := tr.Rename("/a", "/", "b"); err != nil {
				t.Errorf("Rename /a -> /b: %v", err)
				return
			}
			if err := tr.Rename("/b", "/", "a"); err != nil {
				t.Errorf("Rename /b -> /a: %v", err)
				return
			}
		}
		close(stop)
	}()

	// 解析方：结果只能是 {file /a/f}、{file /b/f} 或 ErrNotExist 之一，
	// 且路径必须与查询时某一时刻的真实位置一致。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, p := range []string{"/a/f", "/b/f"} {
					entry, err := tr.Resolve(p, true)
					if errors.Is(err, ErrNotExist) {
						continue // 改名另一瞬间：该位置不存在，合法
					}
					if err != nil {
						t.Errorf("Resolve(%q) 出现非 NotExist 错误: %v", p, err)
						return
					}
					if entry.Kind != File || entry.Path != p {
						t.Errorf("Resolve(%q) 解析出不一致位置: %+v", p, entry)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("输入: %d 轮 Rename(/a<->/b) 与 %d 路并发 Resolve(/a/f, /b/f)", rounds, workers)
	t.Log("输出: 无数据竞争，无解析到不存在位置的报告")
	t.Log("判定依据: 每次解析在锁内完成，结果等于改名前或改名后之一的串行结果")
}
