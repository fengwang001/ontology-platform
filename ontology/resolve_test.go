package ontology

import (
	"fmt"
	"strings"
	"testing"
)

// hid 生成测试用对象标识：前缀 + 零填充的序号，总长 40。
func hid(prefix string, n int) string {
	return fmt.Sprintf("%s%0*x", prefix, idLen-len(prefix), n)
}

// mkid 生成测试用对象标识：前缀 + 全零填充，总长 40。
func mkid(prefix string) string {
	return prefix + strings.Repeat("0", idLen-len(prefix))
}

func mustPut(t *testing.T, s *Service, o Object) {
	t.Helper()
	if err := s.Put(o); err != nil {
		t.Fatalf("Put(%s %s) failed: %v", o.Type, o.ID, err)
	}
	t.Logf("Put(%s %s) -> ok", o.Type, o.ID[:12])
}

func mustSetRef(t *testing.T, s *Service, name, id string) {
	t.Helper()
	if err := s.SetRef(name, id); err != nil {
		t.Fatalf("SetRef(%q) failed: %v", name, err)
	}
}

// expectOK 断言解析成功并命中 wantID，打印输入、实际输出与判定依据。
func expectOK(t *testing.T, s *Service, expr, wantID, why string) {
	t.Helper()
	res, err := s.Resolve(expr, Query{})
	if err != nil {
		t.Fatalf("input=%q -> unexpected error %v | 判定依据: %s", expr, err, why)
	}
	if res.ID != wantID {
		t.Fatalf("input=%q -> id=%s, want %s | 判定依据: %s", expr, res.ID, wantID, why)
	}
	t.Logf("input=%q -> id=%s type=%s | 判定依据: %s", expr, res.ID[:12], res.Type, why)
}

// expectErr 断言解析以 kind 失败且失败段下标为 seg（-1 表示基址或最终类型
// 检查），打印输入、实际输出与判定依据。
func expectErr(t *testing.T, s *Service, expr string, q Query, kind ErrKind, seg int, why string) *Error {
	t.Helper()
	_, err := s.Resolve(expr, q)
	if err == nil {
		t.Fatalf("input=%q -> unexpected success | want %s | 判定依据: %s", expr, kind, why)
	}
	if err.Kind != kind {
		t.Fatalf("input=%q -> kind=%s, want %s (err=%v) | 判定依据: %s", expr, err.Kind, kind, err, why)
	}
	if err.Segment != seg {
		t.Fatalf("input=%q -> segment=%d, want %d (err=%v) | 判定依据: %s", expr, err.Segment, seg, err, why)
	}
	t.Logf("input=%q -> err=%q segment=%d | 判定依据: %s", expr, err, err.Segment, why)
	return err
}

// fixture 是一棵标准对象图：
// c0(根) <- c1 <- c2(父=[c1,c0])，各提交的树均为 t0；tag1->c2，tag2->tag1；
// b0 为文件。各标识前缀互不相同，互不构成前缀冲突。
type fixture struct {
	s          *Service
	t0, b0     string
	c0, c1, c2 string
	tag1, tag2 string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s := NewService(Config{})
	f := &fixture{
		s:    s,
		t0:   hid("1100", 1),
		b0:   hid("2200", 1),
		c0:   hid("aa00", 1),
		c1:   hid("bb00", 1),
		c2:   hid("cc00", 1),
		tag1: hid("dd00", 1),
		tag2: hid("ee00", 1),
	}
	mustPut(t, s, Object{ID: f.t0, Type: TypeTree})
	mustPut(t, s, Object{ID: f.b0, Type: TypeBlob})
	mustPut(t, s, Object{ID: f.c0, Type: TypeCommit, Tree: f.t0})
	mustPut(t, s, Object{ID: f.c1, Type: TypeCommit, Tree: f.t0, Parents: []string{f.c0}})
	mustPut(t, s, Object{ID: f.c2, Type: TypeCommit, Tree: f.t0, Parents: []string{f.c1, f.c0}})
	mustPut(t, s, Object{ID: f.tag1, Type: TypeTag, Target: f.c2})
	mustPut(t, s, Object{ID: f.tag2, Type: TypeTag, Target: f.tag1})
	return f
}

