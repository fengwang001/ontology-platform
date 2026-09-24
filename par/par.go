package par

import (
	"sync"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

type collector struct {
	cells []cell.Cell
	ends  []int64
	read  int64
}

func (c *collector) Field(x cell.Cell) error { c.cells = append(c.cells, x); return nil }
func (c *collector) RecordEnd(o int64) error { c.ends = append(c.ends, o); return nil }

type candidate struct {
	state      uint8
	err        *lexer.Error
	rec, field int
	rows       int
}

type result struct {
	cand [4]candidate
	read int64
}

func scan(data []byte, base int64) result {
	r := result{}
	states := [4]uint8{lexer.StateStart, lexer.StatePlain, lexer.StateQuoted, lexer.StateQuoteClose}
	rec, field := [4]int{1, 1, 1, 1}, [4]int{1, 1, 1, 1}
	for i, b := range data {
		r.read++
		o := base + int64(i)
		for j := range states {
			r.cand[j] = step(states[j], b, o, rec[j], field[j], &rec[j], &field[j], &states[j])
		}
	}
	for j := range r.cand {
		r.cand[j].state = states[j]
		r.cand[j].rec = rec[j]
		r.cand[j].field = field[j]
		r.cand[j].rows = rec[j] - 1
	}
	return r
}

func step(st uint8, b byte, o int64, rec, field int, nr, nf *int, ns *uint8) candidate {
	save := func(s uint8) candidate {
		*ns = s
		*nr, *nf = rec, field
		return candidate{state: s, rec: rec, field: field}
	}
	switch st {
	case lexer.StateStart, lexer.StatePlain:
		switch b {
		case '"':
			if st == lexer.StateStart {
				return save(lexer.StateQuoted)
			}
			return candidate{state: st, rec: rec, field: field, err: &lexer.Error{Kind: lexer.KindBareQuote, Offset: o, Record: rec, Field: field}}
		case ',':
			*nf = field + 1
			return save(lexer.StateStart)
		case '\r':
			return save(lexer.StateCR)
		case '\n':
			*nr, *nf = rec+1, 1
			return save(lexer.StateStart)
		default:
			return save(lexer.StatePlain)
		}
	case lexer.StateQuoted:
		if b == '"' {
			return save(lexer.StateQuoteClose)
		}
		return save(lexer.StateQuoted)
	case lexer.StateQuoteClose:
		switch b {
		case '"':
			return save(lexer.StateQuoted)
		case ',':
			*nf = field + 1
			return save(lexer.StateStart)
		case '\r':
			return save(lexer.StateQuoteCR)
		case '\n':
			*nr, *nf = rec+1, 1
			return save(lexer.StateStart)
		default:
			return candidate{state: st, rec: rec, field: field, err: &lexer.Error{Kind: lexer.KindQuoteAfterClose, Offset: o, Record: rec, Field: field}}
		}
	case lexer.StateCR, lexer.StateQuoteCR:
		if b == '\n' {
			*nr, *nf = rec+1, 1
			return save(lexer.StateStart)
		}
		return candidate{state: st, rec: rec, field: field, err: &lexer.Error{Kind: lexer.KindOrphanCR, Offset: o - 1, Record: rec, Field: field}}
	}
	return save(st)
}

func Parse(buf []byte, k int, lim lexer.Limits) (table.Table, error, int64) {
	if k < 1 {
		k = 1
	}
	if n := len(buf); k > n && n > 0 {
		k = n
	}
	parts := split(len(buf), k)
	first := make([]result, len(parts))
	var wg sync.WaitGroup
	for i, p := range parts {
		wg.Add(1)
		go func(i int, p [2]int) { defer wg.Done(); first[i] = scan(buf[p[0]:p[1]], int64(p[0])) }(i, p)
	}
	wg.Wait()

	chosen := make([]uint8, len(parts))
	chosen[0] = lexer.StateStart
	rowsBefore := make([]int, len(parts))
	var reads int64
	for i, r := range first {
		reads += r.read
		if i > 0 {
			prev := first[i-1].cand[chosen[i-1]]
			chosen[i] = mapStart(prev.state)
		}
		c := r.cand[chosen[i]]
		if i+1 < len(parts) {
			rowsBefore[i+1] = rowsBefore[i] + c.rows
		}
	}

	outs := make([]collector, len(parts))
	for i, p := range parts {
		wg.Add(1)
		go func(i int, p [2]int) {
			defer wg.Done()
			c := first[i].cand[chosen[i]]
			b := lexer.Boundary{State: chosen[i], Pos: int64(p[0]), Rec: rowsBefore[i] + 1, Field: 1, Start: int64(p[0]), Active: chosen[i] != lexer.StateStart, Quoted: chosen[i] == lexer.StateQuoted}
			l := lexer.NewAt(&outs[i], lim, b)
			_ = l.Feed(buf[p[0]:p[1]])
			outs[i].read = l.Bytes()
			_ = c
		}(i, p)
	}
	wg.Wait()
	for i := range outs {
		reads += outs[i].read
	}
	return join(outs, lim, parts), firstError(first, chosen), reads
}

func mapStart(end uint8) uint8 {
	if end == lexer.StateQuoted {
		return lexer.StateQuoted
	}
	if end == lexer.StateQuoteClose {
		return lexer.StateQuoteClose
	}
	if end == lexer.StateQuoteCR {
		return lexer.StateQuoteCR
	}
	if end == lexer.StateCR {
		return lexer.StateCR
	}
	return lexer.StateStart
}

func firstError(rs []result, ch []uint8) error {
	for i, r := range rs {
		if e := r.cand[ch[i]].err; e != nil {
			return e
		}
	}
	return nil
}

func split(n, k int) [][2]int {
	out := [][2]int{}
	for i := 0; i < k; i++ {
		if a, z := n*i/k, n*(i+1)/k; z > a {
			out = append(out, [2]int{a, z})
		}
	}
	return out
}

func join(parts []collector, lim lexer.Limits, chunks [][2]int) table.Table {
	p := table.NewParser(lim)
	for _, part := range parts {
		for _, c := range part.cells {
			_ = p.Field(c)
		}
		for _, e := range part.ends {
			_ = p.EmitEnd(e)
		}
	}
	return p.Table()
}
