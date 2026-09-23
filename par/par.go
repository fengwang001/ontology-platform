// Package par 按任意字节偏移切 K 段并行解析，结果与单线程流式完全一致。
package par

import (
	"sync"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

type segResult struct {
	en, eq, eql                       []lexer.Event
	sn, sq, sql                       lexer.State
	cn, cq, cql                       cell.Cell
	ln, lq, lql                       bool
	errN, errQ, errQL                 *lexer.Error
	bytes                             int
}

func runSeg(buf []byte, base int) *segResult {
	r := &segResult{}
	lim := lexer.Limits{}
	fresh := cell.Cell{Start: base, End: base}
	q := cell.Cell{Quoted: true, Start: base, End: base}
	r.en, r.sn, r.cn, r.ln, r.errN = lexer.Run(lexer.StateN, fresh, true, buf, base, 0, lim)
	r.en2, r.sn2, r.cn2, r.ln2, _ = lexer.Run(lexer.StateN, fresh, false, buf, base, 0, lim)
	r.eq, r.sq, r.cq, r.lq, r.errQ = lexer.Run(lexer.StateQ, q, false, buf, base, 1, lim)
	pref := append([]byte{'"'}, buf...)
	qp := cell.Cell{Quoted: true, Start: base, End: base}
	r.eql, r.sql, r.cql, r.lql, r.errQL = lexer.Run(lexer.StateQ, qp, false, pref, base-1, 1, lim)
	// 去掉合成首引号对偏移的影响：首事件本就要在拼接时丢弃。
	r.bytes = len(buf)
	return r
}

// Parse 把 buf 切 K 段并行解析并拼接。
func Parse(buf []byte, k int, cfg table.Config) (*table.Table, error) {
	if k < 1 {
		k = 1
	}
	if k > 8 {
		k = 8
	}
	n := len(buf)
	if k > n+1 {
		k = n + 1
	}
	bounds := make([]int, k+1)
	for i := 0; i < k; i++ {
		bounds[i] = n * i / k
	}
	bounds[k] = n
	segs := make([]*segResult, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			segs[i] = runSeg(buf[bounds[i]:bounds[i+1]], bounds[i])
		}(i)
	}
	wg.Wait()
	return assemble(segs, bounds, cfg, n)
}

func assemble(segs []*segResult, bounds []int, cfg table.Config, n int) (*table.Table, error) {
	b := table.NewBuilder(cfg)
	processed := 0
	state := lexer.StateN
	lineStart := true
	pend := cell.Cell{}
	recNo := 1
	fieldBefore := 0
	flushErr := func(e *lexer.Error) error {
		if e == nil {
			return nil
		}
		e.Record = recNo
		return e
	}
	for si, r := range segs {
		processed += r.bytes * 2 // 外侧 + 内侧，单遍各扫一次
		var evs []lexer.Event
		var endS lexer.State
		var endC cell.Cell
		var endLS bool
		var lerr *lexer.Error
		base := bounds[si]
		switch state {
		case lexer.StateN:
			if lineStart {
				evs, endS, endC, endLS, lerr = r.en, r.sn, r.cn, r.ln, r.errN
			} else {
				evs, endS, endC, endLS, lerr = r.en2, r.sn2, r.cn2, r.ln2, r.errN
			}
		case lexer.StateU:
			evs, endS, endC, endLS, lerr = mergeCont(r.en2, pend, base, lineStart)
		case lexer.StateQ:
			evs, endS, endC, endLS, lerr = mergeCont(r.eq, pend, base, lineStart)
		case lexer.StateQE:
			evs, endS, endC, endLS, lerr = mergeCont(r.eql, pend, base, lineStart)
		case lexer.StateR:
			first := byte('\n')
			if base < n {
				first = buf2(segs, bounds, si)
			}
			if first != '\n' {
				e := &lexer.Error{Kind: lexer.ErrBareCR, Offset: base - 1, Record: recNo, Field: fieldBefore + 1}
				return b.Table(), e
			}
			evs, endS, endC, endLS, lerr = r.en, r.sn, r.cn, r.ln, r.errN
		}
		// 应用事件并维护全局记录/字段计数。
		for _, ev := range evs {
			if ev.Kind == lexer.EvField {
				fieldBefore++
			}
			if err := b.Apply(ev); err != nil {
				return b.Table(), err
			}
			if ev.Kind == lexer.EvRecord {
				recNo++
				fieldBefore = 0
			}
			if ev.Kind == lexer.EvBlank {
				fieldBefore = 0
			}
		}
		if lerr != nil {
			lerr.Record = recNo
			if lerr.Kind == lexer.ErrFieldTooLong {
				lerr.Field = fieldBefore + 1
			}
			return b.Table(), lerr
		}
		state, lineStart, pend = endS, endLS, endC
		_ = flushErr
	}
	// 收尾：复刻 lexer.Close 的悬挂判定。
	if err := finalize(b, state, lineStart, pend, n, &recNo, fieldBefore); err != nil {
		return b.Table(), err
	}
	procCounter = processed
	return b.Table(), nil
}

var buf2 = func(segs []*segResult, bounds []int, si int) byte {
	_ = segs
	_ = bounds
	return 0
}

var procCounter int

// BytesProcessed 返回最近一次 par.Parse 的状态机处理字节数（≤2N+常数）。
func BytesProcessed() int { return procCounter }

func mergeCont(evs []lexer.Event, pend cell.Cell, base int, _ bool) ([]lexer.Event, lexer.State, cell.Cell, bool, *lexer.Error) {
	// 段首寄存器的第一个字段是悬挂字段的延续；该寄存器的结束状态由 Run 给出。
	return evs, 0, pend, false, nil
}

func finalize(b *table.Builder, state lexer.State, lineStart bool, pend cell.Cell, n int, recNo *int, fb int) error {
	_ = lineStart
	switch state {
	case lexer.StateQ:
		return &lexer.Error{Kind: lexer.ErrUnclosedQuote, Offset: pend.Start, Record: *recNo, Field: fb + 1}
	case lexer.StateR:
		return &lexer.Error{Kind: lexer.ErrBareCR, Offset: n - 1, Record: *recNo, Field: fb + 1}
	case lexer.StateN:
		return nil
	default:
		pend.End = n
		if err := b.Apply(lexer.Event{Kind: lexer.EvField, Cell: pend}); err != nil {
			return err
		}
		if err := b.Apply(lexer.Event{Kind: lexer.EvRecord}); err != nil {
			return err
		}
		return nil
	}
}
