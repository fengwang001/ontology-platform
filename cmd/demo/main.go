// Command demo exercises every guarantee of the range-response assembler
// and prints one OK/FAIL line per guarantee. Exit code is 0 iff all pass.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"ontology/coalesce"
	"ontology/rangespec"
	"ontology/serve"
	"ontology/source"
)

var failures int

func report(name string, ok bool, detail ...string) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		failures++
	}
	line := fmt.Sprintf("%-2s %s %s", mark, name, strings.Join(detail, " "))
	fmt.Println(strings.TrimRight(line, " "))
}

// zeroRand makes boundaries deterministic for byte-comparison checks.
type zeroRand struct{}

func (zeroRand) Read(p []byte) (int, error) { return len(p), nil }

type chunkWriter struct {
	buf bytes.Buffer
	max int
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.buf.Write(p)
}

func drain(r *serve.Response, w io.Writer) {
	for {
		if _, err := r.WriteSome(w); err != nil {
			return
		}
	}
}

func body(a *serve.Assembler, header string) ([]byte, *serve.Response) {
	r, err := a.Build(header)
	if err != nil {
		return nil, nil
	}
	var buf bytes.Buffer
	drain(r, &buf)
	return buf.Bytes(), r
}

func main() {
	data := source.Bytes([]byte("0123456789"))
	a := assemblerOf(data)

	// 1. Three forms and out-of-bounds clipping.
	b1, _ := body(a, "bytes=2-4")
	b2, _ := body(a, "bytes=8-99")
	b3, _ := body(a, "bytes=7-")
	b4, _ := body(a, "bytes=-3")
	b5, _ := body(a, "bytes=-99")
	report("1  three forms + clipping", string(b1) == "234" && string(b2) == "89" &&
		string(b3) == "789" && string(b4) == "789" && string(b5) == "0123456789")

	// 2. bytes=-0 is unsatisfiable and carries the total length.
	_, err := a.Build("bytes=-0")
	var ue *coalesce.UnsatisfiableError
	report("2  bytes=-0 unsatisfiable", errors.As(err, &ue) && ue.Total == 10)

	// 3. Syntax error vs unsatisfiable are distinguishable.
	_, synErr := a.Build("bytes=x-y")
	var se *rangespec.SyntaxError
	ok := errors.As(synErr, &se) && !errors.As(synErr, &ue) &&
		errors.As(err, &ue) && !errors.As(err, &se)
	report("3  error kinds distinguishable", ok, fmt.Sprintf("(syntax offset=%d)", se.Offset))

	// 4. Byte set identical before/after coalescing.
	report("4  merge preserves byte set", byteSetCheck())

	// 5. Comparison counts for n=100 vs n=10000.
	c1 := countComparisons(100)
	c2 := countComparisons(10000)
	ratio := float64(c2) / float64(c1)
	report("5  comparisons O(n log n)", ratio <= 400,
		fmt.Sprintf("(n=100: %d, n=10000: %d, ratio=%.1f)", c1, c2, ratio))

	// 6. Short reads are completed.
	short := &serve.Assembler{Src: source.Short{Src: data, Max: 1}}
	b6, _ := body(short, "bytes=0-9")
	report("6  short reads completed", string(b6) == "0123456789")

	// 7. Every write split point yields identical bytes.
	report("7  all split points identical", splitPointCheck())

	// 8. Single range is raw bytes; merged-to-one degenerates too.
	r1, _ := a.Build("bytes=2-5")
	r2, _ := a.Build("bytes=0-4, 5-9")
	report("8  single range raw bytes", !r1.Multipart() && !r2.Multipart())

	// 9. Boundary never collides with content.
	report("9  boundary avoids content", boundaryCheck())

	// 10. Three resource limits are distinguishable.
	report("10 three limits reject", limitsCheck())

	// 11. Queries are stable and non-mutating.
	q1 := fmt.Sprint(r1.Ranges(), r1.TotalBytes(), r1.Written(), r1.Multipart())
	q2 := fmt.Sprint(r1.Ranges(), r1.TotalBytes(), r1.Written(), r1.Multipart())
	report("11 queries stable", q1 == q2)

	fmt.Printf("== %d/11 checks passed\n", 11-failures)
	if failures > 0 {
		os.Exit(1)
	}
}

func assemblerOf(s source.Source) *serve.Assembler { return &serve.Assembler{Src: s} }
