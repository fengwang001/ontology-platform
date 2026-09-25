package par

import (
	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

func stitch(buf []byte, bounds []int, h0, h1, cr []*runOut, lim table.Limits) Result {
	b := table.New(lim)
	var total int64
	k := len(h0)
	mode := lexer.EndBoundary
	var held cell.Cell

	for i := 0; i < k; i++ {
		total += h0[i].bytes + h1[i].bytes
		var r *runOut
		switch mode {
		case lexer.EndQuote:
			r = h1[i]
		case lexer.EndUnquoted:
			if err := b.ReplayField(held); err != nil {
				return Result{Table: b.Table(), Err: err, Bytes: total}
			}
			r = h0[i]
		case lexer.EndQuoteEnd:
			if err := b.ReplayField(held); err != nil {
				return Result{Table: b.Table(), Err: err, Bytes: total}
			}
			r = h0[i]
		case lexer.EndCRPending:
			seg := buf[bounds[i]:bounds[i+1]]
			cr[i] = runOne(seg, bounds[i], lim.MaxFieldBytes, lexer.Init{
				StartCR: true, FStart: held.Start, CRValue: []byte(held.Value),
			})
			total += cr[i].bytes
			r = cr[i]
		default:
			r = h0[i]
		}
		if r.err != nil {
			err := b.ReplayError(r.err)
			return Result{Table: b.Table(), Err: err, Bytes: total}
		}
		for _, ev := range r.events {
			var err error
			if ev.kind == evField {
				err = b.ReplayField(ev.c)
			} else {
				err = b.ReplayEndRecord()
			}
			if err != nil {
				return Result{Table: b.Table(), Err: err, Bytes: total}
			}
		}
		mode = r.snap.Kind
		held = r.snap.Partial
	}

	var err error
	switch mode {
	case lexer.EndQuote:
		err = b.ReplayError(&lexer.PosError{Err: lexer.ErrUnclosedQuote, Offset: len(buf), Record: 0, Field: 0})
	case lexer.EndCRPending:
		err = b.ReplayError(&lexer.PosError{Err: lexer.ErrOrphanCR, Offset: len(buf) - 1, Record: 0, Field: 0})
	case lexer.EndUnquoted, lexer.EndQuoteEnd:
		if e := b.ReplayField(held); e != nil {
			err = e
		} else {
			err = b.ReplayEndRecord()
		}
	}
	if err != nil {
		return Result{Table: b.Table(), Err: err, Bytes: total}
	}
	return Result{Table: b.Table(), Bytes: total}
}
