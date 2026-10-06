package revision

import (
	"testing"
)

// testStore 构造一个对象库已就绪、命名空间次序固定的服务供多数用例使用。
//
// 对象拓扑（标识用前缀缩写表示）：
//
//	11.. 两棵树；21.. 文件；33.. 根提交(树11)；32.. 提交(父33)；
//	31.. 合并提交(父32,33，树12)；41.. 标签->31；42.. 标签->41
func testStore(t *testing.T, minAbbrev int) *Store {
	t.Helper()
	s := NewStore(Config{MinAbbrev: minAbbrev}, []string{"refs/tags/", "refs/heads/", ""})
	t1, t2, b1 := idOf("11"), idOf("12"), idOf("21")
	c1, c2, c3 := idOf("31"), idOf("32"), idOf("33")
	g1, g2 := idOf("41"), idOf("42")
	mustAdd := func(o Object) {
		if err := s.AddObject(o); err != nil {
			t.Fatalf("add %s: %v", o.ID, err)
		}
	}
	mustAdd(Object{ID: t1, Type: TypeTree})
	mustAdd(Object{ID: t2, Type: TypeTree})
	mustAdd(Object{ID: b1, Type: TypeBlob})
	mustAdd(Object{ID: c3, Type: TypeCommit, Tree: t1})
	mustAdd(Object{ID: c2, Type: TypeCommit, Tree: t1, Parents: []string{c3}})
	mustAdd(Object{ID: c1, Type: TypeCommit, Tree: t2, Parents: []string{c2, c3}})
	mustAdd(Object{ID: g1, Type: TypeTag, Target: c1})
	mustAdd(Object{ID: g2, Type: TypeTag, Target: g1})
	if err := s.SetRef("refs/tags/v1", g1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRef("refs/heads/main", c1); err != nil {
		t.Fatal(err)
	}
	return s
}

func zeroID(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}

func idOf(prefix string) string { return prefix + zeroID(IDFullLen-len(prefix)) }

func logf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf(format, args...)
}

func expectErr(t *testing.T, name, input string, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("[%s] input=%q actual=success, want error code %d", name, input, want)
	}
	re, ok := err.(*ResolutionError)
	if !ok || re.Code != want {
		t.Fatalf("[%s] input=%q actual=%v, want error code %d", name, input, err, want)
	}
	logf(t, "[%s] input=%q actual=错误码%d(seg=%d,cand=%d) 依据=期望裁决码 %d 且相符",
		name, input, re.Code, re.SegIndex, re.Candidates, want)
}

func expectOK(t *testing.T, name, input string, res Result, wantIDPrefix string) {
	t.Helper()
	if !startsWith(res.Object.ID, wantIDPrefix) {
		t.Fatalf("[%s] input=%q actual=%s, want id prefix %q",
			name, input, res.Object.ID, wantIDPrefix)
	}
	logf(t, "[%s] input=%q actual=%s(%s) fromRef=%q 依据=结果对象标识以 %q 开头",
		name, input, res.Object.ID, typeName(res.Object.Type), res.FromRef, wantIDPrefix)
}

func startsWith(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func mustResolve(t *testing.T, s *Store, expr string, opt ResolveOption) Result {
	t.Helper()
	res, err := s.Resolve(expr, opt)
	if err != nil {
		t.Fatalf("resolve %q: %v", expr, err)
	}
	return res
}
