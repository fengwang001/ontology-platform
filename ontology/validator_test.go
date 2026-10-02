package ontology

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, L, n int) *Validator {
	t.Helper()
	v, err := New(L, n)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func mkCert(id, subj, iss, key, auth string, nb, na int64, ca bool, pl int64) Cert {
	return Cert{
		ID:        []byte(id),
		Subject:   []byte(subj),
		Issuer:    []byte(iss),
		Key:       []byte(key),
		AuthKey:   []byte(auth),
		NotBefore: nb,
		NotAfter:  na,
		IsCA:      ca,
		PathLen:   pl,
	}
}

func mustAdd(t *testing.T, v *Validator, c Cert) {
	t.Helper()
	if err := v.Add(c); err != nil {
		t.Fatalf("Add %s: %v", c.ID, err)
	}
}

func mustTrust(t *testing.T, v *Validator, id string) {
	t.Helper()
	if err := v.Trust([]byte(id)); err != nil {
		t.Fatal(err)
	}
}

func expectOpErr(t *testing.T, err error, kind ErrorKind) *OpError {
	t.Helper()
	var oe *OpError
	if !errors.As(err, &oe) || oe.Kind != kind {
		t.Fatalf("want kind %d, got %v", kind, err)
	}
	return oe
}

func idStrs(r *VerifyResult) []string {
	out := make([]string, len(r.Path))
	for i, b := range r.Path {
		out[i] = string(b)
	}
	return out
}

func eqStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func byteToStr(bb [][]byte) []string {
	out := make([]string, len(bb))
	for i, b := range bb {
		out[i] = string(b)
	}
	return out
}

func simpleChain(t *testing.T, L int) (*Validator, Cert, Cert) {
	v := mustNew(t, L, 50)
	root := mkCert("R", "r", "r", "kr", "kr", 0, 1000, true, -1)
	leaf := mkCert("L", "l", "r", "kl", "kr", 0, 1000, false, -1)
	leaf.SAN = []string{"h.example"}
	mustAdd(t, v, root)
	mustAdd(t, v, leaf)
	mustTrust(t, v, "R")
	return v, root, leaf
}

// 题目主示例：候选次序、回溯、失败归因与考察数。
func TestSpecExample(t *testing.T) {
	v := mustNew(t, 16, 100)
	root := mkCert("A", "ca-root", "ca-root", "kR", "kR", 0, 1000, true, -1)
	i1 := mkCert("I1", "ca-int", "ca-root", "kI", "kR", 0, 500, true, 0)
	i1.Excluded = []string{"example.com"}
	i2 := mkCert("I2", "ca-int", "ca-root", "kI", "kR", 0, 900, true, 0)
	leaf := mkCert("L", "leaf", "ca-int", "kL", "kI", 0, 400, false, -1)
	leaf.SAN = []string{"*.example.com"}
	mustAdd(t, v, root)
	mustAdd(t, v, i1)
	mustAdd(t, v, i2)
	mustAdd(t, v, leaf)
	mustTrust(t, v, "A")

	r, err := v.Verify([]byte("L"), "a.example.com", 100)
	if err != nil {
		t.Fatal(err)
	}
	if r.NoPath || r.Failure != nil {
		t.Fatalf("want success, got noPath=%v fail=%+v", r.NoPath, r.Failure)
	}
	if !eqStr(idStrs(r), []string{"L", "I2", "A"}) {
		t.Fatalf("path = %v", idStrs(r))
	}
	if r.Considered != 2 {
		t.Fatalf("considered = %d, want 2", r.Considered)
	}

	if err := v.Revoke([]byte("I2"), 50); err != nil {
		t.Fatal(err)
	}
	r, err = v.Verify([]byte("L"), "a.example.com", 100)
	if err != nil {
		t.Fatal(err)
	}
	if r.NoPath || r.Failure == nil {
		t.Fatalf("want verify failure, got noPath=%v", r.NoPath)
	}
	if r.Failure.Reason != FailRevoked || r.Failure.Index != 1 {
		t.Fatalf("failure = %+v, want revoked@1", r.Failure)
	}
	if !eqStr(byteToStr(r.Failure.Path), []string{"L", "I2", "A"}) {
		t.Fatalf("failure path = %v", r.Failure.Path)
	}
	if r.Considered != 4 {
		t.Fatalf("considered = %d, want 4", r.Considered)
	}
}

// 有效期边界：NotAfter==now 无效，差 1 有效；NotBefore==now 有效。
func TestValidityEdges(t *testing.T) {
	v := mustNew(t, 4, 10)
	mustAdd(t, v, mkCert("R", "r", "r", "kr", "kr", 0, 1000, true, -1))
	leaf := mkCert("L", "l", "r", "kl", "kr", 100, 200, false, -1)
	leaf.SAN = []string{"h.example"}
	mustAdd(t, v, leaf)
	mustTrust(t, v, "R")
	for _, tc := range []struct {
		now int64
		ok  bool
	}{
		{99, false},
		{100, true},
		{199, true},
		{200, false},
	} {
		r, err := v.Verify([]byte("L"), "h.example", tc.now)
		if err != nil {
			t.Fatal(err)
		}
		// now=99 时终端证书未生效；200 时已过期。
		if tc.ok && (r.Failure != nil || r.NoPath) {
			t.Fatalf("now=%d want ok, got %+v", tc.now, r)
		}
		if !tc.ok && (r.Failure == nil || r.Failure.Reason != FailExpired) {
			t.Fatalf("now=%d want expired, got %+v", tc.now, r.Failure)
		}
	}
}

// 吊销边界与只取较小值；同一证书先有效期后吊销的次序。
func TestRevocation(t *testing.T) {
	v, _, _ := simpleChain(t, 4)
	if err := v.Revoke([]byte("L"), 100); err != nil {
		t.Fatal(err)
	}
	if err := v.Revoke([]byte("L"), 200); err != nil {
		t.Fatal(err)
	}
	r, _ := v.Verify([]byte("L"), "h.example", 150)
	if r.Failure == nil || r.Failure.Reason != FailRevoked || r.Failure.Index != 0 {
		t.Fatalf("want revoked@0, got %+v", r.Failure)
	}
	r, _ = v.Verify([]byte("L"), "h.example", 100)
	if r.Failure == nil || r.Failure.Reason != FailRevoked {
		t.Fatalf("revocation at == now should revoke, got %+v", r.Failure)
	}
	r, _ = v.Verify([]byte("L"), "h.example", 99)
	if r.Failure != nil || r.NoPath {
		t.Fatalf("now=99 should not be revoked, got %+v", r)
	}

	// 同证书同时过期且已吊销：报有效期违规（先查有效期）。
	v2 := mustNew(t, 4, 10)
	mustAdd(t, v2, mkCert("R2", "r2", "r2", "kr2", "kr2", 0, 1000, true, -1))
	l2 := mkCert("L2", "l2", "r2", "kl2", "kr2", 0, 50, false, -1)
	l2.SAN = []string{"h.example"}
	mustAdd(t, v2, l2)
	mustTrust(t, v2, "R2")
	if err := v2.Revoke([]byte("L2"), 10); err != nil {
		t.Fatal(err)
	}
	r, _ = v2.Verify([]byte("L2"), "h.example", 60)
	if r.Failure == nil || r.Failure.Reason != FailExpired {
		t.Fatalf("want expired first, got %+v", r.Failure)
	}
}

// 非 CA 作签发者。
func TestNotCAIssuer(t *testing.T) {
	v := mustNew(t, 4, 10)
	mustAdd(t, v, mkCert("R", "r", "r", "kr", "kr", 0, 1000, true, -1))
	mustAdd(t, v, mkCert("M", "m", "r", "km", "kr", 0, 1000, false, -1))
	leaf := mkCert("L", "l", "m", "kl", "km", 0, 1000, false, -1)
	leaf.SAN = []string{"h.example"}
	mustAdd(t, v, leaf)
	mustTrust(t, v, "R")
	r, _ := v.Verify([]byte("L"), "h.example", 10)
	if r.NoPath || r.Failure == nil || r.Failure.Reason != FailNotCA || r.Failure.Index != 1 {
		t.Fatalf("got %+v", r)
	}
}

// PathLen：0/1 的恰等与超限；信任锚自身 PathLen 参与判定；自签发不计入 m。
func TestPathLen(t *testing.T) {
	build := func(rootPL int64) *Validator {
		v := mustNew(t, 8, 20)
		mustAdd(t, v, mkCert("R", "r", "r", "kr", "kr", 0, 1000, true, rootPL))
		mustAdd(t, v, mkCert("M", "m", "r", "km", "kr", 0, 1000, true, -1))
		leaf := mkCert("L", "l", "m", "kl", "km", 0, 1000, false, -1)
		leaf.SAN = []string{"h.example"}
		mustAdd(t, v, leaf)
		mustTrust(t, v, "R")
		return v
	}

	r, _ := build(0).Verify([]byte("L"), "h.example", 10)
	if r.Failure == nil || r.Failure.Reason != FailPathLen || r.Failure.Index != 2 {
		t.Fatalf("root pl0 want pathlen@2, got %+v", r.Failure)
	}
	r, _ = build(1).Verify([]byte("L"), "h.example", 10)
	if r.Failure != nil || r.NoPath {
		t.Fatalf("root pl1 should pass: %+v", r)
	}

	// 中间 PathLen=0：M 前区间为空，m=0 通过。
	v := mustNew(t, 8, 20)
	mustAdd(t, v, mkCert("R", "r", "r", "kr", "kr", 0, 1000, true, -1))
	mustAdd(t, v, mkCert("M", "m", "r", "km", "kr", 0, 1000, true, 0))
	leaf := mkCert("L", "l", "m", "kl", "km", 0, 1000, false, -1)
	leaf.SAN = []string{"h.example"}
	mustAdd(t, v, leaf)
	mustTrust(t, v, "R")
	r, _ = v.Verify([]byte("L"), "h.example", 10)
	if r.Failure != nil || r.NoPath {
		t.Fatalf("mid pl0 should pass: %+v", r)
	}

	// 两层中间证书时 M(pl=0) 区间含一个非自签证书，m=1 超限。
	v = mustNew(t, 8, 20)
	mustAdd(t, v, mkCert("R", "r", "r", "kr", "kr", 0, 1000, true, -1))
	mustAdd(t, v, mkCert("M1", "m1", "r", "km1", "kr", 0, 1000, true, 0))
	mustAdd(t, v, mkCert("M2", "m2", "m1", "km2", "km1", 0, 1000, true, -1))
	leaf2 := mkCert("L", "l", "m2", "kl", "km2", 0, 1000, false, -1)
	leaf2.SAN = []string{"h.example"}
	mustAdd(t, v, leaf2)
	mustTrust(t, v, "R")
	r, _ = v.Verify([]byte("L"), "h.example", 10)
	if r.Failure == nil || r.Failure.Reason != FailPathLen || r.Failure.Index != 2 {
		t.Fatalf("want pathlen@2 m=1, got %+v", r.Failure)
	}

	// 自签发证书不计入 m：[L,S,M,R]，S 的 Subject==Issuer。
	v = mustNew(t, 8, 20)
	mustAdd(t, v, mkCert("R", "r", "r", "kr", "kr", 0, 1000, true, 1))
	mustAdd(t, v, mkCert("M", "m", "r", "km", "kr", 0, 1000, true, -1))
	mustAdd(t, v, mkCert("S", "m", "m", "km2", "km", 0, 1000, true, -1))
	leaf3 := mkCert("L", "l", "m", "kl", "km2", 0, 1000, false, -1)
	leaf3.SAN = []string{"h.example"}
	mustAdd(t, v, leaf3)
	mustTrust(t, v, "R")
	r, _ = v.Verify([]byte("L"), "h.example", 10)
	if r.Failure != nil || r.NoPath {
		t.Fatalf("self-issued chain should pass: %+v", r.Failure)
	}
	if !eqStr(idStrs(r), []string{"L", "S", "M", "R"}) {
		t.Fatalf("path = %v", idStrs(r))
	}
	// S 候选 NotAfter 同为 1000，ID 序 S<M，先走 S。
}

// 子树约束：标签边界、前导点写法、排除优先于允许。
func TestSubtrees(t *testing.T) {
	cases := []struct {
		name      string
		excluded  []string
		permitted []string
		want      FailReason // 0 表示通过
	}{
		{"example.com", nil, []string{"example.com"}, 0},
		{"a.example.com", nil, []string{"example.com"}, 0},
		{"notexample.com", nil, []string{"example.com"}, FailNotPermitted},
		{"example.com", nil, []string{".example.com"}, FailNotPermitted},
		{"a.example.com", nil, []string{".example.com"}, 0},
		{"a.example.com", []string{"example.com"}, []string{"example.com"}, FailExcluded},
		{"other.com", []string{".example.com"}, []string{"example.com"}, FailNotPermitted},
		{"a.example.com", []string{".example.com"}, []string{"example.com"}, FailExcluded},
	}
	for _, tc := range cases {
		v := mustNew(t, 4, 10)
		mustAdd(t, v, mkCert("R", "r", "r", "kr", "kr", 0, 1000, true, -1))
		mid := mkCert("M", "m", "r", "km", "kr", 0, 1000, true, -1)
		mid.Excluded = tc.excluded
		mid.Permitted = tc.permitted
		mustAdd(t, v, mid)
		leaf := mkCert("L", "l", "m", "kl", "km", 0, 1000, false, -1)
		leaf.SAN = []string{tc.name}
		mustAdd(t, v, leaf)
		mustTrust(t, v, "R")
		r, _ := v.Verify([]byte("L"), tc.name, 10)
		if tc.want == 0 {
			if r.Failure != nil || r.NoPath {
				t.Fatalf("%q want ok, got %+v", tc.name, r)
			}
			continue
		}
		if r.Failure == nil || r.Failure.Reason != tc.want {
			t.Fatalf("%q want reason %d, got %+v", tc.name, tc.want, r.Failure)
		}
	}
}

// 通配 SAN 只匹配恰好一个非点标签；非通配须逐字节相等。
func TestSANWildcard(t *testing.T) {
	v2 := mustNew(t, 4, 10)
	mustAdd(t, v2, mkCert("R", "r", "r", "kr", "kr", 0, 1000, true, -1))
	leaf := mkCert("L", "l", "r", "kl", "kr", 0, 1000, false, -1)
	leaf.SAN = []string{"*.example.com"}
	mustAdd(t, v2, leaf)
	mustTrust(t, v2, "R")
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"a.example.com", true},
		{"example.com", false},
		{"a.b.example.com", false},
	} {
		r, err := v2.Verify([]byte("L"), tc.name, 10)
		if err != nil {
			t.Fatal(err)
		}
		if tc.ok != (r.Failure == nil && !r.NoPath) {
			t.Fatalf("%q ok=%v got %+v", tc.name, tc.ok, r.Failure)
		}
	}
}