// 全名直接命中优先于命名空间补全。
func TestFullNameBeatsCompletion(t *testing.T) {
	f := newFixture(t)
	mustSetRef(t, f.s, "refs/heads/x", f.c0)
	mustSetRef(t, f.s, "refs/heads/refs/heads/x", f.c1)
	expectOK(t, f.s, "refs/heads/x", f.c0,
		"短名恰好是全名 refs/heads/x 时直接命中；补全 refs/heads/+短名 虽也存在但次序靠后")
}

// 短名按固定命名空间次序补全，首个命中者胜出。
func TestNamespaceOrder(t *testing.T) {
	f := newFixture(t)
	mustSetRef(t, f.s, "refs/heads/main", f.c0)
	mustSetRef(t, f.s, "refs/tags/main", f.c1)
	mustSetRef(t, f.s, "refs/tags/only", f.c2)
	expectOK(t, f.s, "main", f.c0,
		"refs/heads/ 次序先于 refs/tags/，main 在两个命名空间都有，heads 胜出")
	expectOK(t, f.s, "only", f.c2,
		"only 仅在 refs/tags/ 中，heads 未命中后落到 tags")
}

// 短名同时是引用与有效标识前缀时报「引用与标识歧义」；恰好是全名时豁免。
func TestRefIDAmbiguity(t *testing.T) {
	f := newFixture(t)
	mustPut(t, f.s, Object{ID: hid("abcd", 7), Type: TypeBlob})
	mustSetRef(t, f.s, "refs/heads/abcd", f.c0)
	expectErr(t, f.s, "abcd", Query{}, ErrRefIDAmbiguous, -1,
		"abcd 经补全命中 refs/heads/abcd，同时是对象 abcd...07 的有效前缀（长度4达到下限）")

	// 全名豁免：把 "abcde" 直接设为全名，即使它也是有效标识前缀。
	mustPut(t, f.s, Object{ID: hid("abcde", 9), Type: TypeBlob})
	mustSetRef(t, f.s, "abcde", f.c1)
	expectOK(t, f.s, "abcde", f.c1,
		"abcde 恰好是全名（非补全命中），豁免引用与标识歧义，直接命中引用")
}

// 长度低于配置下限的缩写不参与标识匹配，只按引用名解释。
func TestBelowMinAbbrevIsRefOnly(t *testing.T) {
	f := newFixture(t)
	mustPut(t, f.s, Object{ID: hid("abc", 3), Type: TypeBlob})
	expectErr(t, f.s, "abc", Query{}, ErrNotExist, -1,
		"abc 长度3低于下限4，不参与标识匹配；又无此引用，报不存在")
	mustSetRef(t, f.s, "refs/heads/abc", f.c0)
	expectOK(t, f.s, "abc", f.c0,
		"abc 低于下限只按引用名解释：命中 refs/heads/abc，且不与对象 abc...03 构成歧义")
}

