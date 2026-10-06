package lineage

import (
	"fmt"
	"strings"
	"testing"
)

func mustLoad(t *testing.T, s *Service, c Commit) {
	t.Helper()
	if err := s.LoadCommit(c); err != nil {
		t.Fatalf("load %s: %v", c.ID, err)
	}
}

func mustVersion(t *testing.T, s *Service, ids ...string) int {
	t.Helper()
	v, err := s.NewIgnoreVersion(ids)
	if err != nil {
		t.Fatalf("new ignore version: %v", err)
	}
	return v
}

func mustBlame(t *testing.T, s *Service, id, path string, v int) []Attribution {
	t.Helper()
	res, err := s.Blame(id, path, v)
	if err != nil {
		t.Fatalf("blame(%q,%q,%d): %v", id, path, v, err)
	}
	return res
}

func attrsStr(attrs []Attribution) string {
	var b strings.Builder
	for i, a := range attrs {
		fmt.Fprintf(&b, "  行%d -> (%s, %s, 行%d, 被忽略仍归属=%v)\n",
			i+1, a.CommitID, a.Path, a.Line, a.IgnoredButAttributed)
	}
	return b.String()
}

func at(id, path string, line int, flag bool) Attribution {
	return Attribution{CommitID: id, Path: path, Line: line, IgnoredButAttributed: flag}
}

// check 比较实际输出与期望，不一致时返回描述串。
func check(got []Attribution, want ...Attribution) string {
	if len(got) != len(want) {
		return fmt.Sprintf("行数不符: 实际 %d, 期望 %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Sprintf("行%d: 实际 %+v, 期望 %+v", i+1, got[i], want[i])
		}
	}
	return ""
}

func TestLinearHistory(t *testing.T) {
	s := NewService()
	mustLoad(t, s, Commit{ID: "c1", Files: map[string]string{"f": "a\nb\nc\n"}})
	mustLoad(t, s, Commit{ID: "c2", Parents: []string{"c1"}, Files: map[string]string{"f": "a\nX\nc\n"}})
	mustLoad(t, s, Commit{ID: "c3", Parents: []string{"c2"}, Files: map[string]string{"f": "a\nX\nc\nd\n"}})
	v := mustVersion(t, s)

	got := mustBlame(t, s, "c3", "f", v)
	t.Logf("输入: 线性链 c1(a,b,c)->c2(b改X)->c3(追加d); 查询 (c3, f, v%d)", v)
	t.Logf("实际输出:\n%s", attrsStr(got))
	t.Logf("判定依据: a,c 源自 c1; X 由 c2 引入; d 由 c3 引入")
	if msg := check(got,
		at("c1", "f", 1, false),
		at("c2", "f", 2, false),
		at("c1", "f", 3, false),
		at("c3", "f", 4, false),
	); msg != "" {
		t.Fatal(msg)
	}
}

func TestForkAndMerge(t *testing.T) {
	s := NewService()
	mustLoad(t, s, Commit{ID: "base", Files: map[string]string{"f": "a\nb\n"}})
	mustLoad(t, s, Commit{ID: "left", Parents: []string{"base"}, Files: map[string]string{"f": "a\nb\nL\n"}})
	mustLoad(t, s, Commit{ID: "right", Parents: []string{"base"}, Files: map[string]string{"f": "a\nb\nR\n"}})
	mustLoad(t, s, Commit{ID: "m", Parents: []string{"left", "right"}, Files: map[string]string{"f": "a\nb\nL\nR\nM\n"}})
	v := mustVersion(t, s)

	got := mustBlame(t, s, "m", "f", v)
	t.Logf("输入: base 分叉为 left(加L)/right(加R), m 合并两者并加 M; 查询 (m, f, v%d)", v)
	t.Logf("实际输出:\n%s", attrsStr(got))
	t.Logf("判定依据: L 在第一父 left 中有配对 -> left; R 第一父无、第二父有 -> right; M 两父都无 -> 合并提交 m")
	if msg := check(got,
		at("base", "f", 1, false),
		at("base", "f", 2, false),
		at("left", "f", 3, false),
		at("right", "f", 3, false),
		at("m", "f", 5, false),
	); msg != "" {
		t.Fatal(msg)
	}
}

