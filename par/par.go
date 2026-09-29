package par

import (
	"errors"
	"sync"
	"ontology/lexer"
	"ontology/table"
)

type result struct {
	blocks [2]lexer.Block
	errs [2]error
}

type replay struct {
	b *table.Builder
	base int
}

func (r replay) Start(off int) error { return r.b.Start(r.base+off) }
func (r replay) Append(data []byte) error { return r.b.Append(data) }
func (r replay) EndField(off int, q bool) { r.b.EndField(r.base+off, q) }
func (r replay) EndRecord(off int) { r.b.EndRecord(r.base+off) }

func Parse(buf []byte, k int, lim table.Limits) (*table.Table, error) {
	if k < 1 { k = 1 }
	if k > len(buf) { k = max(1, len(buf)) }
	bounds := bounds(len(buf), k)
	rs := make([]result, len(bounds))
	var wg sync.WaitGroup
	for i, bd := range bounds {
		wg.Add(1)
		go func(i, start, end int) {
			defer wg.Done()
			p := buf[start:end]
			rs[i].blocks[0], rs[i].errs[0] = lexer.ScanBlock(p, false)
			rs[i].blocks[1], rs[i].errs[1] = lexer.ScanBlock(p, true)
		}(i, bd[0], bd[1])
	}
	wg.Wait()

	b := table.NewBuilder()
	end := lexer.StateIDExport(0)
	inside := false
	var processed int64
	for i, bd := range bounds {
		choice := 0
		if inside { choice = 1 }
		blk, scanErr := rs[i].blocks[choice], rs[i].errs[choice]
		processed += blk.Bytes
		ev := blk.Events
		if inside && len(ev) > 0 && ev[0].Kind == lexer.BStart {
			ev = ev[1:]
		}
		if !inside && continuation(end) && len(ev) > 0 {
			if ev[0].Kind == lexer.BStart { ev = ev[1:] }
		}
		r := replay{b: b, base: bd[0]}
		var replayErr error
		for _, e := range ev {
			switch e.Kind {
			case lexer.BStart: replayErr = r.Start(e.Off)
			case lexer.BAppend: replayErr = r.Append(e.Data)
			case lexer.BEndField: r.EndField(e.Off, e.Quoted)
			case lexer.BEndRecord: r.EndRecord(e.Off)
			}
			if replayErr != nil { break }
		}
		if replayErr != nil || b.PendingError() != nil {
			return nil, b.Error(globalize(replayErr, bd[0]))
		}
		if scanErr != nil {
			return nil, b.Error(globalize(scanErr, bd[0]))
		}
		end = blk.End
		inside = end == 2 || end == 3
	}
	closeMachine := lexer.New(lim)
	closeMachine.SetState(end, inside)
	if err := closeMachine.Close(replay{b: b, base: len(buf)}); err != nil {
		return nil, b.Error(err)
	}
	return b.Finish(), nil
}

func continuation(s lexer.StateExport) bool {
	return s == lexer.StateExport(1) || s == lexer.StateExport(4) || s == lexer.StateExport(5)
}

func globalize(err error, base int) error {
	var pe *lexer.PosError
	if errors.As(err, &pe) {
		return &lexer.PosError{Err: pe.Err, Offset: pe.Offset+base}
	}
	return err
}

func bounds(n, k int) [][2]int {
	out := make([][2]int, 0, k)
	for i := 0; i < k; i++ {
		start := i * n / k
		end := (i+1) * n / k
		if start == end && len(out) > 0 { continue }
		out = append(out, [2]int{start, end})
	}
	if len(out) == 0 { out = append(out, [2]int{0,0}) }
	return out
}