// 候选并列 NotAfter 时按 ID 升序；回溯到第二候选后通过。
func TestOrderingAndBacktrack(t *testing.T) {
	v := mustNew(t, 4, 20)
	mustAdd(t, v, mkCert("R", "r", "r", "kr", "kr", 0, 1000, true, -1))
	// 两张同 Subject/Key 的中间证书，NotAfter 相同，ID 升序：bad < good。
	bad := mkCert("bad", "m", "r", "km", "kr", 0, 1000, true, -1)
	bad.Excluded = []string{"example.com"}
	good := mkCert("good", "m", "r", "km", "kr", 0, 1000, true, -1)
	mustAdd(t, v, bad)
	mustAdd(t, v, good)
	leaf := mkCert("L", "l", "m", "kl", "km", 0, 1000, false, -1)
	leaf.SAN = []string{"h.example.com"}
	mustAdd(t, v, leaf)
	mustTrust(t, v, "R")
	r, _ := v.Verify([]byte("L"), "h.example.com", 10)
	if r.Failure != nil || r.NoPath {
		t.Fatalf("backtrack should succeed via good: %+v", r.Failure)
	}
	if !eqStr(idStrs(r), []string{"L", "good", "R"}) {
		t.Fatalf("path = %v", idStrs(r))
	}
	// bad、R（bad 分支）、good、R 各考察一次。
	if r.Considered != 4 {
		t.Fatalf("considered = %d, want 4", r.Considered)
	}
}

