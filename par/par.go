// Package par 按任意字节偏移切 K 段并行解析后拼接。
package par

import (
	"sync"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

// frag 为一个字段片段（值已是解码后字节）。
type frag struct {
	start, end int
	quoted     bool
	val        string
	endRec     bool // 该字段后记录落定
}

// segSink 收集一段解析产出的字段片段与记录边界。
type segSink struct {
	frags []frag
}

func (s *segSink) BeginField(start int, quoted bool) error {
	s.frags = append(s.frags, frag{start: start, quoted: quoted})
	return nil
}

func (s *segSink) Append(p []byte) error {
	s.frags[len(s.frags)-1].val += string(p)
	return nil
}

func (s *segSink) EndField(end int) (cell.Cell, error) {
	if len(s.frags) > 0 {
		s.frags[len(s.frags)-1].end = end
	}
	return cell.Cell{}, nil
}

func (s *segSink) EndRecord() error {
	if len(s.frags) > 0 {
		s.frags[len(s.frags)-1].endRec = true
	}
	return nil
}

type run struct {
	lex *lexer.Lexer
	snk *segSink
	err error
}

type seg struct {
	lo, hi int
	runs   [2]run // 0=OUT假设 1=IN假设
}

func oneRun(buf []byte, lo, hi int, st lexer.State, openStart, openLen int, quoted bool,
	lim lexer.Limits, final bool) run {
	s := &segSink{}
	l := lexer.New(s, lo, st, openStart, openLen, quoted, lim)
	err := l.Feed(buf[lo:hi])
	if err == nil && final {
		err = l.Close(true)
	}
	return run{lex: l, snk: s, err: err}
}

// mode 为拼接器在段起点的真实模式。
type mode int

const (
	mOUT mode = iota // 引号外
	mIN              // 引号字段内
	mCR              // CR 待定
)

func buildSegs(buf []byte, bounds []int, lim lexer.Limits) []seg {
	segs := make([]seg, len(bounds)-1)
	var wg sync.WaitGroup
	for i := range segs {
		lo, hi := bounds[i], bounds[i+1]
		wg.Add(1)
		go func(i, lo, hi int) {
			defer wg.Done()
			final := hi == len(buf)
			segs[i].lo, segs[i].hi = lo, hi
			// A 假设：起点引号外；切点前一字节为 \r 时从 CR 待定起。
			stA := lexer.Clear
			if lo > 0 && buf[lo-1] == '\r' {
				stA = lexer.CR
			}
			segs[i].runs[0] = oneRun(buf, lo, hi, stA, lo, 0, false, lim, final)
			// B 假设：起点引号内；前一字节为 " 则从 quoteSeen 起。
			stB := lexer.Quoted
			if lo > 0 && buf[lo-1] == '"' {
				stB = lexer.QuoteSeen
			}
			segs[i].runs[1] = oneRun(buf, lo, hi, stB, lo, 0, true, lim, false)
		}(i, lo, hi)
	}
	wg.Wait()
	return segs
}

func appendFrag(b *table.Builder, f frag) error {
	if err := b.BeginField(f.start, f.quoted); err != nil {
		return err
	}
	if err := b.Append([]byte(f.val)); err != nil {
		return err
	}
	if _, err := b.EndField(f.end); err != nil {
		return err
	}
	if f.endRec {
		return b.EndRecord()
	}
	return nil
}

// stitch 按真实模式从左到右选 A/B 输出，喂给一个全局 Builder。
func stitch(segs []seg, lim lexer.Limits) (*table.Table, int, error) {
	b := table.NewBuilder(lim)
	md := mOUT
	total := 0
	for si := range segs {
		s := &segs[si]
		var r *run
		switch md {
		case mOUT, mCR:
			r = &s.runs[0]
		case mIN:
			r = &s.runs[1]
		}
		total += r.lex.N()
		// IN 模式下本段首片段是跨切点字段的延续：并入上一字段。
		frags := r.snk.frags
		if md == mIN && len(frags) > 0 {
			f := frags[0]
			if err := b.Append([]byte(f.val)); err != nil {
				return nil, total, err
			}
			b.SetEnd(f.end)
			if f.endRec {
				if _, err := b.EndField(f.end); err != nil {
					return nil, total, err
				}
				if err := b.EndRecord(); err != nil {
					return nil, total, err
				}
			} else {
				// 仅更新暂挂字段的终点；字段未定稿，不调用 EndField。
			}
			frags = frags[1:]
		}
		for _, fi := range frags {
			if err := appendFrag(b, fi); err != nil {
				return nil, total, err
			}
		}
		if r.err != nil {
			return nil, total, r.err
		}
		// 由末状态决定下段真实模式。
		switch r.lex.State() {
		case lexer.Quoted, lexer.QuoteSeen:
			md = mIN
		case lexer.CR:
			md = mCR
		default:
			md = mOUT
		}
	}
	return &table.Table{Records: b.Records()}, total, b.Err()
}

func runParse(buf []byte, bounds []int, lim lexer.Limits) (*table.Table, int, error) {
	segs := buildSegs(buf, bounds, lim)
	return stitch(segs, lim)
}

func boundsK(n, k int) []int {
	bounds := []int{0}
	for i := 1; i < k; i++ {
		bounds = append(bounds, n*i/k)
	}
	return append(bounds, n)
}

// Parse 将 buf 切成 K 段并行解析。
func Parse(buf []byte, k int, lim lexer.Limits) (*table.Table, error) {
	if k < 1 {
		k = 1
	}
	t, n, err := runParse(buf, boundsK(len(buf), k), lim)
	lastN = n
	return t, err
}

// ParseCuts 在给定切点并行解析（绝对偏移；自动去重排序并补 0/len）。
func ParseCuts(buf []byte, cuts []int, lim lexer.Limits) (*table.Table, error) {
	bounds := []int{0}
	for _, c := range cuts {
		if c > 0 && c < len(buf) && c != bounds[len(bounds)-1] {
			bounds = append(bounds, c)
		}
	}
	bounds = append(bounds, len(buf))
	t, n, err := runParse(buf, bounds, lim)
	lastN = n
	return t, err
}

var lastN int

// BytesProcessed 返回最近一次并行解析所有 worker 的处理字节计数之和。
func BytesProcessed() int { return lastN }
