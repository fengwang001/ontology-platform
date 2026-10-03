package fsmapper

import (
	"errors"
	"testing"
)

// 超长名在记号边界截断且不切断 %XX 与多字节 rune。
func TestTruncation(t *testing.T) {
	const mb = 16
	// 11a + ':' + 2b => 11+3+2=16，恰不截断。
	if got := baseMap("aaaaaaaaaaa:bb", mb); got != "aaaaaaaaaaa%3Abb" {
		t.Fatalf("len16 case = %q, want aaaaaaaaaaa%%3Abb", got)
	}
	s2 := "aaaaaaaaaaaaa:bb" // 13a + ':' + 2b，转义后 18
	h2 := fnv1a32Hex(escapeThenSteps(s2))
	if got := baseMap(s2, mb); got != "aaaaaaa~"+h2 {
		t.Fatalf("truncated = %q, want aaaaaaa~%s", got, h2)
	}
	// s3: 7a + ':' + 6b => 7+3+6=16，不截断；用 7b 才 17。
	s3 := "aaaaaaa:bbbbbbb" // 7a + ':' + 7b，转义后 17
	h3 := fnv1a32Hex(escapeThenSteps(s3))
	if got := baseMap(s3, mb); got != "aaaaaaa~"+h3 {
		t.Fatalf("token-boundary truncation = %q, want aaaaaaa~%s", got, h3)
	}
	// s4: 14a + 中文(3) = 17，前缀 7a。
	s4 := "aaaaaaaaaaaaaa中" // 16a+3=19
	got4 := baseMap(s4, mb)
	if len(got4) != mb || got4[:7] != "aaaaaaa" || got4[7] != '~' {
		t.Fatalf("multibyte truncation = %q (%d bytes)", got4, len(got4))
	}
	for _, s := range []string{s2, s3, s4} {
		if got := baseMap(s, mb); len(got) > mb {
			t.Fatalf("mapped %q exceeds MaxBytes", got)
		}
	}
}

func escapeThenSteps(src string) string {
	s := escapeStep(src)
	s = dotSpaceStep(s)
	s = reservedStep(s)
	return s
}

// 后缀候选自身超长时缩短 base；缩不下去报 ErrCannotFit。
func TestSuffixShortenBase(t *testing.T) {
	const mb = 16
	m, _ := New(mb, 4096, 100)
	mustMap(t, m, 0, "abcdefghijklmnop", false, "abcdefghijklmnop")
	mustMap(t, m, 0, "ABCDEFGHIJKLMNOP", false, "ABCDEFGHIJKLMN~2")
	mustMap(t, m, 0, "Abcdefghijklmnop", false, "Abcdefghijklmn~3")

	m2, _ := New(mb, 4096, 100)
	mustMap(t, m2, 0, "aaaaaaaaaaaaa:", false, "aaaaaaaaaaaaa%3A")
	// ~2 需缩到 14 字节：13 个 a 后 %3A 会使长度到 16，停在 13。
	mustMap(t, m2, 0, "AAAAAAAAAAAAA:", false, "AAAAAAAAAAAAA~2")

	// base 缩不下去：t0 为 "a." + 14 个 b（16 字节）。
	// 候选 ~2 需 2 字节后缀 + 15 字节 ext（含点）> 16，limit<0。
	m3, _ := New(mb, 4096, 100)
	srcA := "a.bbbbbbbbbbbbbb"
	mustMap(t, m3, 0, srcA, false, srcA)
	_, err := m3.Add(0, "A.bbbbbbbbbbbbbb", false)
	expectErr(t, err, ErrCannotFit)

	// base 最小记号 3 字节（%XX），而候选 limit 为 2 时任何记号都放不下。
	// 用 MaxBytes=16：src = "::" + 一个普通字符使 t0 恰 16，且拆分后
	// ext 为 11 字节 => limit=16-2-11=3 恰好能放一个 %XX；再把 ext
	// 增到 12 字节 limit=2 => 无记号可放。t0=6+1+12=19 会被第四步
	// 截断，故改用 src 直接产出 16 字节：base 部分用 %3A%3A%3A%20 之类。
	// 最直接：src 为 4 个 ':' + '.' + 4 个 'b'：t0=12+1+4=17 超限。
	// 取 3 个 ':' + '.' + 6 个 'b'：t0=9+1+6=16，ext 7 字节，
	// limit=16-2-7=7 可放两个 %XX，仍能缩。因此“最小记号放不下”
	// 的不可约情形等价于 limit<3：src = "::" + "...." 之类多占 ext。
	// src = "::" + '.' + 12 个 'b' => 9+1+12=22，第四步先截断，改变
	// 结构。故该边界由随机对照测试覆盖（小 MaxBytes、长后缀场景），
	// 此处仅保留 m3 的 ErrCannotFit 直接验证。
}