func TestMergeBothParentsContainLine(t *testing.T) {
	s := NewService()
	mustLoad(t, s, Commit{ID: "base", Files: map[string]string{"f": "a\n"}})
	mustLoad(t, s, Commit{ID: "p1", Parents: []string{"base"}, Files: map[string]string{"f": "a\nS\n"}})
	mustLoad(t, s, Commit{ID: "p2", Parents: []string{"base"}, Files: map[string]string{"f": "a\nS\n"}})
	mustLoad(t, s, Commit{ID: "m", Parents: []string{"p1", "p2"}, Files: map[string]string{"f": "a\nS\n"}})
	v := mustVersion(t, s)

	got := mustBlame(t, s, "m", "f", v)
	t.Logf("输入: p1 与 p2 各自独立引入相同行 S, m 合并; 查询 (m, f, v%d)", v)
	t.Logf("实际输出:\n%s", attrsStr(got))
	t.Logf("判定依据: 两父都含 S 时以第一父 p1 为准")
	if msg := check(got,
		at("base", "f", 1, false),
		at("p1", "f", 2, false),
	); msg != "" {
		t.Fatal(msg)
	}
}

func TestRenameFollowThrough(t *testing.T) {
	s := NewService()
	mustLoad(t, s, Commit{ID: "c1", Files: map[string]string{"old.txt": "a\nb\n"}})
	mustLoad(t, s, Commit{
		ID:      "c2",
		Parents: []string{"c1"},
		Files:   map[string]string{"new.txt": "a\nb\nc\n"},
		Renames: []Rename{{Old: "old.txt", New: "new.txt"}},
	})
	v := mustVersion(t, s)

	got := mustBlame(t, s, "c2", "new.txt", v)
	t.Logf("输入: c1 有 old.txt(a,b); c2 声明 old.txt->new.txt 并追加 c; 查询 (c2, new.txt, v%d)", v)
	t.Logf("实际输出:\n%s", attrsStr(got))
	t.Logf("判定依据: 改名穿透, a/b 归属到 c1 的 old.txt; c 由 c2 以 new.txt 引入")
	if msg := check(got,
		at("c1", "old.txt", 1, false),
		at("c1", "old.txt", 2, false),
		at("c2", "new.txt", 3, false),
	); msg != "" {
		t.Fatal(msg)
	}
}

func TestChainedRenames(t *testing.T) {
	s := NewService()
	mustLoad(t, s, Commit{ID: "c1", Files: map[string]string{"a.txt": "x\ny\n"}})
	mustLoad(t, s, Commit{
		ID:      "c2",
		Parents: []string{"c1"},
		Files:   map[string]string{"b.txt": "x\ny\nz\n"},
		Renames: []Rename{{Old: "a.txt", New: "b.txt"}},
	})
	mustLoad(t, s, Commit{
		ID:      "c3",
		Parents: []string{"c2"},
		Files:   map[string]string{"c.txt": "x\ny\nz\nw\n"},
		Renames: []Rename{{Old: "b.txt", New: "c.txt"}},
	})
	v := mustVersion(t, s)

	got := mustBlame(t, s, "c3", "c.txt", v)
	t.Logf("输入: a.txt -(c2)-> b.txt -(c3)-> c.txt, 每次追加一行; 查询 (c3, c.txt, v%d)", v)
	t.Logf("实际输出:\n%s", attrsStr(got))
	t.Logf("判定依据: 连续改名逐段穿透, x/y 归 c1 的 a.txt, z 归 c2 的 b.txt, w 归 c3")
	if msg := check(got,
		at("c1", "a.txt", 1, false),
		at("c1", "a.txt", 2, false),
		at("c2", "b.txt", 3, false),
		at("c3", "c.txt", 4, false),
	); msg != "" {
		t.Fatal(msg)
	}
}

func TestRenameBack(t *testing.T) {
	s := NewService()
	mustLoad(t, s, Commit{ID: "c1", Files: map[string]string{"f": "a\n"}})
	mustLoad(t, s, Commit{
		ID:      "c2",
		Parents: []string{"c1"},
		Files:   map[string]string{"g": "a\nb\n"},
		Renames: []Rename{{Old: "f", New: "g"}},
	})
	mustLoad(t, s, Commit{
		ID:      "c3",
		Parents: []string{"c2"},
		Files:   map[string]string{"f": "a\nb\nc\n"},
		Renames: []Rename{{Old: "g", New: "f"}},
	})
	v := mustVersion(t, s)

	got := mustBlame(t, s, "c3", "f", v)
	t.Logf("输入: f -(c2)-> g -(c3)-> f, 每次追加一行; 查询 (c3, f, v%d)", v)
	t.Logf("实际输出:\n%s", attrsStr(got))
	t.Logf("判定依据: 改回原名后仍能穿透两段改名, a 归 c1 的 f, b 归 c2 的 g, c 归 c3")
	if msg := check(got,
		at("c1", "f", 1, false),
		at("c2", "g", 2, false),
		at("c3", "f", 3, false),
	); msg != "" {
		t.Fatal(msg)
	}
}