// 每种导航段的合法与非法类型。
func TestNavigationSegments(t *testing.T) {
	f := newFixture(t)
	// ^N 取第 N 父（缺省第一父）
	expectOK(t, f.s, f.c2+"^", f.c1, "^ 缺省取第一父 c1")
	expectOK(t, f.s, f.c2+"^2", f.c0, "^2 取第二父 c0")
	expectErr(t, f.s, f.c2+"^3", Query{}, ErrNoSuchParent, 0, "c2 只有2个父，^3 父不存在")
	expectErr(t, f.s, f.c2+"^0", Query{}, ErrInvalid, -1, "^0 文法非法：父序号必须>=1")
	expectErr(t, f.s, f.b0+"^", Query{}, ErrTypeNotNavigable, 0, "对文件取父：类型不可导航")
	expectErr(t, f.s, f.t0+"^", Query{}, ErrTypeNotNavigable, 0, "对树取父：类型不可导航")
	expectErr(t, f.s, f.tag1+"^", Query{}, ErrTypeNotNavigable, 0, "对标签取父：类型不可导航（不隐式剥层）")
	// ~N 沿第一父向上 N 代
	expectOK(t, f.s, f.c2+"~", f.c1, "~ 缺省向上1代")
	expectOK(t, f.s, f.c2+"~2", f.c0, "~2 向上2代到根 c0")
	expectOK(t, f.s, f.c2+"~0", f.c2, "~0 向上0代等于自身")
	expectErr(t, f.s, f.c2+"~3", Query{}, ErrBeyondHistory, 0, "c2 沿第一父只有2代，~3 超出历史")
	expectErr(t, f.s, f.c0+"~", Query{}, ErrBeyondHistory, 0, "根提交无父，~ 超出历史")
	expectErr(t, f.s, f.b0+"~2", Query{}, ErrTypeNotNavigable, 0, "对文件向上：类型不可导航")
	// ^{} 剥去标签直至非标签；对非标签是无副作用的成功
	expectOK(t, f.s, f.tag1+"^{}", f.c2, "tag1->c2，剥一层")
	expectOK(t, f.s, f.tag2+"^{}", f.c2, "tag2->tag1->c2，嵌套剥两层")
	expectOK(t, f.s, f.b0+"^{}", f.b0, "对文件 ^{} 无副作用成功")
	expectOK(t, f.s, f.c2+"^{}", f.c2, "对提交 ^{} 无副作用成功")
	// ^{tree} 取提交的树
	expectOK(t, f.s, f.c2+"^{tree}", f.t0, "取 c2 的树")
	expectErr(t, f.s, f.t0+"^{tree}", Query{}, ErrTypeNotNavigable, 0, "对树取树：类型不可导航")
	expectErr(t, f.s, f.b0+"^{tree}", Query{}, ErrTypeNotNavigable, 0, "对文件取树：类型不可导航")
	expectErr(t, f.s, f.tag1+"^{tree}", Query{}, ErrTypeNotNavigable, 0, "对标签取树：类型不可导航")
	// @{N} 引用日志导航
	mustSetRef(t, f.s, "refs/heads/m", f.c0)
	mustSetRef(t, f.s, "refs/heads/m", f.c1)
	mustSetRef(t, f.s, "refs/heads/m", f.c2)
	expectOK(t, f.s, "m@{0}", f.c2, "@{0} 当前值 c2")
	expectOK(t, f.s, "m@{1}", f.c1, "@{1} 第1次更早的值 c1")
	expectOK(t, f.s, "m@{2}", f.c0, "@{2} 第2次更早的值 c0")
	expectErr(t, f.s, "m@{3}", Query{}, ErrNoSuchLogEntry, 0, "日志只有3条，@{3} 记录不存在")
	expectOK(t, f.s, "m@{1}~", f.c0, "@{1} 后再向上1代：c1~ = c0")
	expectErr(t, f.s, f.c2[:8]+"@{1}", Query{}, ErrNotARef, 0,
		"标识缩写不是引用，不可用日志")
	expectErr(t, f.s, "m@{0}@{1}", Query{}, ErrNotARef, 1,
		"@{0} 之后结果已是对象而非引用，第二段（下标1）日志导航不可用")
	// 标识前缀与完整标识作为基址
	expectOK(t, f.s, f.c2, f.c2, "完整标识直接命中")
	expectOK(t, f.s, f.c2[:8], f.c2, "唯一前缀缩写命中")
}

