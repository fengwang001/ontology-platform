// Command demo 逐条演示多区间 Range 组装器的语义判定。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"

	"ontology/coalesce"
	"ontology/multipart"
	"ontology/rangespec"
	"ontology/serve"
	"ontology/source"
)

var failures int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func mustParse(h string) []rangespec.Range {
	rs, err := rangespec.Parse(h)
	if err != nil {
		panic(err)
	}
	return rs
}

func mustPrepare(cfg serve.Config, header string, src source.Source) *serve.Assembler {
	a := serve.New(cfg)
	if err := a.Prepare(header, src); err != nil {
		panic(err)
	}
	return a
}

func drain(a *serve.Assembler, chunk int) []byte {
	var out []byte
	buf := make([]byte, chunk)
	for {
		n, err := a.Write(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			return out
		}
	}
}

func expand(rs []coalesce.Range, set map[int64]bool) {
	for _, r := range rs {
		for p := r.Start; p < r.End; p++ {
			set[p] = true
		}
	}
}

func main() {
	demoRangespec()
	demoCoalesce()
	demoMultipart()
	demoServe()
	fmt.Printf("total: %d failure(s)\n", failures)
	if failures > 0 {
		panic("demo failed")
	}
}

func demoRangespec() {
	rs, err := rangespec.Parse("bytes=0-4,5-,-10")
	ok := err == nil && len(rs) == 3 &&
		rs[0].Start == 0 && rs[0].End == 4 && rs[0].Suffix == -1 &&
		rs[1].Start == 5 && rs[1].End == -1 && rs[2].Suffix == 10
	check("parse three forms", ok, "a-b / a- / -n")
	var pe *rangespec.ParseError
	_, err = rangespec.Parse("bytes=0x-1")
	ok = errors.As(err, &pe) && pe.Offset == 7
	check("parse error offset", ok, fmt.Sprintf("err=%v", err))
}

func demoCoalesce() {
	const total = 10
	rs, err := coalesce.Normalize(mustParse("bytes=5-100,-100"), total)
	ok := err == nil && len(rs) == 1 && rs[0].Start == 0 && rs[0].End == 10
	check("clamp out-of-bounds", ok, fmt.Sprintf("%v", rs))

	var ue *coalesce.UnsatisfiableError
	_, err = coalesce.Normalize(mustParse("bytes=-0"), total)
	ok = errors.As(err, &ue) && ue.Total == total
	check("bytes=-0 unsatisfiable", ok, fmt.Sprintf("err=%v", err))

	var pe *rangespec.ParseError
	_, perr := rangespec.Parse("bytes=x")
	_, uerr := coalesce.Normalize(mustParse("bytes=50-"), total)
	ok = errors.As(perr, &pe) && !errors.As(perr, &ue) &&
		errors.As(uerr, &ue) && !errors.As(uerr, &pe)
	check("two error kinds distinct", ok, "parse vs unsatisfiable")

	rng := rand.New(rand.NewSource(42))
	var specs []rangespec.Range
	before := map[int64]bool{}
	for i := 0; i < 200; i++ {
		s := rangespec.Range{Start: rng.Int63n(12), End: rng.Int63n(12), Suffix: -1}
		if one, err := coalesce.Normalize([]rangespec.Range{s}, total); err == nil {
			specs = append(specs, s)
			expand(one, before)
		}
	}
	merged, err := coalesce.Normalize(specs, total)
	after := map[int64]bool{}
	expand(merged, after)
	ok = err == nil && reflect.DeepEqual(before, after)
	check("merge keeps byte set", ok, fmt.Sprintf("%d specs -> %d ranges", len(specs), len(merged)))

	c100, c10000 := countCompares(rng, 100), countCompares(rng, 10000)
	ok = c100 > 0 && c10000 > 0 && float64(c10000)/float64(c100) <= 300
	check("compares O(n log n)", ok, fmt.Sprintf("n=100:%d n=10000:%d ratio=%.1f",
		c100, c10000, float64(c10000)/float64(c100)))
}

func countCompares(rng *rand.Rand, n int) int64 {
	specs := make([]rangespec.Range, n)
	for i := range specs {
		specs[i] = rangespec.Range{Start: rng.Int63n(1 << 40), End: rng.Int63n(1 << 40), Suffix: -1}
	}
	coalesce.ResetCompares()
	_, _ = coalesce.Normalize(specs, 1<<40)
	return coalesce.Compares()
}

