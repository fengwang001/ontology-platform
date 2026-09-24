// Command demo 逐条演示多区间 Range 响应组装器的各项语义判定。
package main

import (
	"bytes"
	crand "crypto/rand"
	"errors"
	"fmt"
	"math/rand"
	"reflect"

	"ontology/coalesce"
	"ontology/multipart"
	"ontology/rangespec"
	"ontology/serve"
	"ontology/source"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// rangespec：三种写法解析
	specs, err := rangespec.Parse("bytes=0-4, 10-, -3")
	want := []rangespec.Spec{{Start: 0, End: 4}, {Start: 10, End: -1}, {Start: -1, Suffix: 3}}
	check("rangespec: three forms parse", err == nil && reflect.DeepEqual(specs, want))

	// rangespec：语法错误带偏移，且与不可满足是两类
	_, synErr := rangespec.Parse("bytes=1-cd")
	var syn *rangespec.SyntaxError
	var unsat *rangespec.UnsatisfiableError
	check("rangespec: syntax error has offset, distinct kind",
		errors.As(synErr, &syn) && syn.Offset == 8 && !errors.As(synErr, &unsat))

	// coalesce：三种写法与越界裁剪（total=20）
	norm, err := coalesce.Normalize(specs, 20)
	clip, err2 := coalesce.Normalize([]rangespec.Spec{{Start: 5, End: 999}, {Start: -1, Suffix: 100}}, 20)
	check("coalesce: three forms clipped to bounds", err == nil && err2 == nil &&
		reflect.DeepEqual(norm, []coalesce.Range{{Start: 0, End: 4}, {Start: 10, End: 19}}) &&
		reflect.DeepEqual(clip, []coalesce.Range{{Start: 0, End: 19}}))

	// coalesce：bytes=-0 不可满足且带总长
	_, zeroErr := coalesce.Normalize([]rangespec.Spec{{Start: -1, Suffix: 0}}, 42)
	check("coalesce: bytes=-0 unsatisfiable with total",
		errors.As(zeroErr, &unsat) && unsat.Total == 42 && !errors.As(zeroErr, &syn))
	// coalesce：合并前后字节集合相同（随机穷举对照）
	check("coalesce: byte set preserved", byteSetPreserved(500))
	// coalesce：比较次数两组对照（n=100 vs n=10000）
	c1, c2 := countFor(100), countFor(10000)
	check(fmt.Sprintf("coalesce: compares %d vs %d, ratio %.1f <= 400", c1, c2, float64(c2)/float64(c1)),
		float64(c2)/float64(c1) <= 400)
	// source：短读注入（单次最多 MaxChunk 字节）
	mem := &source.Mem{Data: []byte("0123456789"), MaxChunk: 3}
	n, err := mem.ReadAt(make([]byte, 10), 0)
	check("source: short read injection", err == nil && n == 3)

	// multipart：内容含"看起来像边界串"的字节时，生成的边界串不冲突
	fake := multipart.Prefix + "00000000000000000000000000000000"
	content := []byte("AAA" + fake + "BBB")
	b, err := multipart.Boundary(content, 8, crand.Reader)
	check("multipart: boundary avoids content", err == nil && !bytes.Contains(content, []byte(b)))

	// multipart：重试上限耗尽可判定（注入确定性随机源，内容恰含该边界串）
	_, bErr := multipart.Boundary(content, 3, bytes.NewReader(make([]byte, 64)))
	check("multipart: retry limit exhausted", errors.Is(bErr, multipart.ErrBoundaryRetries))

	// serve：短读源循环补齐
	d200 := make([]byte, 200)
	for i := range d200 {
		d200[i] = byte(i)
	}
	a1 := serve.New(&source.Mem{Data: d200, MaxChunk: 3}, serve.Limits{})
	ok1 := a1.Build("bytes=10-29") == nil && bytes.Equal(drain(a1, 1<<20), d200[10:30])
	check("serve: short read filled", ok1)

	// serve：遍历所有写出切分点，字节完全相同
	a2 := serve.New(&source.Mem{Data: d200}, serve.Limits{})
	ok2 := a2.Build("bytes=10-29, 50-69, 180-") == nil
	ref := drain(a2, 1<<20)
	for chunk := 1; chunk <= len(ref) && ok2; chunk++ {
		a2.Rewind()
		ok2 = bytes.Equal(drain(a2, chunk), ref)
	}
	check(fmt.Sprintf("serve: all %d split points identical", len(ref)), ok2)

	// serve：单区间裸字节；相邻两区间合并后退化为裸字节
	a3 := serve.New(&source.Mem{Data: d200}, serve.Limits{})
	a4 := serve.New(&source.Mem{Data: d200}, serve.Limits{})
	ok3 := a3.Build("bytes=2-5") == nil && !a3.Stats().Multipart && bytes.Equal(drain(a3, 1<<20), d200[2:6])
	ok4 := a4.Build("bytes=0-4, 5-9") == nil && !a4.Stats().Multipart && bytes.Equal(drain(a4, 1<<20), d200[0:10])
	check("serve: single range bare, merged pair degenerates", ok3 && ok4)

	// serve：边界串与内容不冲突（内容含伪边界串）
	fakeData := []byte("HEAD" + fake + "MID" + fake + "TAIL")
	a5 := serve.New(&source.Mem{Data: fakeData}, serve.Limits{})
	ok5 := a5.Build("bytes=0-10, 20-") == nil && a5.Stats().Multipart
	body := drain(a5, 1<<20)
	boundary := body[2:bytes.IndexByte(body, '\r')]
	check("serve: boundary avoids content", ok5 && !bytes.Contains(fakeData, boundary))

	// serve：三类超限，彼此可判定且拒绝后状态零变化
	zero := serve.Stats{}
	limCases := []struct {
		lim  serve.Limits
		hdr  string
		src  *source.Mem
		want error
	}{
		{serve.Limits{MaxRanges: 2}, "bytes=0-1, 5-6, 10-11", &source.Mem{Data: d200}, serve.ErrTooManyRanges},
		{serve.Limits{MaxBytes: 5}, "bytes=0-20", &source.Mem{Data: d200}, serve.ErrTooManyBytes},
		{serve.Limits{MaxTries: 1, Rand: bytes.NewReader(make([]byte, 64))}, "bytes=0-44, 50-60",
			&source.Mem{Data: fakeData}, serve.ErrBoundaryTries},
	}
	okLim := !errors.Is(serve.ErrTooManyRanges, serve.ErrTooManyBytes) // 三类错误彼此可判定
	for _, c := range limCases {
		la := serve.New(c.src, c.lim)
		okLim = okLim && errors.Is(la.Build(c.hdr), c.want) && reflect.DeepEqual(la.Stats(), zero)
	}
	check("serve: three limits reject, state unchanged", okLim)

	// serve：查询稳定（连查两次一致、不推进状态、未组装为零值）
	fresh := serve.New(&source.Mem{Data: d200}, serve.Limits{})
	s1, s2 := a2.Stats(), a2.Stats()
	check("serve: stats stable, zero before build",
		reflect.DeepEqual(s1, s2) && reflect.DeepEqual(fresh.Stats(), zero) &&
			s1.Multipart && len(s1.Ranges) == 3)

	fmt.Printf("TOTAL %d failed\n", failures)
	if failures > 0 {
		panic("demo checks failed")
	}
}

