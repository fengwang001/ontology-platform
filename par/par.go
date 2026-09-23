// Package par 把缓冲区按任意字节偏移切成 K 段并行解析后顺序裁决拼接。
package par

import (
	"sync"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

type ev struct {
	c cell.Cell
	r bool
}

type recorder struct{ evs []ev }

func (r *recorder) OnCell(c cell.Cell) error { r.evs = append(r.evs, ev{c: c}); return nil }
func (r *recorder) OnRecord() error          { r.evs = append(r.evs, ev{r: true}); return nil }

type run struct {
	rc  *recorder
	sn  lexer.Snapshot
	err error
}

// worker 只对本段 Feed（不 Close），错误与末态一并带回。
func worker(p []byte, lim lexer.Limits, sn *lexer.Snapshot) *run {
	rc := &recorder{}
	var lx *lexer.Lexer
	if sn == nil {
		lx = lexer.New(rc, lim)
	} else {
		lx = lexer.Restore(rc, lim, *sn)
	}
	return &run{rc: rc, sn: lx.Snap(), err: lx.Feed(p)}
}

func cuts(buf []byte, K int) [][]byte {
	n := len(buf)
	if K < 1 {
		K = 1
	}
	segs := make([][]byte, 0, K)
	for i := 0; i < K; i++ {
		s, e := i*n/K, (i+1)*n/K
		if s < e || i == 0 {
			segs = append(segs, buf[s:e])
		}
	}
	return segs
}

func shift(err error, baseOff, baseRec int) error {
	var pe *lexer.PosError
	if asErr(err, &pe) {
		return &lexer.PosError{Err: pe.Err, Offset: pe.Offset + baseOff,
			Record: pe.Record + baseRec, Field: pe.Field}
	}
	return err
}

// Parse 并行解析 buf，结果与 table.Parse 完全相同；返回表与字节处理总次数。
func Parse(buf []byte, K int, lim lexer.Limits) (*table.Table, int, error) {
	segs := cuts(buf, K)
	K = len(segs)
	bases := make([]int, K)
	h0 := make([]*run, K)
	var wg sync.WaitGroup
	for i := range segs {
		if i > 0 {
			bases[i] = bases[i-1] + len(segs[i-1])
		}
		wg.Add(1)
		go func(i int) { defer wg.Done(); h0[i] = worker(segs[i], lim, nil) }(i)
	}
	wg.Wait()

	tab := table.NewTable(lim)
	total := 0
	var sn *lexer.Snapshot
	closedRecs := 0
	flush := func(r *run, h0run bool, baseOff, baseRec int) error {
		if r.err != nil {
			if h0run {
				return shift(r.err, baseOff, baseRec)
			}
			return r.err
		}
		cellsSinceRec := 0
		for _, e := range r.rc.evs {
			if e.r {
				if err := tab.OnRecord(); err != nil {
					return err
				}
				if cellsSinceRec > 0 {
					closedRecs++
				}
				cellsSinceRec = 0
			} else {
				c := e.c
				if h0run {
					c.Start += baseOff
					c.End += baseOff
				}
				if err := tab.OnCell(c); err != nil {
					return err
				}
				cellsSinceRec++
			}
		}
		return nil
	}
	for i := range segs {
		var chosen *run
		isH0 := i > 0 && sn != nil && sn.AtCleanBoundary()
		if isH0 {
			chosen = h0[i]
			total += len(segs[i])
		} else {
			chosen = worker(segs[i], lim, sn)
			total += len(segs[i])
			if i > 0 {
				total += len(segs[i]) // H0 已并行预跑
			}
		}
		if err := flush(chosen, isH0, bases[i], closedRecs); err != nil {
			return tab, total, err
		}
		s := chosen.sn
		sn = &s
	}
	// 仅最终选中态 Close，冲刷无尾换行的末记录或报未闭合/裸 CR。
	finalRC := &recorder{}
	lx := lexer.Restore(finalRC, lim, *sn)
	if err := lx.Close(); err != nil {
		return tab, total, err
	}
	if err := flush(&run{rc: finalRC}, false, 0, 0); err != nil {
		return tab, total, err
	}
	return tab, total, nil
}

// asErr 是 errors.As 的薄封装（避免额外导入行）。
func asErr(err error, target **lexer.PosError) bool {
	for err != nil {
		if pe, ok := err.(*lexer.PosError); ok {
			*target = pe
			return true
		}
		if u, ok := err.(interface{ Unwrap() error }); ok {
			err = u.Unwrap()
			continue
		}
		return false
	}
	return false
}