func demoMultipart() {
	tricky := []byte("payload with \r\n--ontology-boundary-deadbeef\r\n inside")
	ranges := []coalesce.Range{{Start: 0, End: 10}, {Start: 20, End: 30}}
	contents := [][]byte{tricky, []byte("second part body")}
	body, boundary, err := multipart.Build("application/octet-stream", 64, ranges, contents, 8)
	delim := []byte("\r\n--" + boundary)
	ok := err == nil && boundary != "" &&
		bytes.Count(append(tricky, delim...), delim) == 1 &&
		bytes.Contains(body, tricky) && bytes.Contains(body, []byte("second part body"))
	check("boundary avoids content", ok, fmt.Sprintf("boundary=%.24s...", boundary))
}

func demoServe() {
	data := []byte("0123456789abcdefghijklmnopqrstuvwxyz")

	short := source.NewScripted(data)
	short.SetMaxChunk(1)
	a := mustPrepare(serve.Config{}, "bytes=2-8,20-25", short)
	check("short reads topped up", bytes.Contains(drain(a, 1<<20), data[2:9]), "maxChunk=1")

	shrunk := source.NewScripted(data[:10])
	shrunk.SetLen(20) // 声称 20 字节，实际只有 10
	err := serve.New(serve.Config{}).Prepare("bytes=0-19", shrunk)
	check("EOF shortfall detected", errors.Is(err, serve.ErrShortRead), fmt.Sprintf("err=%v", err))

	cfg := serve.Config{BoundaryGen: func() string { return "DEMOBOUNDARY" }}
	ref := drain(mustPrepare(cfg, "bytes=0-9,20-29", source.Bytes(data)), 1<<20)
	same := len(ref) > 0
	for cut := 1; cut <= len(ref) && same; cut++ {
		same = bytes.Equal(drain(mustPrepare(cfg, "bytes=0-9,20-29", source.Bytes(data)), cut), ref)
	}
	check("all write cut points identical", same, fmt.Sprintf("%d cut points", len(ref)))

	one := mustPrepare(serve.Config{}, "bytes=2-5", source.Bytes(data))
	raw := bytes.Equal(drain(one, 1<<20), data[2:6]) && !one.Info().Multipart
	merged := mustPrepare(serve.Config{}, "bytes=0-3,4-7", source.Bytes(data))
	raw = raw && !merged.Info().Multipart && bytes.Equal(drain(merged, 1<<20), data[0:8])
	check("single range raw bytes", raw, "incl. merged degenerate case")

	lim := serve.New(serve.Config{MaxRanges: 2, MaxBytes: 16, MaxBoundaryTries: 2,
		BoundaryGen: func() string { return "X" }})
	if err := lim.Prepare("bytes=0-3", source.Bytes(data)); err != nil {
		panic(err)
	}
	before := lim.Info()
	e1 := lim.Prepare("bytes=0-1,2-3,4-5", source.Bytes(data))
	e2 := lim.Prepare("bytes=0-9,20-29", source.Bytes(append([]byte("\r\n--X"), data...)))
	e3 := lim.Prepare("bytes=0-30", source.Bytes(data))
	ok := errors.Is(e1, serve.ErrTooManyRanges) && errors.Is(e2, multipart.ErrBoundaryRetries) &&
		errors.Is(e3, serve.ErrTooLarge) && reflect.DeepEqual(lim.Info(), before)
	check("three limits rejected cleanly", ok, "state untouched")

	q := serve.New(serve.Config{})
	i0, i1 := q.Info(), q.Info()
	_ = q.Prepare("bytes=0-3,8-11", source.Bytes(data))
	i2, i3 := q.Info(), q.Info()
	ok = reflect.DeepEqual(i0, serve.Info{}) && reflect.DeepEqual(i0, i1) &&
		reflect.DeepEqual(i2, i3) && i2.TotalBytes > 0 && i2.Written == 0 && i2.Multipart
	check("queries stable, no side effect", ok, fmt.Sprintf("ranges=%d", len(i2.Ranges)))
}