// drain 用固定块大小写完组装器，返回拼接字节。
func drain(a *serve.Assembler, chunk int) []byte {
	var out []byte
	buf := make([]byte, chunk)
	for {
		n, err := a.Write(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out // io.EOF 表示写完
		}
	}
}

func byteSetPreserved(trials int) bool {
	r := rand.New(rand.NewSource(7))
	expand := func(rs []coalesce.Range, set map[int64]bool) {
		for _, g := range rs {
			for b := g.Start; b <= g.End; b++ {
				set[b] = true
			}
		}
	}
	for t := 0; t < trials; t++ {
		total := int64(1 + r.Intn(30))
		var specs []rangespec.Spec
		before := map[int64]bool{}
		for i := 0; i < 1+r.Intn(6); i++ {
			s := rangespec.Spec{Start: int64(r.Intn(int(total) + 3)), End: int64(r.Intn(int(total) + 3))}
			specs = append(specs, s)
			if s.End >= total {
				s.End = total - 1
			}
			if s.Start < total && s.End >= s.Start {
				expand([]coalesce.Range{{Start: s.Start, End: s.End}}, before)
			}
		}
		got, err := coalesce.Normalize(specs, total)
		if len(before) == 0 {
			continue
		}
		after := map[int64]bool{}
		expand(got, after)
		if err != nil || !reflect.DeepEqual(before, after) {
			return false
		}
	}
	return true
}

func countFor(n int) int64 {
	r := rand.New(rand.NewSource(int64(n)))
	specs := make([]rangespec.Spec, n)
	for i := range specs {
		specs[i] = rangespec.Spec{Start: int64(r.Intn(n * 2)), End: int64(r.Intn(n * 4))}
	}
	coalesce.ResetCompareCount()
	_, _ = coalesce.Normalize(specs, int64(n*4))
	return coalesce.CompareCount()
}