// 多段导航中首个失败段的定位。
func TestFirstFailingSegment(t *testing.T) {
	f := newFixture(t)
	expectErr(t, f.s, f.c2+"~1~5", Query{}, ErrBeyondHistory, 1,
		"段0 ~1 成功到 c1；段1 ~5 在 c1 上超出历史，失败段下标为1")
	expectErr(t, f.s, f.c2+"^2^", Query{}, ErrNoSuchParent, 1,
		"段0 ^2 成功到 c0；段1 ^ 在根 c0 上父不存在，失败段下标为1")
	expectErr(t, f.s, f.c2+"^{tree}^", Query{}, ErrTypeNotNavigable, 1,
		"段0 ^{tree} 成功到 t0；段1 ^ 对树不可导航，失败段下标为1")
	expectErr(t, f.s, f.c2+"~9^3", Query{}, ErrBeyondHistory, 0,
		"段0 ~9 已超出历史，首个失败段下标为0，后续段不再求值")
}

// 符号引用的多级解析与成环拒绝。
func TestSymbolicRefs(t *testing.T) {
	f := newFixture(t)
	mustSetRef(t, f.s, "refs/heads/m", f.c0)
	if err := f.s.SetSymbolic("HEAD", "refs/heads/m"); err != nil {
		t.Fatalf("SetSymbolic(HEAD) failed: %v", err)
	}
	if err := f.s.SetSymbolic("refs/heads/alias", "HEAD"); err != nil {
		t.Fatalf("SetSymbolic(alias) failed: %v", err)
	}
	expectOK(t, f.s, "alias", f.c0,
		"alias -> HEAD -> refs/heads/m -> c0，符号引用多级解析")
	expectOK(t, f.s, "HEAD", f.c0, "HEAD -> refs/heads/m -> c0")

	// 成环拒绝：x->y 允许（y 尚不存在，悬空），y->x 会成环，整体拒绝。
	if err := f.s.SetSymbolic("x", "y"); err != nil {
		t.Fatalf("SetSymbolic(x->y) should be allowed (dangling): %v", err)
	}
	err := f.s.SetSymbolic("y", "x")
	if err == nil || err.Kind != ErrSymrefCycle {
		t.Fatalf("SetSymbolic(y->x) -> %v, want symref-cycle", err)
	}
	t.Logf("input=SetSymbolic(y->x) -> err=%q | 判定依据: x->y 已存在，y->x 会成环，整体拒绝", err)
	expectErr(t, f.s, "x", Query{}, ErrNotExist, -1,
		"x->y 而 y 不存在（成环设置被拒绝，未改变状态），解析报不存在")
	expectErr(t, f.s, "y", Query{}, ErrNotExist, -1,
		"y 从未被成功设置，报不存在，证明被拒绝的设置无副作用")

	// 自环同样拒绝。
	err = f.s.SetSymbolic("z", "z")
	if err == nil || err.Kind != ErrSymrefCycle {
		t.Fatalf("SetSymbolic(z->z) -> %v, want symref-cycle", err)
	}
	t.Logf("input=SetSymbolic(z->z) -> err=%q | 判定依据: 自环拒绝", err)
}

