package revision

import (
	"testing"
)

func mustResolveResult(t *testing.T, s *Store, expr string) Result {
	t.Helper()
	return mustResolve(t, s, expr, ResolveOption{})
}

// 用例 1：全名直接命中优先于命名空间补全。
func TestExactFullNameBeatsNamespace(t *testing.T) {
	s := testStore(t, 4)
	if err := s.SetRef("main", idOf("21")); err != nil {
		t.Fatal(err)
	}
	res, err := s.Resolve("main", ResolveOption{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FromRef != "main" || res.Object.ID != idOf("21") {
		t.Fatalf("actual fromRef=%q id=%s, 依据=全名 main 应直接命中 b1", res.FromRef, res.Object.ID)
	}
	logf(t, "[全名优先] input=%q actual=%s fromRef=%q 依据=全名精确命中优先于 refs/heads/ 补全",
		"main", res.Object.ID, res.FromRef)
}

// 用例 2：命名空间次序——先 tags/ 后 heads/，同名短名首个命中者胜出。
func TestNamespaceOrder(t *testing.T) {
	s := testStore(t, 4)
	if err := s.SetRef("refs/tags/x", idOf("21")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRef("refs/heads/x", idOf("11")); err != nil {
		t.Fatal(err)
	}
	res := mustResolve(t, s, "x", ResolveOption{})
	if res.Object.ID != idOf("21") {
		t.Fatalf("actual=%s, 依据=次序 tags 在 heads 前，x 应命中 refs/tags/x->b1", res.Object.ID)
	}
	logf(t, "[命名空间次序] input=%q actual=%s 依据=配置次序 tags/ 先于 heads/", "x", res.Object.ID)
}

// 用例 3：引用与标识歧义，以及全名直接命中豁免。
func TestRefIDAmbiguityAndExactExemption(t *testing.T) {
	s := testStore(t, 1)
	// refs/tags/31 经命名空间补全命中，而 31 又是提交标识前缀 => 歧义。
	if err := s.SetRef("refs/tags/31", idOf("21")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Resolve("31", ResolveOption{})
	expectErr(t, "引用标识歧义", "31", err, ErrRefIDAmbiguous)

	// 全名 "33" 恰好存在 => 豁免歧义，直接按引用解释。
	if err := s.SetRef("33", idOf("21")); err != nil {
		t.Fatal(err)
	}
	res, err := s.Resolve("33", ResolveOption{})
	if err != nil || res.Object.ID != idOf("21") {
		t.Fatalf("actual=%v,%s 依据=全名 33 豁免歧义", err, res.Object.ID)
	}
	logf(t, "[全名豁免] input=%q actual=%s 依据=短名恰为全名，歧义豁免", "33", res.Object.ID)
}

// 用例 4：低于下限的缩写只按引用解释，不参与标识匹配。
func TestBelowMinAbbrevRefOnly(t *testing.T) {
	s := testStore(t, 4)
	_, err := s.Resolve("c", ResolveOption{})
	expectErr(t, "下限以下", "c", err, ErrNotFound)

	if err := s.SetRef("refs/heads/c", idOf("31")); err != nil {
		t.Fatal(err)
	}
	res := mustResolve(t, s, "c", ResolveOption{})
	expectOK(t, "下限以下但引用命中", "c", res, "31")
}

// 用例 5：前缀歧义附候选个数；唯一前缀命中；完整标识命中。
func TestPrefixAmbiguity(t *testing.T) {
	s := testStore(t, 1)
	_, err := s.Resolve("3", ResolveOption{})
	re, _ := err.(*ResolutionError)
	if !IsCode(err, ErrPrefixAmbiguous) || re.Candidates != 3 {
		t.Fatalf("actual=%v cand=%d 依据=31/32/33 三个对象以前缀 3 开头", err, re.Candidates)
	}
	logf(t, "[前缀歧义] input=%q actual=候选%d 依据=31,32,33 均以 3 开头", "3", re.Candidates)

	res := mustResolve(t, s, "2", ResolveOption{})
	expectOK(t, "唯一前缀", "2", res, "21")

	// 长度低于下限但引用不存在 => 不存在（不进入标识匹配）。
	s2 := testStore(t, 40)
	_, err = s2.Resolve("31", ResolveOption{})
	expectErr(t, "高下限不存在", "31", err, ErrNotFound)
	res = mustResolve(t, s2, idOf("31"), ResolveOption{})
	expectOK(t, "完整标识", idOf("31"), res, "31")
}

// 用例 6：每种导航段的合法与非法类型。
func TestNavigationSegments(t *testing.T) {
	s := testStore(t, 2)
	// ^ 第一父；^2 第二父；父数不足。
	expectOK(t, "第一父", "main^", mustResolveResult(t, s, "main^"), "32")
	expectOK(t, "第二父", "main^2", mustResolveResult(t, s, "main^2"), "33")
	_, err := s.Resolve("main^3", ResolveOption{})
	expectErr(t, "父不存在", "main^3", err, ErrParentMissing)

	// ~N 沿第一父；根提交再向上 => 超出历史。
	expectOK(t, "向上两代", "main~2", mustResolveResult(t, s, "main~2"), "33")
	_, err = s.Resolve("main~3", ResolveOption{})
	expectErr(t, "超出历史", "main~3", err, ErrBeyondHistory)

	// 对非提交取父/向上 => 类型不可导航。
	_, err = s.Resolve(idOf("11")+"^", ResolveOption{})
	expectErr(t, "树取父", idOf("11")+"^", err, ErrNotNavigable)
	_, err = s.Resolve(idOf("21")+"~1", ResolveOption{})
	expectErr(t, "文件向上", idOf("21")+"~1", err, ErrNotNavigable)

	// ^{} 剥嵌套标签；对非标签无副作用成功。
	expectOK(t, "剥标签", "v1^{}", mustResolveResult(t, s, "v1^{}"), "31")
	expectOK(t, "对提交剥无副作用", "main^{}", mustResolveResult(t, s, "main^{}"), "31")
	if err := s.SetRef("refs/tags/nested", idOf("42")); err != nil {
		t.Fatal(err)
	}
	expectOK(t, "嵌套标签剥尽", "nested^{}^{}", mustResolveResult(t, s, "nested^{}^{}"), "31")

	// ^{tree} 合法与非法。
	expectOK(t, "取树", "main^{tree}", mustResolveResult(t, s, "main^{tree}"), "12")
	_, err = s.Resolve("v1^{tree}", ResolveOption{})
	expectErr(t, "标签取树", "v1^{tree}", err, ErrNotNavigable)
	_, err = s.Resolve(idOf("21")+"^{tree}", ResolveOption{})
	expectErr(t, "文件取树", idOf("21")+"^{tree}", err, ErrNotNavigable)
}

// 用例 7：reflog 合法、越界、对标识缩写使用、日志后再日志。
func TestReflog(t *testing.T) {
	s := testStore(t, 4)
	if err := s.SetRef("refs/heads/main", idOf("32")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRef("refs/heads/main", idOf("33")); err != nil {
		t.Fatal(err)
	}
	expectOK(t, "日志当前", "main@{0}", mustResolveResult(t, s, "main@{0}"), "33")
	expectOK(t, "日志前1", "main@{1}", mustResolveResult(t, s, "main@{1}"), "32")
	expectOK(t, "日志前2", "main@{2}", mustResolveResult(t, s, "main@{2}"), "31")
	_, err := s.Resolve("main@{3}", ResolveOption{})
	expectErr(t, "记录不存在", "main@{3}", err, ErrReflogMissing)

	_, err = s.Resolve(idOf("31")+"@{1}", ResolveOption{})
	expectErr(t, "标识用日志", idOf("31")+"@{1}", err, ErrReflogOnID)

	// 日志解析后可继续对象导航；日志后再日志则按非引用裁决。
	expectOK(t, "日志后取父", "main@{1}^", mustResolveResult(t, s, "main@{1}^"), "33")
	_, err = s.Resolve("main@{0}@{0}", ResolveOption{})
	expectErr(t, "日志后日志", "main@{0}@{0}", err, ErrReflogOnID)
}

// 用例 8：多段导航首个失败段定位。
func TestFirstFailingSegment(t *testing.T) {
	s := testStore(t, 2)
	_, err := s.Resolve("main^^3^{}", ResolveOption{})
	re, _ := err.(*ResolutionError)
	if !IsCode(err, ErrParentMissing) || re.SegIndex != 1 {
		t.Fatalf("actual=%v seg=%d 依据=首个失败段是下标1的^3", err, re.SegIndex)
	}
	logf(t, "[首败段定位] input=%q actual=seg%d 依据=段0成功, 段1父不足, 段2不求值",
		"main^^3^{}", re.SegIndex)

	// 文法错误段定位：^0 非法正整数，下标 1。
	_, err = s.Resolve("main^^0", ResolveOption{})
	re, _ = err.(*ResolutionError)
	if !IsCode(err, ErrInvalid) || re.SegIndex != 1 {
		t.Fatalf("actual=%v seg=%d 依据=^0 文法错误位于段1", err, re.SegIndex)
	}
	logf(t, "[文法错误段定位] input=%q actual=seg%d 依据=^0 不是正整数",
		"main^^0", re.SegIndex)
}

// 用例 9：符号引用多级解析与成环拒绝（拒绝不改变状态）。
func TestSymbolicRefs(t *testing.T) {
	s := testStore(t, 4)
	if err := s.SetSymbolicRef("HEAD", "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSymbolicRef("refs/heads/alias", "HEAD"); err != nil {
		t.Fatal(err)
	}
	expectOK(t, "多级符号", "alias", mustResolveResult(t, s, "alias"), "31")

	err := s.SetSymbolicRef("refs/heads/main", "refs/heads/alias")
	expectErr(t, "成环拒绝", "symbolic refs/heads/main->alias", err, ErrSymbolicCycle)
	res := mustResolve(t, s, "refs/heads/main", ResolveOption{})
	if res.Object.ID != idOf("31") {
		t.Fatalf("actual=%s 依据=成环拒绝不得改动既有引用", res.Object.ID)
	}
	logf(t, "[成环不改变状态] actual=%s 依据=被拒绝设置后 main 仍解析到 31", res.Object.ID)

	err = s.SetSymbolicRef("HEAD", "HEAD")
	expectErr(t, "自指拒绝", "HEAD->HEAD", err, ErrSymbolicCycle)

	// 指向不存在目标的符号引用解析时按不存在裁决，设置本身允许。
	if err := s.SetSymbolicRef("dangling", "refs/heads/nope"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Resolve("dangling", ResolveOption{})
	expectErr(t, "悬空符号", "dangling", err, ErrNotFound)
}

// 用例 10：结果类型约束与自动剥标签。
func TestResultTypeConstraint(t *testing.T) {
	s := testStore(t, 4)
	if err := s.SetRef("refs/tags/nested", idOf("42")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Resolve("v1", ResolveOption{Require: true, WantType: TypeCommit})
	expectErr(t, "标签不符commit", "v1", err, ErrTypeMismatch)

	res, err := s.Resolve("v1", ResolveOption{Require: true, WantType: TypeCommit, AutoPeel: true})
	if err != nil {
		t.Fatal(err)
	}
	expectOK(t, "自动剥后达标", "v1(autopeel)", res, "31")

	// 要求文件：剥到底是提交 => 仍类型不符。
	_, err = s.Resolve("nested", ResolveOption{Require: true, WantType: TypeBlob, AutoPeel: true})
	expectErr(t, "剥后仍不符", "nested->blob", err, ErrTypeMismatch)

	// 不要求类型时标签原样返回；要求标签且本身为标签则成功。
	res = mustResolveResult(t, s, "nested")
	if res.Object.Type != TypeTag || res.Object.ID != idOf("42") {
		t.Fatalf("actual=%s 依据=无约束时不自动剥标签", res.Object.ID)
	}
	logf(t, "[无约束不剥标签] actual=%s(%s) 依据=剥标签只在显式导航或 AutoPeel 时发生",
		res.Object.ID, typeName(res.Object.Type))
}

// 用例 11：最短缩写随库增长只变长，历史缩写仍有效，下限生效。
func TestShortestAbbrevMonotone(t *testing.T) {
	s := NewStore(Config{MinAbbrev: 1}, nil)
	a := "12ab" + zeroID(36)
	if err := s.AddObject(Object{ID: a, Type: TypeBlob}); err != nil {
		t.Fatal(err)
	}
	ab1, ok := s.ShortestAbbrev(a)
	if !ok || ab1 != "1" {
		t.Fatalf("actual=%q 依据=单对象时最短缩写为下限1", ab1)
	}
	logf(t, "[缩写初始] actual=%q 依据=唯一对象, 取下限1", ab1)

	b := "12cd" + zeroID(36)
	if err := s.AddObject(Object{ID: b, Type: TypeBlob}); err != nil {
		t.Fatal(err)
	}
	ab2, _ := s.ShortestAbbrev(a)
	if ab2 != "12a" {
		t.Fatalf("actual=%q 依据=与 12cd 共前缀 12, 需要 12a 才唯一", ab2)
	}
	logf(t, "[缩写变长] actual=%q 依据=新增 12cd 后公共前缀增至 12", ab2)
	if len(ab2) < len(ab1) {
		t.Fatal("缩写长度不得随增长变短")
	}
	res := mustResolve(t, s, ab2, ResolveOption{})
	if res.Object.ID != a {
		t.Fatalf("历史缩写 %q 不再有效", ab2)
	}
	logf(t, "[历史缩写仍有效] abbrev=%q actual=%s 依据=只增对象不会让旧唯一前缀变多义",
		ab2, res.Object.ID)

	s2 := NewStore(Config{MinAbbrev: 7}, nil)
	if err := s2.AddObject(Object{ID: a, Type: TypeBlob}); err != nil {
		t.Fatal(err)
	}
	ab3, _ := s2.ShortestAbbrev(a)
	if ab3 != "12ab000" {
		t.Fatalf("actual=%q 依据=下限7, 即使唯一也不得短于7", ab3)
	}
	logf(t, "[下限生效] actual=%q 依据=MinAbbrev=7", ab3)

	// 未知对象返回 false。
	if _, ok := s2.ShortestAbbrev(idOf("ffff")); ok {
		t.Fatal("未知对象不应产生缩写")
	}
}

// 用例 12：错误次序——每对相邻裁决码的优先关系。
func TestErrorOrdering(t *testing.T) {
	// 每个 pair 构造一个"同时具备两种错误条件"的输入，断言只报更靠前的码。
	s := testStore(t, 1)
	_ = s.SetRef("refs/tags/31", idOf("21"))

	// Invalid vs 任何：非法文法即使同时不存在，先报 Invalid。
	check := func(name string, expr string, want ErrorCode) {
		_, err := s.Resolve(expr, ResolveOption{Require: true, WantType: TypeBlob})
		expectErr(t, name, expr, err, want)
	}
	check("非法先于不存在", "bad name", ErrInvalid)

	// RefID 歧义 vs 前缀歧义：refs/tags/3 命中且 3 前缀匹配 3 个对象。
	_ = s.SetRef("refs/tags/3", idOf("21"))
	_, err := s.Resolve("3", ResolveOption{})
	expectErr(t, "引用标识歧义先于前缀歧义", "3", err, ErrRefIDAmbiguous)

	// 前缀歧义 vs 不存在：多义前缀报前缀歧义（不存在无从构造对照，
	// 用同库对照：唯一缺失走不存在，多义走前缀歧义）。
	_, err = s.Resolve("9", ResolveOption{})
	expectErr(t, "不存在裁决", "9", err, ErrNotFound)

	// ReflogOnID vs ReflogMissing：标识+日志 => 非引用优先于记录号越界。
	_, err = s.Resolve(idOf("31")+"@{99}", ResolveOption{})
	expectErr(t, "非引用日志先于记录缺失", idOf("31")+"@{99}", err, ErrReflogOnID)

	// 记录缺失 vs 类型不可导航：main@{3} 越界（即便后面接 ^）。
	_, err = s.Resolve("main@{3}^", ResolveOption{})
	expectErr(t, "记录缺失先于不可导航", "main@{3}^", err, ErrReflogMissing)

	// 类型不可导航 vs 父不存在：树对象 ^3（既是错误类型又父号越界）。
	_, err = s.Resolve(idOf("11")+"^3", ResolveOption{})
	expectErr(t, "不可导航先于父不存在", idOf("11")+"^3", err, ErrNotNavigable)

	// 父不存在 vs 超出历史：c2 只有一父，^2 => 父不存在（对照 ~2 超出历史）。
	_, err = s.Resolve(idOf("32")+"^2", ResolveOption{})
	expectErr(t, "父不存在裁决", idOf("32")+"^2", err, ErrParentMissing)
	_, err = s.Resolve(idOf("32")+"~2", ResolveOption{})
	expectErr(t, "超出历史裁决", idOf("32")+"~2", err, ErrBeyondHistory)

	// 超出历史 vs 类型不符：main~3 越界且要求 tree，先报超出历史。
	_, err = s.Resolve("main~3", ResolveOption{Require: true, WantType: TypeTree})
	expectErr(t, "超出历史先于类型不符", "main~3+require tree", err, ErrBeyondHistory)

	// 合法导航后类型不符排在最后。
	_, err = s.Resolve("main^{tree}", ResolveOption{Require: true, WantType: TypeCommit})
	expectErr(t, "类型不符最后", "main^{tree}+require commit", err, ErrTypeMismatch)

	// 非法空文本与非法字符。
	_, err = s.Resolve("   ", ResolveOption{})
	expectErr(t, "空文本", "'   '", err, ErrInvalid)
	_, err = s.Resolve("ma|in", ResolveOption{})
	expectErr(t, "非法字符", "ma|in", err, ErrInvalid)
}