// 交叉签名成环不死循环；同一 ID 不重复出现。
func TestCrossSignLoop(t *testing.T) {
	v := mustNew(t, 16, 20)
	// A、B 互相以对方公钥签发，Subject 各自不同，形成候选环。
	a := mkCert("A", "sa", "sb", "ka", "kb", 0, 1000, true, -1)
	b := mkCert("B", "sb", "sa", "kb", "ka", 0, 1000, true, -1)
	mustAdd(t, v, a)
	mustAdd(t, v, b)
	mustTrust(t, v, "B")
	leaf := mkCert("L", "sl", "sa", "kl", "ka", 0, 1000, false, -1)
	leaf.SAN = []string{"h.example"}
	mustAdd(t, v, leaf)
	// 路径 L -> A -> B(锚)。
	r, err := v.Verify([]byte("L"), "h.example", 10)
	if err != nil {
		t.Fatal(err)
	}
	if r.Failure != nil || r.NoPath {
		t.Fatalf("want path via B anchor: %+v", r)
	}
	if !eqStr(idStrs(r), []string{"L", "A", "B"}) {
		t.Fatalf("path = %v", idStrs(r))
	}

	// 无锚版本：环被同一 ID 规则终止，报无路径。
	v2 := mustNew(t, 16, 20)
	mustAdd(t, v2, a)
	mustAdd(t, v2, b)
	leaf2 := mkCert("L2", "sl", "sa", "kl2", "ka", 0, 1000, false, -1)
	leaf2.SAN = []string{"h.example"}
	mustAdd(t, v2, leaf2)
	r, _ = v2.Verify([]byte("L2"), "h.example", 10)
	if !r.NoPath {
		t.Fatalf("want no path in anchorless loop, got %+v", r)
	}
}