// 结果类型约束与自动剥标签；检查在全部导航段完成后进行。
func TestTypeConstraint(t *testing.T) {
	f := newFixture(t)
	expectErr(t, f.s, f.c2, Query{Type: TypeTree}, ErrTypeMismatch, -1,
		"结果是提交，要求树：类型不符")
	expectErr(t, f.s, f.tag1, Query{Type: TypeCommit}, ErrTypeMismatch, -1,
		"结果是标签，要求提交且不允许自动剥标签：类型不符")
	res, err := f.s.Resolve(f.tag1, Query{Type: TypeCommit, AutoPeel: true})
	if err != nil || res.ID != f.c2 {
		t.Fatalf("input=%q AutoPeel -> res=%v err=%v, want c2", f.tag1, res, err)
	}
	t.Logf("input=%q AutoPeel=require-commit -> id=%s | 判定依据: 自动剥去 tag1 得到提交 c2，满足约束",
		f.tag1[:12], res.ID[:12])
	res, err = f.s.Resolve(f.tag2, Query{Type: TypeCommit, AutoPeel: true})
	if err != nil || res.ID != f.c2 {
		t.Fatalf("input=%q AutoPeel -> res=%v err=%v, want c2", f.tag2, res, err)
	}
	t.Logf("input=%q AutoPeel=require-commit -> id=%s | 判定依据: 嵌套标签 tag2->tag1->c2 自动剥至提交",
		f.tag2[:12], res.ID[:12])
	expectErr(t, f.s, f.tag1, Query{Type: TypeTree, AutoPeel: true}, ErrTypeMismatch, -1,
		"自动剥标签后得到提交而非树：类型不符")
	expectOK(t, f.s, f.tag1, f.tag1, "无约束时标签本身即为结果")
	res, err = f.s.Resolve(f.tag1, Query{Type: TypeTag})
	if err != nil || res.ID != f.tag1 {
		t.Fatalf("require tag on tag1 -> res=%v err=%v", res, err)
	}
	t.Logf("input=%q require-tag -> id=%s | 判定依据: 结果类型即标签，满足约束", f.tag1[:12], res.ID[:12])
	// 类型检查在全部导航段完成后进行：先显式 ^{} 再检查。
	res, err = f.s.Resolve(f.tag1+"^{}", Query{Type: TypeCommit})
	if err != nil || res.ID != f.c2 {
		t.Fatalf("tag1^{} require commit -> res=%v err=%v", res, err)
	}
	t.Logf("input=%q require-commit -> id=%s | 判定依据: 导航段 ^{} 完成后结果为提交，满足约束",
		(f.tag1 + "^{}"), res.ID[:12])
}

// 错误次序中每对相邻错误的优先关系（符号引用成环仅发生在设置时，见
// TestSymbolicRefs；其与「引用与标识歧义」分属设置/解析两种调用）。
func TestErrorOrdering(t *testing.T) {
	f := newFixture(t)
	// 构造歧义场景：abcd 既是引用又是两个对象的前缀。
	mustPut(t, f.s, Object{ID: hid("abcd", 1), Type: TypeBlob})
	mustPut(t, f.s, Object{ID: hid("abcd", 2), Type: TypeBlob})
	mustSetRef(t, f.s, "refs/heads/abcd", f.c0)
	mustSetRef(t, f.s, "refs/heads/m", f.c2)

	// 参数非法 < 引用与标识歧义：含非法字符的表达式先报文法错。
	expectErr(t, f.s, "abcd!^{}", Query{}, ErrInvalid, -1,
		"非法字符 '!'：参数非法优先于基址的引用与标识歧义")
	// 参数非法 < 符号引用成环（设置时）：非法名优先于成环检查。
	err := f.s.SetSymbolic("bad name!", "bad name!")
	if err == nil || err.Kind != ErrInvalid {
		t.Fatalf("SetSymbolic(bad name!) -> %v, want invalid", err)
	}
	t.Logf("input=SetSymbolic(bad name!) -> err=%q | 判定依据: 参数非法优先于成环检查", err)
	// 引用与标识歧义 < 前缀歧义：abcd 同时是引用与两个对象的前缀。
	expectErr(t, f.s, "abcd", Query{}, ErrRefIDAmbiguous, -1,
		"abcd 是引用且前缀命中2个对象：报引用与标识歧义而非前缀歧义")
	// 前缀歧义 < 不存在：基址错误优先于任何导航段错误。
	mustPut(t, f.s, Object{ID: hid("beef", 1), Type: TypeBlob})
	mustPut(t, f.s, Object{ID: hid("beef", 2), Type: TypeBlob})
	expectErr(t, f.s, "beef@{1}", Query{}, ErrPrefixAmbiguous, -1,
		"beef 非引用但前缀命中2个对象：报前缀歧义（而非后续段的非引用不可用日志）")
	// 不存在 < 非引用不可用日志：ffff 无对象也无引用。
	expectErr(t, f.s, "ffff@{1}", Query{}, ErrNotExist, -1,
		"ffff 不是引用也无对象匹配：报不存在而非非引用不可用日志")
	// 非引用不可用日志 < 记录不存在：标识基址即使序号超大也先报非引用。
	expectErr(t, f.s, f.c2[:8]+"@{99}", Query{}, ErrNotARef, 0,
		"标识缩写上的 @{99}：报非引用不可用日志而非记录不存在")
	// 记录不存在 < 类型不可导航：首段 @{99} 先失败。
	expectErr(t, f.s, "m@{99}^", Query{}, ErrNoSuchLogEntry, 0,
		"m 的日志只有1条：段0 报记录不存在，段1 的取父不再求值")
	// 类型不可导航 < 父不存在：对文件取第5父先报类型错。
	expectErr(t, f.s, f.b0+"^5", Query{}, ErrTypeNotNavigable, 0,
		"对文件 ^5：报类型不可导航而非父不存在")
	// 父不存在 < 超出历史：段0 ^3 先失败。
	expectErr(t, f.s, f.c2+"^3~99", Query{}, ErrNoSuchParent, 0,
		"段0 ^3 父不存在，优先于段1 的超出历史")
	// 超出历史 < 类型不符：导航错误优先于最终结果类型检查。
	expectErr(t, f.s, f.c2+"~99", Query{Type: TypeBlob}, ErrBeyondHistory, 0,
		"~99 超出历史优先于最终的类型不符检查")
}