// 候选键与真实条目冲突的跳号；删除后复用与不漂移。
func TestNumberSkipAndStability(t *testing.T) {
	m, _ := New(255, 4096, 100)
	mustMap(t, m, 0, "Readme.md", false, "Readme.md")
	mustMap(t, m, 0, "README.md", false, "README~2.md")
	mustMap(t, m, 0, "readme.MD", false, "readme~3.MD")
	mustMap(t, m, 0, "readme~2.MD", false, "readme~4.MD")

	got, err := m.Names(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"README~2.md", "Readme.md", "readme~3.MD", "readme~4.MD"}
	if eq, i := stringSlicesEqual(sortedStrings(got), sortedStrings(want)); !eq {
		t.Fatalf("names mismatch at %d: got %v want %v", i, sortedStrings(got), sortedStrings(want))
	}

	// 编号键删除后可复用。
	if err := m.Remove(0, "README.md"); err != nil {
		t.Fatal(err)
	}
	mustMap(t, m, 0, "README~2.MD", false, "README~2.MD")

	// 规范复用例子：仅有 "Readme.md" 时删除再 Add "ReadMe.md"。
	m2, _ := New(255, 4096, 100)
	mustMap(t, m2, 0, "Readme.md", false, "Readme.md")
	if err := m2.Remove(0, "Readme.md"); err != nil {
		t.Fatal(err)
	}
	mustMap(t, m2, 0, "ReadMe.md", false, "ReadMe.md")
	got2, _ := m2.Names(0)
	if eq, i := stringSlicesEqual(sortedStrings(got2), []string{"ReadMe.md"}); !eq {
		t.Fatalf("reuse example mismatch at %d: got %v", i, got2)
	}

	got, _ = m.Names(0)
	want = []string{"README~2.MD", "Readme.md", "readme~3.MD", "readme~4.MD"}
	if eq, i := stringSlicesEqual(sortedStrings(got), sortedStrings(want)); !eq {
		t.Fatalf("after reuse names mismatch at %d: got %v want %v", i, sortedStrings(got), sortedStrings(want))
	}
}

func stringSlicesEqual(a, b []string) (bool, int) {
	if len(a) != len(b) {
		return false, -1
	}
	for i := range a {
		if a[i] != b[i] {
			return false, i
		}
	}
	return true, 0
}

// 改名先释放后分配及失败回滚。
func TestRenameSemantics(t *testing.T) {
	m, _ := New(255, 4096, 100)
	id := mustMap(t, m, 0, "Readme.md", false, "Readme.md")
	if err := m.Rename(0, "Readme.md", "readme.md"); err != nil {
		t.Fatal(err)
	}
	if got := mappedOf(m, id); got != "readme.md" {
		t.Fatalf("after rename = %q", got)
	}
	if _, _, err := m.Lookup(0, "Readme.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old lookup = %v, want ErrNotFound", err)
	}
	if err := m.Rename(0, "readme.md", "readme.md"); err != nil {
		t.Fatalf("noop rename = %v", err)
	}

	mustMap(t, m, 0, "other", false, "other")
	// 源名大小写敏感：OTHER 与 other 不同，可成功（映射键 other~2）。
	if err := m.Rename(0, "readme.md", "OTHER"); err != nil {
		t.Fatalf("rename to OTHER = %v", err)
	}
	if _, _, err := m.Lookup(0, "OTHER"); err != nil {
		t.Fatalf("lookup OTHER after rename = %v", err)
	}
	if got := mappedOf(m, id); got != "OTHER~2" {
		t.Fatalf("mapped OTHER = %q", got)
	}
	// 再改为已存在的源名 other => ErrExists。
	expectErr(t, m.Rename(0, "OTHER", "other"), ErrExists)
	expectErr(t, m.Rename(0, "nope", "x"), ErrNotFound)
	expectErr(t, m.Rename(0, "readme.md", "a/b"), ErrInvalidName)
	// 上面两次失败都不改状态；改名到不存在源名 readme.md 的错误是
	// NotFound（当前源名已是 OTHER）。
	if got := mappedOf(m, id); got != "OTHER~2" {
		t.Fatalf("state changed after failed rename: %q", got)
	}
}

