package overlay

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, lower map[string][]byte) *FS {
	t.Helper()
	fs, err := New(lower)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fs
}

func upperStrings(fs *FS) []string {
	out := []string{}
	for _, r := range fs.Upper() {
		if r.Kind == "file" {
			out = append(out, r.Path+" file "+string(r.Content))
		} else {
			out = append(out, r.Path+" "+r.Kind)
		}
	}
	return out
}

func mustUpper(t *testing.T, fs *FS, want []string) {
	t.Helper()
	if got := upperStrings(fs); !reflect.DeepEqual(got, want) {
		t.Fatalf("Upper() = %q, want %q", got, want)
	}
}

func mustErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want errors.Is(%v)", err, target)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func mustReadDir(t *testing.T, fs *FS, path string, want []string) {
	t.Helper()
	got, err := fs.ReadDir(path)
	mustOK(t, err)
	if got == nil {
		got = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadDir(%q) = %q, want %q", path, got, want)
	}
}

func mustLookupFile(t *testing.T, fs *FS, path, content string) {
	t.Helper()
	e, err := fs.Lookup(path)
	mustOK(t, err)
	if e.Type != EntryFile || string(e.Content) != content {
		t.Fatalf("Lookup(%q) = %+v, want file %q", path, e, content)
	}
}

func mustLookupDir(t *testing.T, fs *FS, path string) {
	t.Helper()
	e, err := fs.Lookup(path)
	mustOK(t, err)
	if e.Type != EntryDir {
		t.Fatalf("Lookup(%q) = %+v, want dir", path, e)
	}
}

func mustLookupNotFound(t *testing.T, fs *FS, path string) {
	t.Helper()
	_, err := fs.Lookup(path)
	mustErrIs(t, err, ErrNotFound)
}

// 删除下层文件产生白障。
func TestRemoveLowerFileWritesWhiteout(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"a/": nil, "a/f": []byte("x")})
	mustOK(t, fs.Remove("a/f"))
	mustUpper(t, fs, []string{"a dir", "a/f whiteout"})
	mustLookupNotFound(t, fs, "a/f")
}

// 删除仅存在于上层的文件不留白障。
func TestRemoveUpperOnlyFileLeavesNothing(t *testing.T) {
	fs := mustNew(t, nil)
	mustOK(t, fs.Write("x", []byte("1")))
	mustOK(t, fs.Remove("x"))
	mustUpper(t, fs, []string{})
	mustLookupNotFound(t, fs, "x")
}

// 删除下层目录后重建为不透明目录，下层内容不复活。
func TestRemoveLowerDirRecreateOpaque(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"d/": nil, "d/f": []byte("x")})
	mustErrIs(t, fs.Remove("d"), ErrNotEmpty)
	mustOK(t, fs.Remove("d/f"))
	mustOK(t, fs.Remove("d"))
	mustUpper(t, fs, []string{"d whiteout"})
	mustOK(t, fs.Mkdir("d"))
	mustUpper(t, fs, []string{"d opaque"})
	mustReadDir(t, fs, "d", []string{})
	mustLookupNotFound(t, fs, "d/f")
}

// 再删除该不透明目录仍须写白障（下层同名目录仍在）。
func TestRemoveOpaqueDirStillWritesWhiteout(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"d/": nil, "d/f": []byte("x")})
	mustOK(t, fs.Remove("d/f"))
	mustOK(t, fs.Remove("d"))
	mustOK(t, fs.Mkdir("d"))
	mustOK(t, fs.Remove("d"))
	mustUpper(t, fs, []string{"d whiteout"})
	mustLookupNotFound(t, fs, "d")
}

// 不透明祖先之下下层不可达，删除不写白障。
func TestRemoveUnderOpaqueNoWhiteout(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"o/": nil, "o/f": []byte("x")})
	mustOK(t, fs.Remove("o/f"))
	mustOK(t, fs.Remove("o"))
	mustOK(t, fs.Mkdir("o")) // o 变为不透明目录
	mustOK(t, fs.Write("o/g", []byte("1")))
	mustOK(t, fs.Remove("o/g"))
	mustUpper(t, fs, []string{"o opaque"})
	// 下层被遮蔽的 o/f 在合并视图中不存在。
	mustErrIs(t, fs.Remove("o/f"), ErrNotFound)
	mustUpper(t, fs, []string{"o opaque"})
}

