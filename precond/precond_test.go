package precond_test

import (
	"errors"
	"ontology/etag"
	"ontology/httpdate"
	"ontology/precond"
	"testing"
	"time"
)

func date(s string) time.Time {
	t, _ := httpdate.Parse(s)
	return t
}

var (
	equal, yesterday = `Tue, 03 Jan 2006 15:04:05 GMT`, `Mon, 02 Jan 2006 15:04:05 GMT`
	res              = precond.Resource{Exists: true, ETag: `"a"`, LastMod: date(equal)}
	now              = func() time.Time { return date(`Wed, 04 Jan 2006 15:04:05 GMT`) }
)

type R = precond.Request

var C, N, F = precond.Continue, precond.NotModified, precond.Failed

func ev(q R) precond.Result { return precond.Evaluate(res, q, now) }
func want(t *testing.T, q R, w precond.Result) {
	t.Helper()
	if got := ev(q); got != w {
		t.Errorf("%+v: got %v want %v", q, got, w)
	}
}
func TestCompare(t *testing.T) { // 强弱比较四种组合
	w, s := etag.Tag{Weak: true, Value: "x"}, etag.Tag{Value: "x"}
	if etag.StrongEqual(w, s) || etag.StrongEqual(w, w) || !etag.StrongEqual(s, s) ||
		!etag.WeakEqual(w, s) || !etag.WeakEqual(w, w) || etag.WeakEqual(s, etag.Tag{Value: "y"}) {
		t.Fatal("强弱比较语义错误")
	}
}
func TestSemantics(t *testing.T) {
	want(t, R{Method: "PUT", IfMatch: `"a"`, IfUnmodifiedSince: yesterday}, C) // If-Match 压制 IUS
	want(t, R{Method: "PUT", IfMatch: `"zzz"`, IfUnmodifiedSince: equal}, F)
	want(t, R{Method: "GET", IfNoneMatch: `"zzz"`, IfModifiedSince: equal}, C) // INM 压制 IMS
	want(t, R{Method: "PUT", IfMatch: "*"}, C)
	want(t, R{Method: "GET", IfNoneMatch: "*"}, N)
	want(t, R{Method: "GET", IfNoneMatch: "\"b\",\r\n \"a\""}, N) // 折行列表任一匹配
	want(t, R{Method: "PUT", IfMatch: `"b", "a"`}, C)
	want(t, R{Method: "GET", IfModifiedSince: equal}, N) // 临界：相等算未修改
	want(t, R{Method: "GET", IfModifiedSince: yesterday}, C)
	want(t, R{Method: "PUT", IfUnmodifiedSince: equal}, C) // 临界：相等算通过
	want(t, R{Method: "PUT", IfUnmodifiedSince: yesterday}, F)
	gone := precond.Resource{}
	if precond.Evaluate(gone, R{Method: "PUT", IfMatch: "*"}, now) != F ||
		precond.Evaluate(gone, R{Method: "GET", IfNoneMatch: "*"}, now) != C {
		t.Error("资源不存在时 * 语义错误")
	}
	for m, w := range map[string]precond.Result{"GET": N, "HEAD": N, "PUT": F, "POST": F} {
		want(t, R{Method: m, IfNoneMatch: `"a"`}, w) // 读写方法结果可区分
	}
	for _, bad := range []string{"", ",", `a`, `"a",,"b"`, `*, "a"`} {
		if _, _, err := etag.ParseList(bad); err == nil {
			t.Errorf("%q 应为语法错误", bad)
		}
	}
}
func TestParseErrorsAndIgnore(t *testing.T) {
	all := []error{httpdate.ErrSuffix, httpdate.ErrMonth, httpdate.ErrDate, httpdate.ErrWhitespace}
	bad := []string{`Mon, 02 Jan 2006 15:04:05 UTC`, `Mon, 02 Foo 2006 15:04:05 GMT`,
		`Mon, 30 Feb 2006 15:04:05 GMT`, `Mon,  02 Jan 2006 15:04:05 GMT`}
	for i, in := range bad {
		_, err := httpdate.Parse(in)
		for j, o := range all {
			if errors.Is(err, o) != (j == i) {
				t.Errorf("%q: 错误类别不匹配或不可区分", in)
			}
		}
	}
	for _, q := range []R{{Method: "GET", IfModifiedSince: "junk"}, {Method: "PUT", IfUnmodifiedSince: "junk"},
		{Method: "PUT", IfMatch: "broken"}, {Method: "GET", IfNoneMatch: ",,"}} {
		want(t, q, C) // 解析失败忽略该头部
	}
}
func TestClockAndRepeat(t *testing.T) {
	calls := 0
	clock := func() time.Time { calls++; return time.Time{} }
	precond.Evaluate(res, R{Method: "GET"}, clock)
	precond.Evaluate(res, R{Method: "PUT", IfMatch: `"a"`}, clock)
	if calls != 0 {
		t.Fatalf("无条件头部时时钟被调用 %d 次", calls)
	}
	q := R{Method: "GET", IfNoneMatch: `"a"`, IfModifiedSince: equal}
	resBefore, qBefore := res, q
	if ev(q) != ev(q) || res != resBefore || q != qBefore {
		t.Fatal("连查不一致或传入结构被修改")
	}
}
func TestCombos81(t *testing.T) {
	ref := func(q R) precond.Result { // 优先级参考模型
		switch {
		case q.IfMatch == `"zzz"`, q.IfMatch == "" && q.IfUnmodifiedSince == yesterday:
			return F
		case q.IfNoneMatch == `"a"`:
			return N
		case q.IfNoneMatch != "":
			return C
		case q.IfModifiedSince == equal:
			return N
		}
		return C
	}
	st := [][]string{{"", `"a"`, `"zzz"`}, {"", `"a"`, `"zzz"`}, {"", equal, yesterday}, {"", equal, yesterday}}
	for i := 0; i < 81; i++ {
		q := R{Method: "GET", IfMatch: st[0][i/27%3], IfNoneMatch: st[1][i/9%3],
			IfModifiedSince: st[2][i/3%3], IfUnmodifiedSince: st[3][i%3]}
		if got := ev(q); got != ref(q) {
			t.Errorf("%+v: got %v want %v", q, got, ref(q))
		}
	}
}