// buildIgnoreChain 构造 c1(a) -> c2(加b) -> c3(加c) -> c4(加d)。
func buildIgnoreChain(t *testing.T, s *Service) {
	t.Helper()
	mustLoad(t, s, Commit{ID: "c1", Files: map[string]string{"f": "a\n"}})
	mustLoad(t, s, Commit{ID: "c2", Parents: []string{"c1"}, Files: map[string]string{"f": "a\nb\n"}})
	mustLoad(t, s, Commit{ID: "c3", Parents: []string{"c2"}, Files: map[string]string{"f": "a\nb\nc\n"}})
	mustLoad(t, s, Commit{ID: "c4", Parents: []string{"c3"}, Files: map[string]string{"f": "a\nb\nc\nd\n"}})
}

func TestIgnoreSingleLevel(t *testing.T) {
	s := NewService()
	buildIgnoreChain(t, s)
	v0 := mustVersion(t, s)
	v1 := mustVersion(t, s, "c3")

	got := mustBlame(t, s, "c4", "f", v1)
	t.Logf("输入: 链 c1..c4 各加一行; 名单 v%d={c3}; 查询 (c4, f, v%d)", v1, v1)
	t.Logf("实际输出:\n%s", attrsStr(got))
	t.Logf("判定依据: c 行由被忽略的 c3 引入, 所有父中无配对 -> 仍归 c3 并打标记; 其余行不受影响")
	if msg := check(got,
		at("c1", "f", 1, false),
		at("c2", "f", 2, false),
		at("c3", "f", 3, true),
		at("c4", "f", 4, false),
	); msg != "" {
		t.Fatal(msg)
	}

	// 旧版本 v0 仍可查询且结果不变。
	got0 := mustBlame(t, s, "c4", "f", v0)
	t.Logf("输入: 同一查询改用旧名单版本 v%d(空)", v0)
	t.Logf("实际输出:\n%s", attrsStr(got0))
	t.Logf("判定依据: 旧版本可继续引用, 无提交被忽略, 全部不带标记")
	if msg := check(got0,
		at("c1", "f", 1, false),
		at("c2", "f", 2, false),
		at("c3", "f", 3, false),
		at("c4", "f", 4, false),
	); msg != "" {
		t.Fatal(msg)
	}
}

func TestIgnoreMultiLevel(t *testing.T) {
	s := NewService()
	buildIgnoreChain(t, s)
	v := mustVersion(t, s, "c2", "c3")

	got := mustBlame(t, s, "c4", "f", v)
	t.Logf("输入: 链 c1..c4 各加一行; 名单 v%d={c2,c3}; 查询 (c4, f, v%d)", v, v)
	t.Logf("实际输出:\n%s", attrsStr(got))
	t.Logf("判定依据: 连续两个被忽略提交被连续穿透, b 归 c2、c 归 c3 且都带标记, a/d 不受影响")
	if msg := check(got,
		at("c1", "f", 1, false),
		at("c2", "f", 2, true),
		at("c3", "f", 3, true),
		at("c4", "f", 4, false),
	); msg != "" {
		t.Fatal(msg)
	}
}

func TestIgnorePassthroughToAncestor(t *testing.T) {
	s := NewService()
	buildIgnoreChain(t, s)
	// c2 被忽略但 a 行并非它引入: 追溯穿过 c2 落到 c1, 不带标记。
	v := mustVersion(t, s, "c2")

	got := mustBlame(t, s, "c2", "f", v)
	t.Logf("输入: 名单 v%d={c2}; 查询 (c2, f, v%d)", v, v)
	t.Logf("实际输出:\n%s", attrsStr(got))
	t.Logf("判定依据: a 行穿透被忽略的 c2 归到 c1(无标记); b 行确由 c2 引入, 归 c2 并带标记")
	if msg := check(got,
		at("c1", "f", 1, false),
		at("c2", "f", 2, true),
	); msg != "" {
		t.Fatal(msg)
	}
}