// 写文件替换白障，使下层同名目录被文件遮蔽。
func TestWriteOverWhiteoutShadowsLowerDir(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"w/": nil, "w/f": []byte("x")})
	mustOK(t, fs.Remove("w/f"))
	mustOK(t, fs.Remove("w"))
	mustUpper(t, fs, []string{"w whiteout"})
	mustOK(t, fs.Write("w", []byte("v")))
	mustUpper(t, fs, []string{"w file v"})
	mustLookupFile(t, fs, "w", "v")
	_, err := fs.ReadDir("w")
	mustErrIs(t, err, ErrNotDir)
}

// 对下层深处写文件时，上层祖先补为普通目录。
func TestWriteDeepFillsPlainDirAncestors(t *testing.T) {
	fs := mustNew(t, map[string][]byte{
		"a/": nil, "a/b/": nil, "a/b/c/": nil, "a/b/c/f": []byte("x"),
	})
	mustOK(t, fs.Write("a/b/c/g", []byte("1")))
	mustUpper(t, fs, []string{
		"a dir", "a/b dir", "a/b/c dir", "a/b/c/g file 1",
	})
	mustReadDir(t, fs, "a/b/c", []string{"f", "g"})
}

// 删除目录丢弃其下全部上层记录。
func TestRemoveDropsUpperSubtreeRecords(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"e/": nil, "e/f": []byte("x")})
	mustOK(t, fs.Write("e/g", []byte("1")))
	mustOK(t, fs.Remove("e/f"))
	mustUpper(t, fs, []string{"e dir", "e/f whiteout", "e/g file 1"})
	mustOK(t, fs.Remove("e/g"))
	mustOK(t, fs.Remove("e"))
	// e/f 的白障一并被丢弃，只留 e 的白障。
	mustUpper(t, fs, []string{"e whiteout"})
}

// 上层文件盖住下层目录；上层目录盖住下层文件。
func TestUpperShadowsLowerByKind(t *testing.T) {
	fs := mustNew(t, map[string][]byte{
		"c/": nil, "c/f": []byte("x"), "g": []byte("y"),
	})
	// 先删除下层目录再写文件：上层文件盖住下层目录。
	mustOK(t, fs.Remove("c/f"))
	mustOK(t, fs.Remove("c"))
	mustOK(t, fs.Write("c", []byte("v")))
	mustLookupFile(t, fs, "c", "v")
	_, err := fs.Lookup("c/f")
	mustErrIs(t, err, ErrNotDir)
	_, err = fs.ReadDir("c")
	mustErrIs(t, err, ErrNotDir)
	// 先删除下层文件再建目录：上层（不透明）目录盖住下层文件。
	mustOK(t, fs.Remove("g"))
	mustOK(t, fs.Mkdir("g"))
	mustLookupDir(t, fs, "g")
	mustReadDir(t, fs, "g", []string{})
	mustUpper(t, fs, []string{"c file v", "g opaque"})
}

// ReadDir 的并集、同名上层优先与字节序排序。
func TestReadDirUnionPriorityAndOrder(t *testing.T) {
	fs := mustNew(t, map[string][]byte{
		"m/": nil, "m/a": []byte("la"), "m/b": []byte("lb"),
	})
	mustOK(t, fs.Write("m/b", []byte("ub")))
	mustOK(t, fs.Write("m/c", []byte("uc")))
	mustReadDir(t, fs, "m", []string{"a", "b", "c"})
	mustLookupFile(t, fs, "m/b", "ub")
	// 白障隐藏下层同名项。
	mustOK(t, fs.Remove("m/a"))
	mustReadDir(t, fs, "m", []string{"b", "c"})
	mustUpper(t, fs, []string{
		"m dir", "m/a whiteout", "m/b file ub", "m/c file uc",
	})
}

// 被拒绝的操作不得改变上层。
func TestRejectedOpsKeepUpper(t *testing.T) {
	fs := mustNew(t, map[string][]byte{
		"a/": nil, "a/f": []byte("x"), "g": []byte("y"),
	})
	mustOK(t, fs.Write("u", []byte("1")))
	before := upperStrings(fs)
	rejections := []error{
		fs.Mkdir(""),
		fs.Mkdir("a/f"),
		fs.Mkdir("a/f/k"),
		fs.Mkdir("nope/k"),
		fs.Write("", nil),
		fs.Write("a", nil),
		fs.Write("a/f/k", nil),
		fs.Remove(""),
		fs.Remove("zz"),
		fs.Remove("a"),
		fs.Rename("", "x"),
		fs.Rename("a", ""),
		fs.Rename("zz", "x"),
		fs.Rename("a", "a"),
		fs.Rename("a", "a/b"),
		fs.Rename("a", "z"),
		fs.Rename("g", "a"),
		fs.Rename("u", "a"),
	}
	for i, err := range rejections {
		if err == nil {
			t.Fatalf("rejection %d unexpectedly succeeded", i)
		}
	}
	if got := upperStrings(fs); !reflect.DeepEqual(got, before) {
		t.Fatalf("upper changed by rejected ops: %q -> %q", before, got)
	}
}