// 目录改名的子树路径长度判定与回滚。
func TestRenameDirPathLengthRollback(t *testing.T) {
	m, _ := New(16, 24, 100)
	pj := mustMap(t, m, 0, "projects", true, "projects")
	rm := mustMap(t, m, pj, "readme.txt", false, "readme.txt")
	p, _ := m.Path(rm)
	if p != "projects/readme.txt" || len(p) != 19 {
		t.Fatalf("child path = %q len %d", p, len(p))
	}
	expectErr(t, m.Rename(0, "projects", "projects-archive"), ErrPathTooLong)
	if got := mappedOf(m, pj); got != "projects" {
		t.Fatalf("dir after rollback = %q", got)
	}
	if got := mappedOf(m, rm); got != "readme.txt" {
		t.Fatalf("child after rollback = %q", got)
	}
	if cp, _ := m.Path(rm); cp != "projects/readme.txt" {
		t.Fatalf("child path after rollback = %q", cp)
	}

	empty := mustMap(t, m, 0, "tmp", true, "tmp")
	if err := m.Rename(0, "tmp", "projects-archive"); err != nil {
		t.Fatalf("rename empty dir = %v", err)
	}
	if cp, _ := m.Path(empty); cp != "projects-archive" || len(cp) != 16 {
		t.Fatalf("renamed empty dir path = %q", cp)
	}

	// Add 深层目录：projects(8)+1+12=21 <= 24，可建；其下加文件
	// 21+1+10=32 > 24，被拒。
	deep := mustMap(t, m, pj, "subdir123456", true, "subdir123456")
	_, err := m.Add(deep, "readme.txt", false)
	expectErr(t, err, ErrPathTooLong)
	// 被拒后目录仍为空，可删除。
	if err := m.Remove(pj, "subdir123456"); err != nil {
		t.Fatalf("remove after rejected add: %v", err)
	}
}

// 删除非空目录与不存在父。
func TestRemoveRules(t *testing.T) {
	m, _ := New(16, 256, 100)
	d := mustMap(t, m, 0, "d", true, "d")
	mustMap(t, m, d, "f", false, "f")
	expectErr(t, m.Remove(0, "d"), ErrNotEmpty)
	expectErr(t, m.Remove(0, "missing"), ErrNotFound)
	expectErr(t, m.Remove(999, "f"), ErrNoParent)
	if err := m.Remove(d, "f"); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(0, "d"); err != nil {
		t.Fatalf("remove now-empty dir: %v", err)
	}
}

// 错误优先级。
func TestErrorPriority(t *testing.T) {
	m, _ := New(16, 24, 1)
	_, err := m.Add(999, "a/b", false)
	expectErr(t, err, ErrInvalidName)
	_, err = m.Add(999, "ok", false)
	expectErr(t, err, ErrNoParent)
	mustMap(t, m, 0, "f", false, "f")
	_, err = m.Add(0, "f", false)
	expectErr(t, err, ErrExists)
	_, err = m.Add(0, "g", false)
	expectErr(t, err, ErrFull)

	// Rename 优先级：InvalidName > NotFound > noop > Exists > CannotFit > PathTooLong。
	expectErr(t, m.Rename(0, "f", "a/b"), ErrInvalidName)
	expectErr(t, m.Rename(0, "nope", "a/b"), ErrInvalidName)
	expectErr(t, m.Rename(0, "nope", "x"), ErrNotFound)
	m2, _ := New(16, 256, 100)
	mustMap(t, m2, 0, "f", false, "f")
	mustMap(t, m2, 0, "g", false, "g")
	if err := m2.Rename(0, "f", "f"); err != nil {
		t.Fatalf("noop = %v", err)
	}
	expectErr(t, m2.Rename(0, "f", "g"), ErrExists)

	if _, err := NamesOf(m, 999); !errors.Is(err, ErrNoParent) {
		t.Fatalf("Names missing parent = %v", err)
	}
	if _, err := m.Path(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Path missing id = %v", err)
	}
}

// NamesOf 小辅助，测试中直接用 Names 即可，保留避免未使用导入。
func NamesOf(m *Mapper, parent int64) ([]string, error) { return m.Names(parent) }