func TestLoadRejections(t *testing.T) {
	s := NewService()
	mustLoad(t, s, Commit{ID: "c1", Files: map[string]string{"f": "a\n"}})

	cases := []struct {
		name string
		c    Commit
		kind Kind
		why  string
	}{
		{"重复标识", Commit{ID: "c1", Files: map[string]string{"f": "z\n"}}, KindDuplicateCommit, "同一标识重复载入"},
		{"父不存在", Commit{ID: "c2", Parents: []string{"ghost"}, Files: map[string]string{"f": "a\n"}}, KindParentNotFound, "父提交不存在"},
		{"改名旧路径父中不存在", Commit{ID: "c3", Parents: []string{"c1"}, Files: map[string]string{"g": "a\n"}, Renames: []Rename{{Old: "nope", New: "g"}}}, KindInvalidRename, "旧路径不在父提交中"},
		{"改名旧路径本提交仍存在", Commit{ID: "c4", Parents: []string{"c1"}, Files: map[string]string{"f": "a\n", "g": "a\n"}, Renames: []Rename{{Old: "f", New: "g"}}}, KindInvalidRename, "旧路径在本提交快照中仍然存在"},
		{"同一新路径多条改名", Commit{ID: "c5", Parents: []string{"c1"}, Files: map[string]string{"g": "a\n"}, Renames: []Rename{{Old: "f", New: "g"}, {Old: "f", New: "g"}}}, KindInvalidRename, "同一新路径对应多条改名记录"},
		{"根提交带改名", Commit{ID: "c6", Files: map[string]string{"g": "a\n"}, Renames: []Rename{{Old: "f", New: "g"}}}, KindInvalidRename, "无父提交可承载改名旧路径"},
		{"空标识", Commit{ID: "", Files: map[string]string{"f": "a\n"}}, KindInvalidParam, "空提交标识"},
	}
	for _, tc := range cases {
		err := s.LoadCommit(tc.c)
		t.Logf("输入: 载入 %+v", tc.c)
		t.Logf("实际输出: err=%v", err)
		t.Logf("判定依据: %s -> 应拒绝且类别为 %d", tc.why, tc.kind)
		if !IsKind(err, tc.kind) {
			t.Fatalf("%s: 期望类别 %d, 实际 %v", tc.name, tc.kind, err)
		}
	}
	if n := s.CommitCount(); n != 1 {
		t.Fatalf("被拒绝的载入留下痕迹: 提交数=%d, 期望 1", n)
	}
	t.Logf("判定依据: 全部拒绝后提交数仍为 1, 未留任何痕迹")

	// 忽略名单中列入不存在的提交 -> 参数非法。
	_, err := s.NewIgnoreVersion([]string{"c1", "ghost"})
	t.Logf("输入: 创建名单 [c1 ghost]; 实际输出: err=%v", err)
	t.Logf("判定依据: 名单含不存在的提交标识 -> 参数非法")
	if !IsKind(err, KindInvalidParam) {
		t.Fatalf("期望参数非法, 实际 %v", err)
	}
}

func TestQueryErrorPrecedence(t *testing.T) {
	s := NewService()
	mustLoad(t, s, Commit{ID: "c1", Files: map[string]string{"f": "a\n"}})
	v := mustVersion(t, s) // v0 存在, v1 不存在

	cases := []struct {
		name    string
		id      string
		path    string
		version int
		kind    Kind
		why     string
	}{
		// 四个单一错误。
		{"参数非法/空标识", "", "f", v, KindInvalidParam, "空标识"},
		{"参数非法/空路径", "c1", "", v, KindInvalidParam, "空路径"},
		{"参数非法/负版本", "c1", "f", -1, KindInvalidParam, "负的名单版本"},
		{"提交不存在", "ghost", "f", v, KindCommitNotFound, "提交不存在"},
		{"名单版本不存在", "c1", "f", v + 1, KindVersionNotFound, "名单版本不存在"},
		{"路径不存在", "c1", "nope", v, KindPathNotFound, "路径在该提交中不存在"},
		// 相邻错误对的优先关系: 只报次序最靠前的一个。
		{"参数非法优先于提交不存在", "", "f", v, KindInvalidParam, "空标识 + 提交不存在 -> 报参数非法"},
		{"参数非法优先于提交不存在/负版本", "ghost", "f", -1, KindInvalidParam, "负版本 + 提交不存在 -> 报参数非法"},
		{"提交不存在优先于版本不存在", "ghost", "f", v + 1, KindCommitNotFound, "提交不存在 + 版本不存在 -> 报提交不存在"},
		{"版本不存在优先于路径不存在", "c1", "nope", v + 1, KindVersionNotFound, "版本不存在 + 路径不存在 -> 报版本不存在"},
	}
	before := s.Stats()
	for _, tc := range cases {
		_, err := s.Blame(tc.id, tc.path, tc.version)
		t.Logf("输入: blame(%q, %q, %d)", tc.id, tc.path, tc.version)
		t.Logf("实际输出: err=%v", err)
		t.Logf("判定依据: %s", tc.why)
		if !IsKind(err, tc.kind) {
			t.Fatalf("%s: 期望类别 %d, 实际 %v", tc.name, tc.kind, err)
		}
	}
	after := s.Stats()
	if before != after {
		t.Fatalf("被拒绝的查询影响了统计: 前=%+v 后=%+v", before, after)
	}
	s.qcacheMu.Lock()
	qlen := len(s.queryCache)
	s.qcacheMu.Unlock()
	if qlen != 0 {
		t.Fatalf("被拒绝的查询污染了查询缓存: %d 项", qlen)
	}
	t.Logf("判定依据: 全部被拒查询执行后统计与缓存均无变化")
}