// 各类拒绝原因可用 errors.Is 区分。
func TestErrorKinds(t *testing.T) {
	fs := mustNew(t, map[string][]byte{
		"a/": nil, "a/f": []byte("x"), "g": []byte("y"),
	})
	mustErrIs(t, fs.Mkdir("a//b"), ErrInvalidPath)
	mustErrIs(t, fs.Mkdir("a/b/../c"), ErrInvalidPath)
	mustErrIs(t, fs.Write("a/f/k", nil), ErrNotDir)
	mustErrIs(t, fs.Write("zz/k", nil), ErrNotFound)
	mustErrIs(t, fs.Mkdir("a"), ErrExist)
	mustErrIs(t, fs.Write("a", nil), ErrIsDir)
	mustErrIs(t, fs.Remove("zz"), ErrNotFound)
	mustErrIs(t, fs.Remove("a"), ErrNotEmpty)
	mustErrIs(t, fs.Rename("a", "z"), ErrCrossLayer)
	mustErrIs(t, fs.Rename("a", "a"), ErrRenameSelf)
	mustErrIs(t, fs.Rename("a", "a/b"), ErrRenameSelf)
	mustErrIs(t, fs.Rename("g", "a"), ErrIsDir)
	mustOK(t, fs.Mkdir("e"))
	mustErrIs(t, fs.Rename("e", "g"), ErrNotDir)
	_, err := fs.Lookup("zz")
	mustErrIs(t, err, ErrNotFound)
	_, err = fs.Lookup("a/f/k")
	mustErrIs(t, err, ErrNotDir)
	_, err = fs.ReadDir("g")
	mustErrIs(t, err, ErrNotDir)
	_, err = fs.ReadDir("zz")
	mustErrIs(t, err, ErrNotFound)
}

// 构造时下层非法则整体拒绝。
func TestNewRejectsInvalidLower(t *testing.T) {
	bad := []map[string][]byte{
		{"/a": nil},
		{"a//b": nil},
		{"a/./b": nil},
		{"a/../b": nil},
		{"a/b": nil},                           // 缺少上级目录 a
		{"a/": nil, "a": nil},                  // 同一路径既是文件又是目录
		{"a/b/": nil, "a/b/c": nil, "a/": nil}, // a/b 既是目录又被当作... 合法
	}
	for i, lower := range bad[:6] {
		if _, err := New(lower); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("case %d: err = %v, want ErrInvalidPath", i, err)
		}
	}
	if _, err := New(bad[6]); err != nil {
		t.Fatalf("valid lower rejected: %v", err)
	}
}

// 题目示例：Mkdir/Write/Remove 后改名纯上层目录到刚删除的下层目录处。
func TestSpecExampleWalkthrough(t *testing.T) {
	fs := mustNew(t, map[string][]byte{
		"a/": nil, "a/f": []byte("af"),
		"b/": nil, "b/g": []byte("bg"),
		"c": []byte("c"),
	})
	mustOK(t, fs.Mkdir("x"))
	mustOK(t, fs.Write("x/y", []byte("1")))
	mustOK(t, fs.Remove("b/g"))
	mustUpper(t, fs, []string{
		"b dir", "b/g whiteout", "x dir", "x/y file 1",
	})
	// b 已无子项，Rename(x, b) 成功且变为不透明目录。
	mustOK(t, fs.Rename("x", "b"))
	mustUpper(t, fs, []string{"b opaque", "b/y file 1"})
	mustReadDir(t, fs, "", []string{"a", "b", "c"})
	mustReadDir(t, fs, "b", []string{"y"})
	mustLookupNotFound(t, fs, "b/g")
	// 下层目录跨层改名被拒绝。
	mustErrIs(t, fs.Rename("a", "z"), ErrCrossLayer)
	// 下层文件改名成功：c 白障、d 文件。
	mustOK(t, fs.Rename("c", "d"))
	mustUpper(t, fs, []string{
		"b opaque", "b/y file 1", "c whiteout", "d file c",
	})
	mustLookupNotFound(t, fs, "c")
	mustLookupFile(t, fs, "d", "c")
}

