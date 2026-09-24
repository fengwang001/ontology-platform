// Command demo 逐步演示条件请求前置判定器的各项语义。
package main

import (
	"errors"
	"fmt"
	"time"

	"ontology/etag"
	"ontology/httpdate"
	"ontology/precond"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
	} else {
		failed++
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}

func main() {
	strong, weak := mustTag(`"x"`), mustTag(`W/"x"`)
	check("etag: 强比较 W/\"x\" vs \"x\" 不等", !etag.StrongEqual(weak, strong))
	check("etag: 弱比较 W/\"x\" vs \"x\" 相等", etag.WeakEqual(weak, strong))
	check("etag: 强比较 W/\"x\" vs W/\"x\" 不等", !etag.StrongEqual(weak, weak))
	check("etag: 弱比较 W/\"x\" vs W/\"x\" 相等", etag.WeakEqual(weak, weak))
	check("httpdate: 非 GMT 后缀可区分", classErr(`Mon, 02 Jan 2006 15:04:05 UTC`, httpdate.ErrSuffix))
	check("httpdate: 非法月份名可区分", classErr(`Mon, 02 Foo 2006 15:04:05 GMT`, httpdate.ErrMonth))
	check("httpdate: 2 月 30 日不存在可区分", classErr(`Mon, 30 Feb 2006 15:04:05 GMT`, httpdate.ErrDate))
	check("httpdate: 多余空白可区分", classErr(`Mon,  02 Jan 2006 15:04:05 GMT`, httpdate.ErrWhitespace))
	lastMod := mustDate(`Tue, 03 Jan 2006 15:04:05 GMT`)
	res := precond.Resource{Exists: true, ETag: `"a"`, LastMod: lastMod}
	now := func() time.Time { return mustDate(`Wed, 04 Jan 2006 15:04:05 GMT`) }
	eval := func(r precond.Resource, q precond.Request) precond.Result {
		return precond.Evaluate(r, q, now)
	}
	imsEq := precond.Request{Method: "GET", IfModifiedSince: `Tue, 03 Jan 2006 15:04:05 GMT`} // 临界：相等
	iusEq := precond.Request{Method: "GET", IfUnmodifiedSince: `Tue, 03 Jan 2006 15:04:05 GMT`}
	check("precond: If-Match 压制 If-Unmodified-Since",
		eval(res, precond.Request{Method: "PUT", IfMatch: `"a"`,
			IfUnmodifiedSince: `Mon, 02 Jan 2006 15:04:05 GMT`}) == precond.Continue)
	check("precond: If-Match:* 存在通过/不存在失败",
		eval(res, precond.Request{Method: "PUT", IfMatch: "*"}) == precond.Continue &&
			eval(precond.Resource{}, precond.Request{Method: "PUT", IfMatch: "*"}) == precond.Failed)
	check("precond: If-None-Match:* 存在未修改/不存在通过",
		eval(res, precond.Request{Method: "GET", IfNoneMatch: "*"}) == precond.NotModified &&
			eval(precond.Resource{}, precond.Request{Method: "GET", IfNoneMatch: "*"}) == precond.Continue)
	check("precond: 列表任一匹配与折行",
		eval(res, precond.Request{Method: "GET", IfNoneMatch: "\"b\",\r\n \"a\""}) == precond.NotModified &&
			eval(res, precond.Request{Method: "PUT", IfMatch: `"b", "a"`}) == precond.Continue)
	check("precond: 读写方法结果可区分",
		eval(res, precond.Request{Method: "GET", IfNoneMatch: `"a"`}) == precond.NotModified &&
			eval(res, precond.Request{Method: "PUT", IfNoneMatch: `"a"`}) == precond.Failed)
	check("precond: 相等时刻 IMS 未修改 / IUS 通过",
		eval(res, imsEq) == precond.NotModified && eval(res, iusEq) == precond.Continue)
	check("precond: 解析失败忽略该头部",
		eval(res, precond.Request{Method: "GET", IfModifiedSince: "not-a-date"}) == precond.Continue &&
			eval(res, precond.Request{Method: "PUT", IfMatch: "broken"}) == precond.Continue)
	check("precond: 无条件头部时钟零调用", clockUntouched(res))
	check("precond: 81 组合遍历通过", combos81(res, now))
	check("precond: 连查两次一致",
		eval(res, imsEq) == eval(res, imsEq))
	fmt.Printf("total: %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		panic("demo failed")
	}
}

func mustDate(s string) time.Time {
	t, err := httpdate.Parse(s)
	if err != nil {
		panic(err)
	}
	return t
}

func clockUntouched(res precond.Resource) bool {
	calls := 0
	precond.Evaluate(res, precond.Request{Method: "GET"}, func() time.Time {
		calls++
		return time.Time{}
	})
	return calls == 0
}

func combos81(res precond.Resource, now func() time.Time) (ok bool) {
	vals := []string{"", `"a"`, `"zzz"`}
	dates := []string{"", `Tue, 03 Jan 2006 15:04:05 GMT`, `Mon, 02 Jan 2006 15:04:05 GMT`}
	defer func() { ok = recover() == nil && ok }()
	ok = true
	for _, im := range vals {
		for _, inm := range vals {
			for _, ims := range dates {
				for _, ius := range dates {
					r := precond.Evaluate(res, precond.Request{Method: "GET", IfMatch: im,
						IfNoneMatch: inm, IfModifiedSince: ims, IfUnmodifiedSince: ius}, now)
					ok = ok && (r == precond.Continue || r == precond.NotModified || r == precond.Failed)
				}
			}
		}
	}
	return ok
}

func classErr(s string, want error) bool {
	_, err := httpdate.Parse(s)
	return err != nil && errors.Is(err, want)
}

func mustTag(s string) etag.Tag {
	t, err := etag.Parse(s)
	if err != nil {
		panic(err)
	}
	return t
}