// 链长恰为 L 可成路径；L+1 的分支被剪掉。
func TestChainLength(t *testing.T) {
	build := func(L int) (*Validator, [][]byte) {
		v := mustNew(t, L, 50)
		// R <- C1 <- C2 <- Leaf，共 4 张。
		mustAdd(t, v, mkCert("R", "s0", "s0", "k0", "k0", 0, 1000, true, -1))
		mustAdd(t, v, mkCert("C1", "s1", "s0", "k1", "k0", 0, 1000, true, -1))
		mustAdd(t, v, mkCert("C2", "s2", "s1", "k2", "k1", 0, 1000, true, -1))
		leaf := mkCert("Leaf", "s3", "s2", "k3", "k2", 0, 1000, false, -1)
		leaf.SAN = []string{"h.example"}
		mustAdd(t, v, leaf)
		mustTrust(t, v, "R")
		return v, nil
	}
	v, _ := build(4)
	r, _ := v.Verify([]byte("Leaf"), "h.example", 10)
	if r.Failure != nil || r.NoPath {
		t.Fatalf("length == L should pass: %+v", r)
	}
	v, _ = build(3)
	r, _ = v.Verify([]byte("Leaf"), "h.example", 10)
	if !r.NoPath {
		t.Fatalf("length L+1 branch should be pruned, got %+v", r)
	}
	// C2、C1 深入；C1 处取出 R 计一次后因超长剪掉：共 3 次。
	if r.Considered != 3 {
		t.Fatalf("considered = %d, want 3", r.Considered)
	}
}