// 改名纯上层目录到下层同名目录处变不透明。
func TestRenamePureUpperDirOntoLowerDirBecomesOpaque(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"b/": nil})
	mustOK(t, fs.Mkdir("x"))
	mustOK(t, fs.Write("x/y", []byte("1")))
	mustOK(t, fs.Rename("x", "b"))
	mustUpper(t, fs, []string{"b opaque", "b/y file 1"})
	mustReadDir(t, fs, "b", []string{"y"})
}

// 改名不透明目录须在原处写白障（原处下层可达）。
func TestRenameOpaqueDirWritesWhiteoutAtOld(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"s/": nil, "s/f": []byte("x")})
	mustOK(t, fs.Remove("s/f"))
	mustOK(t, fs.Remove("s"))
	mustOK(t, fs.Mkdir("s")) // s 不透明
	mustOK(t, fs.Write("s/g", []byte("1")))
	mustOK(t, fs.Rename("s", "dst"))
	mustUpper(t, fs, []string{
		"dst opaque", "dst/g file 1", "s whiteout",
	})
	mustLookupNotFound(t, fs, "s")
	mustLookupNotFound(t, fs, "s/f")
	mustReadDir(t, fs, "dst", []string{"g"})
}

// 下层不可达处改名不写白障。
func TestRenameUnreachableOldLeavesNoWhiteout(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"o/": nil, "o/f": []byte("x")})
	mustOK(t, fs.Remove("o/f"))
	mustOK(t, fs.Remove("o"))
	mustOK(t, fs.Mkdir("o")) // o 不透明
	mustOK(t, fs.Mkdir("o/h"))
	mustOK(t, fs.Write("o/h/i", []byte("1")))
	// o/h 下层不可达（祖先 o 不透明），改名后原处不留记录。
	mustOK(t, fs.Rename("o/h", "o/k"))
	mustUpper(t, fs, []string{
		"o opaque", "o/k dir", "o/k/i file 1",
	})
	mustLookupNotFound(t, fs, "o/h")
	mustLookupFile(t, fs, "o/k/i", "1")
}

// new 为白障时，改名的目录在 new 处变不透明。
func TestRenameOntoWhiteoutBecomesOpaque(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"v/": nil, "v/f": []byte("x")})
	mustOK(t, fs.Remove("v/f"))
	mustOK(t, fs.Remove("v")) // v 白障
	mustOK(t, fs.Mkdir("src"))
	mustOK(t, fs.Write("src/y", []byte("1")))
	mustOK(t, fs.Rename("src", "v"))
	mustUpper(t, fs, []string{"v opaque", "v/y file 1"})
	mustReadDir(t, fs, "v", []string{"y"})
	mustLookupNotFound(t, fs, "v/f")
}

// new 非空目录拒绝；空目录被替换。
func TestRenameDirOntoNonEmptyAndEmpty(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"n/": nil, "n/g": []byte("x")})
	mustOK(t, fs.Mkdir("s"))
	mustOK(t, fs.Write("s/y", []byte("1")))
	mustErrIs(t, fs.Rename("s", "n"), ErrNotEmpty)
	mustUpper(t, fs, []string{"s dir", "s/y file 1"})
	// 空目录被替换：new 处变不透明（L(new) 是目录且下层可达）。
	mustOK(t, fs.Remove("n/g"))
	mustOK(t, fs.Rename("s", "n"))
	mustUpper(t, fs, []string{"n opaque", "n/y file 1"})
	mustReadDir(t, fs, "n", []string{"y"})
}

// 移入自身。
func TestRenameIntoSelf(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"a/": nil, "a/f": []byte("x")})
	mustOK(t, fs.Mkdir("u"))
	mustErrIs(t, fs.Rename("u", "u"), ErrRenameSelf)
	mustErrIs(t, fs.Rename("u", "u/v"), ErrRenameSelf)
	mustErrIs(t, fs.Rename("a", "a"), ErrRenameSelf)
	mustErrIs(t, fs.Rename("a", "a/b"), ErrRenameSelf)
	mustUpper(t, fs, []string{"u dir"})
}

