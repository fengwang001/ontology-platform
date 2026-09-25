// Package par 按任意字节偏移切分缓冲区并行解析后拼接。
package par

import (
	"sync"

	"ontology/cell"
	"ontology/lexer"
)

// Result 是并行解析结果。
type Result struct {
	Records        [][]cell.Cell
	BytesProcessed int64
}

type seg struct {
	fields    []cell.Cell
	recEnds   []int
	inside    bool
	processed int64
	err       error
}

type collector struct{ s *seg }

func (c collector) Field(cl cell.Cell) { c.s.fields = append(c.s.fields, cl) }
func (c collector) Record()            { c.s.recEnds = append(c.s.recEnds, len(c.s.fields)) }
func (c collector) SkipLine()          {}

// quoteScan 线性扫描，返回每个规整切点是否位于引号字段内部。
func quoteScan(buf []byte, cuts []int) ([]bool, int64) {
	inside := make([]bool, len(cuts))
	q, n := false, int64(0)
	ci := 1
	for i := 0; i < len(buf); i++ {
		n++
		c := buf[i]
		if c == '"' {
			if q && i+1 < len(buf) && buf[i+1] == '"' {
				i++
				n++
			} else {
				q = !q
			}
		}
		if ci < len(cuts)-1 && i+1 == cuts[ci] {
			inside[ci] = q
			ci++
		}
	}
	return inside, n
}

func runSeg(buf []byte, start int, startInside bool) seg {
	s := seg{inside: startInside}
	lx := lexer.New(lexer.Config{BaseOffset: start, StartInside: startInside, NoEOF: true}, collector{&s})
	if e := lx.Feed(buf); e != nil {
		s.err = e
	}
	s.processed = lx.BytesProcessed()
	return s
}

func regularize(buf []byte, cuts []int) []int {
	out := append([]int(nil), cuts...)
	for i := 1; i < len(out)-1; i++ {
		for out[i] > out[i-1]+1 && (buf[out[i]-1] == '"' || buf[out[i]-1] == '\r') {
			out[i]--
		}
	}
	j := 1
	for i := 1; i < len(out); i++ {
		if i == len(out)-1 || out[i] > out[j-1] {
			out[j] = out[i]
			j++
		}
	}
	return out[:j]
}

func mergeCell(dst cell.Cell, more cell.Cell) cell.Cell {
	dst.Value += more.Value
	dst.Quoted = dst.Quoted || more.Quoted
	if more.End > dst.End {
		dst.End = more.End
	}
	return dst
}

// ParseCuts 按给定切点并行解析。
func ParseCuts(buf []byte, cuts []int) (*Result, error) {
	cuts = regularize(buf, cuts)
	inside, scanned := quoteScan(buf, cuts)
	segs := make([]seg, len(cuts)-1)
	var wg sync.WaitGroup
	for i := range segs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			segs[i] = runSeg(buf[cuts[i]:cuts[i+1]], cuts[i], inside[i])
		}(i)
	}
	wg.Wait()
	res := &Result{BytesProcessed: scanned}
	var cur []cell.Cell
	var total int64
	for i := range segs {
		total += segs[i].processed
		if segs[i].err != nil {
			res.BytesProcessed = scanned + total
			return res, segs[i].err
		}
	}
	flush := func() {
		if len(cur) > 0 {
			res.Records = append(res.Records, append([]cell.Cell(nil), cur...))
			cur = cur[:0]
		}
	}
	for i := range segs {
		s := segs[i]
		f, r := 0, 0
		if len(cur) > 0 && len(s.fields) > 0 {
			cur[len(cur)-1] = mergeCell(cur[len(cur)-1], s.fields[0])
			f = 1
		}
		for f < len(s.fields) {
			cur = append(cur, s.fields[f])
			f++
			if r < len(s.recEnds) && f == s.recEnds[r] {
				flush()
				r++
			}
		}
		for r < len(s.recEnds) {
			flush()
			r++
		}
	}
	flush()
	res.BytesProcessed = scanned + total
	return res, nil
}

// Parse 把 buf 切成 K 段并行解析。
func Parse(buf []byte, k int) (*Result, error) {
	if k < 1 {
		k = 1
	}
	if k > len(buf)+1 {
		k = len(buf) + 1
	}
	cuts := make([]int, k+1)
	for i := 0; i <= k; i++ {
		cuts[i] = i * len(buf) / k
	}
	return ParseCuts(buf, cuts)
}