// 前缀歧义须附候选个数。
func TestPrefixAmbiguousCandidates(t *testing.T) {
	f := newFixture(t)
	mustPut(t, f.s, Object{ID: hid("cafe", 1), Type: TypeBlob})
	mustPut(t, f.s, Object{ID: hid("cafe", 2), Type: TypeBlob})
	mustPut(t, f.s, Object{ID: hid("cafe", 3), Type: TypeBlob})
	e := expectErr(t, f.s, "cafe", Query{}, ErrPrefixAmbiguous, -1,
		"cafe 前缀命中3个对象：报前缀歧义并附候选个数3")
	if e.Candidates != 3 {
		t.Fatalf("Candidates=%d, want 3", e.Candidates)
	}
	t.Logf("input=%q -> candidates=%d | 判定依据: 候选个数随错误返回", "cafe", e.Candidates)
	expectErr(t, f.s, "dead", Query{}, ErrNotExist, -1,
		"dead 是合法前缀但匹配0个对象且不是引用：报不存在")
}

// 最短唯一缩写随对象库增长只变长不变短，且之前返回过的更长缩写仍然有效。
func TestShortestAbbrevMonotonic(t *testing.T) {
	s := NewService(Config{})
	target := mkid("abcd") // "abcd" + 36 个 0
	mustPut(t, s, Object{ID: target, Type: TypeBlob})
	// 与 target 的 LCP 分别为 2、3、4、5 的对象逐个加入。
	others := []string{mkid("ab"), mkid("abc"), mkid("abcd1"), mkid("abcd01")}
	wantLens := []int{4, 4, 4, 5, 6}
	var abbrevs []string
	record := func(step int) {
		t.Helper()
		abbr, err := s.ShortestAbbrev(target)
		if err != nil {
			t.Fatalf("ShortestAbbrev step %d failed: %v", step, err)
		}
		abbrevs = append(abbrevs, abbr)
		t.Logf("step=%d shortest(%s...)=%q len=%d | 判定依据: 与邻居的最大LCP+1且不小于下限4",
			step, target[:8], abbr, len(abbr))
		if len(abbr) != wantLens[step] {
			t.Fatalf("step %d: len=%d, want %d", step, len(abbr), wantLens[step])
		}
		if len(abbrevs) > 1 && len(abbr) < len(abbrevs[len(abbrevs)-2]) {
			t.Fatalf("step %d: abbrev shrank %q -> %q", step, abbrevs[len(abbrevs)-2], abbr)
		}
	}
	record(0)
	for i, id := range others {
		mustPut(t, s, Object{ID: id, Type: TypeBlob})
		record(i + 1)
	}
	// 之前返回过的不短于当前最短值的缩写仍然唯一有效。
	cur := abbrevs[len(abbrevs)-1]
	for _, abbr := range abbrevs {
		if len(abbr) < len(cur) {
			continue
		}
		expectOK(t, s, abbr, target,
			"库增长后，不短于当前最短缩写的历史缩写仍唯一解析回 target")
	}
}