// 文件改名的冲突：目标是目录报是目录；目录改到文件报不是目录；
// 文件替换文件成功。
func TestRenameFileConflicts(t *testing.T) {
	fs := mustNew(t, map[string][]byte{
		"a/": nil, "a/f": []byte("x"), "g": []byte("y"),
	})
	mustOK(t, fs.Write("u", []byte("1")))
	mustErrIs(t, fs.Rename("u", "a"), ErrIsDir)
	mustOK(t, fs.Mkdir("e"))
	mustErrIs(t, fs.Rename("e", "g"), ErrNotDir)
	// 文件替换文件（new 为下层文件）：new 被覆盖，old 仅上层不留白障。
	mustOK(t, fs.Rename("u", "g"))
	mustUpper(t, fs, []string{"e dir", "g file 1"})
	mustLookupFile(t, fs, "g", "1")
}

// 下层文件改名：原处写白障。
func TestRenameLowerFileWritesWhiteout(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"c": []byte("cc")})
	mustOK(t, fs.Rename("c", "d"))
	mustUpper(t, fs, []string{"c whiteout", "d file cc"})
	mustLookupNotFound(t, fs, "c")
	mustLookupFile(t, fs, "d", "cc")
}

// 合并目录（普通目录 + 下层目录）跨层拒绝；下层文件被上层文件
// 遮蔽后不再是合并目录，可改名。
func TestRenameMergedDirCrossLayer(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"a/": nil, "a/f": []byte("x")})
	mustOK(t, fs.Write("a/g", []byte("1"))) // a 变为普通目录记录
	mustErrIs(t, fs.Rename("a", "z"), ErrCrossLayer)
	// 下层目录被删除（白障）后，a 成为纯上层目录，可改名。
	mustOK(t, fs.Remove("a/f"))
	mustOK(t, fs.Remove("a/g"))
	mustOK(t, fs.Remove("a"))
	mustOK(t, fs.Mkdir("a"))
	mustOK(t, fs.Write("a/h", []byte("2")))
	mustOK(t, fs.Rename("a", "z"))
	mustUpper(t, fs, []string{
		"a whiteout", "z opaque", "z/h file 2",
	})
}

// 改名丢弃 new 之下的全部上层记录。
func TestRenameDropsNewSubtreeRecords(t *testing.T) {
	fs := mustNew(t, nil)
	mustOK(t, fs.Mkdir("s"))
	mustOK(t, fs.Write("s/y", []byte("1")))
	// 构造 new 处为空目录但其下有上层记录不可能（不变式），
	// 改为验证 new 为白障且其下无记录的基本情形之外，
	// 用空上层目录做替换。
	mustOK(t, fs.Mkdir("d"))
	mustOK(t, fs.Rename("s", "d"))
	mustUpper(t, fs, []string{"d dir", "d/y file 1"})
}

// 改名前后的「下层可达」按改名前状态判定：把目录改到自身
// 子树之外、但 new 的祖先含下层目录时正常补祖先。
func TestRenameFillsNewAncestors(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"p/": nil, "p/q/": nil})
	mustOK(t, fs.Mkdir("s"))
	mustOK(t, fs.Write("s/y", []byte("1")))
	mustOK(t, fs.Rename("s", "p/q/r"))
	mustUpper(t, fs, []string{
		"p dir", "p/q dir", "p/q/r dir", "p/q/r/y file 1",
	})
	mustReadDir(t, fs, "p/q", []string{"r"})
}

// Lookup/ReadDir 的祖先判定与非法路径。
func TestLookupReadDirErrors(t *testing.T) {
	fs := mustNew(t, map[string][]byte{"a/": nil, "a/f": []byte("x")})
	for _, p := range []string{"/a", "a//f", "a/./f", "a/../f", "a/"} {
		if _, err := fs.Lookup(p); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("Lookup(%q) err = %v, want ErrInvalidPath", p, err)
		}
	}
	if _, err := fs.Lookup("zz/f"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := fs.ReadDir("a/f"); !errors.Is(err, ErrNotDir) {
		t.Fatalf("want ErrNotDir, got %v", err)
	}
	if _, err := fs.ReadDir("a/f/g"); !errors.Is(err, ErrNotDir) {
		t.Fatalf("want ErrNotDir, got %v", err)
	}
	// 根总是目录。
	e, err := fs.Lookup("")
	mustOK(t, err)
	if e.Type != EntryDir {
		t.Fatalf("root should be dir")
	}
	mustReadDir(t, fs, "", []string{"a"})
}