// 终端证书本身是信任锚：不再取候选，考察数为 0。
func TestLeafIsAnchor(t *testing.T) {
	v, _, _ := simpleChain(t, 4)
	mustTrust(t, v, "L")
	r, _ := v.Verify([]byte("L"), "h.example", 10)
	if r.Failure != nil || r.NoPath {
		t.Fatalf("anchor leaf: %+v", r)
	}
	if !eqStr(idStrs(r), []string{"L"}) || r.Considered != 0 {
		t.Fatalf("path=%v considered=%d", idStrs(r), r.Considered)
	}
	// 终端锚 SAN 不符仍报 SAN 失败。
	r, _ = v.Verify([]byte("L"), "other.example", 10)
	if r.Failure == nil || r.Failure.Reason != FailSAN {
		t.Fatalf("anchor leaf SAN fail: %+v", r)
	}
}

// 拒绝分类与顺序；被拒绝操作不改变任何状态。
func TestRejectionsAndState(t *testing.T) {
	if _, err := New(0, 10); expectOpErr(t, err, KindInvalidConfig).Op != "New" {
		t.Fatal("New L=0 must fail")
	}
	if _, err := New(16, 0); err == nil {
		t.Fatal("New Nmax=0 must fail")
	}
	if _, err := New(17, 10); err == nil {
		t.Fatal("New L=17 must fail")
	}

	v := mustNew(t, 4, 2)
	mustAdd(t, v, mkCert("A", "a", "a", "ka", "ka", 0, 1000, true, -1))
	// 冲突先于超限：A 已存在，且容量将满，仍报冲突。
	err := v.Add(mkCert("A", "a", "a", "ka", "ka", 0, 1000, true, -1))
	expectOpErr(t, err, KindConflict)
	mustAdd(t, v, mkCert("B", "b", "b", "kb", "kb", 0, 1000, true, -1))
	err = v.Add(mkCert("C", "c", "c", "kc", "kc", 0, 1000, true, -1))
	expectOpErr(t, err, KindLimitExceeded)

	// 不存在。
	expectOpErr(t, v.Trust([]byte("X")), KindNotFound)
	expectOpErr(t, v.Revoke([]byte("X"), 1), KindNotFound)
	_, err = v.Verify([]byte("X"), "h.example", 1)
	expectOpErr(t, err, KindNotFound)

	// 非法字段。
	bad := mkCert("Z", "z", "z", "kz", "kz", 5, 4, true, -1)
	expectOpErr(t, v.Add(bad), KindInvalidConfig)
	bad = mkCert("Z", "", "z", "kz", "kz", 0, 4, true, -1)
	expectOpErr(t, v.Add(bad), KindInvalidConfig)
	bad = mkCert("Z", "z", "z", "kz", "kz", 0, 4, false, 0)
	expectOpErr(t, v.Add(bad), KindInvalidConfig)
	bad = mkCert("Z", "z", "z", "kz", "kz", 0, 4, false, -1)
	bad.Excluded = []string{".example.com"}
	expectOpErr(t, v.Add(bad), KindInvalidConfig)
	bad = mkCert("Z", "z", "z", "kz", "kz", 0, 4, false, -1)
	bad.SAN = []string{"a..example.com"}
	expectOpErr(t, v.Add(bad), KindInvalidConfig)
	bad = mkCert("Z", "z", "z", "kz", "kz", 0, 4, false, -1)
	bad.SAN = []string{"*.example..com"}
	expectOpErr(t, v.Add(bad), KindInvalidConfig)
	expectOpErr(t, v.Revoke([]byte("A"), -1), KindInvalidConfig)

	// 非法 name / now（leaf 不存在时仍先查 name/now 合法性？按规则 Verify 入参先校验）。
	_, err = v.Verify([]byte("A"), "Bad.Name", 0)
	expectOpErr(t, err, KindInvalidConfig)
	_, err = v.Verify([]byte("A"), "ok.example", 1_000_000_000_000_001)
	expectOpErr(t, err, KindInvalidConfig)

	// 状态未被失败操作污染：容量仍为 2，A 未被吊销/信任。
	r, err := v.Verify([]byte("A"), "x.example", 10)
	if err != nil {
		t.Fatal(err)
	}
	// A 不是信任锚且无候选 => 无路径；考察数 0。
	if !r.NoPath || r.Considered != 0 {
		t.Fatalf("state mutated by rejected ops: %+v", r)
	}
}