// 被拒绝的解析不得改变任何缓存、引用或序号。
func TestRejectedParseNoSideEffects(t *testing.T) {
	f := newFixture(t)
	mustSetRef(t, f.s, "refs/heads/m", f.c0)
	mustSetRef(t, f.s, "refs/heads/m", f.c1)
	logBefore := len(f.s.refs.reflog("refs/heads/m"))
	abbrBefore, err := f.s.ShortestAbbrev(f.c2)
	if err != nil {
		t.Fatalf("ShortestAbbrev failed: %v", err)
	}
	bad := []string{
		"", "!!!", f.c2 + "^9", f.c2 + "~9", f.b0 + "^", "m@{9}",
		f.c2[:8] + "@{1}", "zzzz", f.c2 + "^{tree}~1",
	}
	for _, expr := range bad {
		if _, err := f.s.Resolve(expr, Query{Type: TypeCommit}); err == nil {
			t.Fatalf("input=%q -> unexpected success", expr)
		} else {
			t.Logf("input=%q -> err=%q | 判定依据: 拒绝后状态不变", expr, err)
		}
	}
	if got := len(f.s.refs.reflog("refs/heads/m")); got != logBefore {
		t.Fatalf("reflog length changed: %d -> %d", logBefore, got)
	}
	abbrAfter, err := f.s.ShortestAbbrev(f.c2)
	if err != nil || abbrAfter != abbrBefore {
		t.Fatalf("abbrev changed: %q -> %q, err=%v", abbrBefore, abbrAfter, err)
	}
	expectOK(t, f.s, "m@{1}", f.c0, "被拒绝的解析未消费任何日志序号，@{1} 仍指向 c0")
	t.Logf("判定依据: 日志长度 %d 不变、最短缩写 %q 不变、引用值不变", logBefore, abbrBefore)
}

// 写入侧的参数校验。
func TestWriteValidation(t *testing.T) {
	f := newFixture(t)
	if err := f.s.Put(Object{ID: "xyz", Type: TypeBlob}); err == nil || err.Kind != ErrInvalid {
		t.Fatalf("Put(bad id) -> %v, want invalid", err)
	}
	if err := f.s.Put(Object{ID: f.c2, Type: TypeBlob}); err == nil || err.Kind != ErrInvalid {
		t.Fatalf("Put(dup id) -> %v, want invalid", err)
	}
	if err := f.s.Put(Object{ID: hid("99", 1), Type: TypeCommit, Tree: hid("99", 2)}); err == nil || err.Kind != ErrInvalid {
		t.Fatalf("Put(commit, unknown tree) -> %v, want invalid", err)
	}
	if err := f.s.Put(Object{ID: hid("99", 3), Type: TypeTag, Target: hid("99", 4)}); err == nil || err.Kind != ErrInvalid {
		t.Fatalf("Put(tag, unknown target) -> %v, want invalid", err)
	}
	if err := f.s.SetRef("refs/heads/none", hid("99", 5)); err == nil || err.Kind != ErrInvalid {
		t.Fatalf("SetRef(unknown object) -> %v, want invalid", err)
	}
	t.Logf("判定依据: 非法标识、重复标识、悬空引用、未知对象一律报参数非法且不入库")
}
