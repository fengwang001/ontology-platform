// Package par 把缓冲区按任意字节偏移切 K 段并行解析并拼接。
package par

import (
	"errors"
	"sync"

	"ontology/lexer"
	"ontology/table"
)

type run struct {
	tail lexer.Tail
	ev   []lexer.Event
	err  *lexer.PosError
	cnt  int
}

type seg struct {
	data []byte
	base int
	out  run
	in   run
}

func one(data []byte, base int, preCR, inside, insideBytes int, lim lexer.Limits) run {
	c := &lexer.Collector{}
	var m *lexer.Machine
	if inside >= 0 {
		m = lexer.NewContinued(c, base, inside == 1, insideBytes, lim)
	} else {
		m = lexer.New(c, base-bool2(preCR), lim)
		if preCR {
			m.Feed([]byte{'\r'})
		}
	}
	err := m.Feed(data)
	return run{tail: m.Tail(), ev: c.Events, err: err, cnt: m.Count()}
}

func bool2(b bool) int {
	if b {
		return 1
	}
	return 0
}

func work(s *seg, lim lexer.Limits) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.out = one(s.data, s.base, false, -1, 0, lim) }()
	go func() { defer wg.Done(); s.in = one(s.data, s.base, false, 1, 0, lim) }()
	wg.Wait()
}

type carry struct {
	state  int
	val    string
	start  int
	quoted bool
	bytes  int
	recNo  int
	fldNo  int
}

var countTotal int

// Count 返回最近一次 Parse 状态机处理的字节总数。
func Count() int { return countTotal }

// Parse 切 K 段并行解析，结果与 table.Parse 完全一致。
func Parse(buf []byte, k int, lim lexer.Limits) (*table.Table, error) {
	if k < 1 {
		k = 1
	}
	if len(buf) > 0 && k > len(buf) {
		k = len(buf)
	}
	bnd := bounds(len(buf), k)
	segs := make([]seg, len(bnd)-1)
	for i := range segs {
		segs[i] = seg{data: buf[bnd[i]:bnd[i+1]], base: bnd[i]}
	}
	var wg sync.WaitGroup
	for i := range segs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); work(&segs[i], lim) }()
	}
	wg.Wait()

	bd := table.NewBuilder(lim)
	cr := carry{state: lexer.StStart}
	total := 0
	for i := range segs {
		var r run
		switch cr.state {
		case lexer.StQuoted:
			r = segs[i].in
		case lexer.StCR1:
			r = one(segs[i].data, segs[i].base, true, -1, 0, lim)
		default:
			r = segs[i].out
		}
		total += r.cnt
		if r.err != nil {
			countTotal = total
			return nil, r.err
		}
		evs, e := join(cr, r.ev)
		if e != nil {
			countTotal = total
			return nil, e
		}
		for _, ev := range evs {
			var err error
			if ev.Field {
				err = bd.Field(ev.C)
			} else {
				err = bd.Record(ev.Off)
			}
			if err != nil {
				countTotal = total
				var pe *lexer.PosError
				if errors.As(err, &pe) {
					return nil, pe
				}
				return nil, &lexer.PosError{Err: err, Offset: ev.Off}
			}
		}
		cr = carryFrom(r.tail)
	}
	if e := ending(cr, len(buf)); e != nil {
		countTotal = total
		return nil, e
	}
	t, err := bd.Finish()
	countTotal = total
	if err != nil {
		return nil, &lexer.PosError{Err: err, Offset: len(buf)}
	}
	return t, nil
}

func bounds(n, k int) []int {
	b := make([]int, k+1)
	q, rem := n/k, n%k
	for i := 0; i < k; i++ {
		b[i+1] = b[i] + q
		if i < rem {
			b[i+1]++
		}
	}
	return b
}

func carryFrom(t lexer.Tail) carry {
	return carry{state: t.State, val: t.Value, start: t.Start, quoted: t.Quoted, bytes: t.Bytes}
}

// join 处理跨段同一字段的合并，返回本段应回放的事件。
func join(cr carry, evs []lexer.Event) ([]lexer.Event, *lexer.PosError) {
	switch cr.state {
	case lexer.StPlain:
		if len(evs) == 0 || !evs[0].Field {
			return nil, &lexer.PosError{Err: lexer.ErrQuoteInPlain, Offset: cr.start}
		}
		fallthrough
	case lexer.StQSeen:
		if len(evs) == 0 || !evs[0].Field {
			return nil, &lexer.PosError{Err: lexer.ErrCharsAfterQuote, Offset: cr.start}
		}
		f := evs[0]
		if cr.state == lexer.StPlain {
			f.C.Value = cr.val + f.C.Value
		}
		f.C.Quoted = cr.quoted
		f.C.Start = cr.start
		evs[0] = f
	case lexer.StQuoted:
		// inside 运行：首字段续接前段引号字段。
		if len(evs) > 0 && evs[0].Field {
			f := evs[0]
			f.C.Quoted = true
			f.C.Start = cr.start
			evs[0] = f
		}
	}
	return evs, nil
}

func ending(cr carry, n int) *lexer.PosError {
	switch cr.state {
	case lexer.StCR1, lexer.StCR2:
		return &lexer.PosError{Err: lexer.ErrLoneCR, Offset: n - 1}
	case lexer.StQuoted:
		return &lexer.PosError{Err: lexer.ErrUnclosedQuote, Offset: n}
	}
	return nil
}