// 登记一万张 Subject 互不相同、与验证无关的证书，考察数保持不变。
func TestConsideredStable(t *testing.T) {
	v := mustNew(t, 16, 20_000)
	mustAdd(t, v, mkCert("A", "ca-root", "ca-root", "kR", "kR", 0, 1000, true, -1))
	mustAdd(t, v, mkCert("I", "ca-int", "ca-root", "kI", "kR", 0, 900, true, -1))
	leaf := mkCert("L", "leaf", "ca-int", "kL", "kI", 0, 400, false, -1)
	leaf.SAN = []string{"h.example.com"}
	mustAdd(t, v, leaf)
	mustTrust(t, v, "A")

	r0, _ := v.Verify([]byte("L"), "h.example.com", 100)
	if r0.Considered != 2 {
		t.Fatalf("baseline considered = %d", r0.Considered)
	}
	for i := 0; i < 10_000; i++ {
		id := "noise-" + itoa(i)
		mustAdd(t, v, mkCert(id, "noise-subj-"+itoa(i), "noise-subj-"+itoa(i), id, id, 0, 1000, true, -1))
	}
	r1, _ := v.Verify([]byte("L"), "h.example.com", 100)
	if r1.Considered != r0.Considered {
		t.Fatalf("considered changed: before=%d after=%d", r0.Considered, r1.Considered)
	}
	if !eqStr(idStrs(r1), []string{"L", "I", "A"}) {
		t.Fatalf("path = %v", idStrs(r1))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
