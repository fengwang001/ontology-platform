// Package par 按任意字节偏移把缓冲切成 K 段并行解析并拼接。
package par

import (
	"sync"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

type seg struct {
	lo, hi   int
	base     int
	h0, h1   *lexer.Machine
	e0, e1   []lexer.Event
	s0, s1   lexer.Snap
	proc     int
}

func runSeg(buf []byte, lo, hi, maxField int) *seg {
	s := &seg{lo: lo, hi: hi, base: lo}
	collect := func(m *lexer.Machine) ([]lexer.Event, lexer.Snap, int) {
		var ev []lexer.Event
		_ = m.Run(buf[lo:hi], lo, func(e lexer.Event) error {
			ev = append(ev, e)
			if e.Kind == lexer.KErr {
				return lexer.ErrFieldTooLong
			}
			return nil
		})
		return ev, m.Snapshot(), m.Processed()
	}
	m0 := lexer.New()
	m1 := lexer.NewInside(0, -1, 0)
	s.e0, s.s0, p0 := collect(m0)
	s.e1, s.s1, p1 := collect(m1)
	s.h0, s.h1 = m0, m1
	s.proc = p0 + p1
	return s
}

type carry struct {
	lexer.Snap
	has bool
}

func mergeCell(c0 cell.Cell, v string, quoted bool, start int) cell.Cell {
	return cell.Cell{Value: v + c0.Value, Quoted: quoted, Start: start, End: c0.End}
}

// limitOff 在开 cell 跨段超限的错误路径上重跑一次受限机，
// 精确定位第一个超限字节（仅错误路径，成本受限于上限）。
func limitOff(buf []byte, start, maxField int) int {
	m := lexer.NewInside(maxField, start, 0)
	off := start
	_ = m.Run(buf[start:], start, func(e lexer.Event) error {
		if e.Kind == lexer.KErr {
			off = e.Off
			return lexer.ErrFieldTooLong
		}
		return nil
	})
	return off
}

// Parse 切成 K 段并行解析后拼接，结果与 table.Parse 完全相同。
func Parse(buf []byte, k int, lim table.Limits) (*table.Table, error) {
	if k < 1 {
		k = 1
	}
	if k > 8 {
		k = 8
	}
	if k == 1 {
		return table.Parse(buf, lim)
	}
	n := len(buf)
	bounds := make([]int, k+1)
	for i := 0; i < k; i++ {
		bounds[i] = n * i / k
	}
	bounds[k] = n
	segs := make([]*seg, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			segs[i] = runSeg(buf, bounds[i], bounds[i+1], lim.MaxFieldBytes)
		}(i)
	}
	wg.Wait()

	b := table.NewBuilder(lim)
	var car carry
	totalProc := 0
	flush := func(ev lexer.Event) error { return b.Sink(ev) }
	_ = flush
	for si, s := range segs {
		totalProc += s.proc
		inside := car.has && car.St == lexer.Quoted
		events, snap := s.e0, s.s0
		if inside {
			events, snap = s.e1, s.s1
		}
		if car.has && car.St == lexer.QQuote {
			// 精确续接：从快照重跑本段，再选择真实事件流。
			m := lexer.Resume(car.Snap).WithFieldLimit(0)
			var ev []lexer.Event
			_ = m.Run(buf[s.lo:s.hi], s.lo, func(e lexer.Event) error {
				ev = append(ev, e)
				if e.Kind == lexer.KErr {
					return lexer.ErrFieldTooLong
				}
				return nil
			})
			events, snap = ev, m.Snapshot()
			totalProc += s.hi - s.lo
		}
		merged := false
		qCarry := car.has && (car.St == lexer.Quoted || car.St == lexer.Bare)
		emit := func(ev lexer.Event) error {
			if qCarry && !merged {
				merged = true
				if ev.Kind == lexer.KCell {
					if lim.MaxFieldBytes > 0 && car.OpenLen+len(ev.Cell.Value) > lim.MaxFieldBytes {
						off := limitOff(buf, car.OpenStart, lim.MaxFieldBytes)
						return b.Sink(lexer.Event{Kind: lexer.KErr, Err: lexer.ErrFieldTooLong, Off: off})
					}
					return b.Sink(lexer.Event{Kind: lexer.KCell,
						Cell: mergeCell(ev.Cell, car.Val, car.St == lexer.Quoted, car.OpenStart)})
				}
				// carry 为空 cell 且本段无对应 cell（如 CR/EOF）：补发 carry。
				if err := b.Sink(lexer.Event{Kind: lexer.KCell,
					Cell: cell.Cell{Value: car.Val, Quoted: car.St == lexer.Quoted,
						Start: car.OpenStart, End: s.lo}}); err != nil {
					return err
				}
			}
			if ev.Kind == lexer.KCell && lim.MaxFieldBytes > 0 && len(ev.Cell.Value) > lim.MaxFieldBytes {
				off := limitOff(buf, ev.Cell.Start, lim.MaxFieldBytes)
				ev.Off = off
				ev.Err = lexer.ErrFieldTooLong
				ev.Kind = lexer.KErr
				ev.Cell = cell.Cell{}
			}
			return b.Sink(ev)
		}
		halt := false
		for _, ev := range events {
			if err := emit(ev); err != nil {
				halt = true
				break
			}
		}
		if halt {
			return b.Table(), b.Err()
		}
		last := si == k-1
		if last {
			end := n
			var fin []lexer.Event
			m := lexer.Resume(snap)
			_ = m.Finish(end, func(e lexer.Event) error { fin = append(fin, e); return nil })
			q2 := snap.St == lexer.Quoted || snap.St == lexer.Bare
			if q2 {
				if err := b.Sink(lexer.Event{Kind: lexer.KCell,
					Cell: cell.Cell{Value: snap.Val, Quoted: snap.St == lexer.Quoted,
						Start: snap.OpenStart, End: end}}); err != nil {
					return b.Table(), err
				}
				totalProc++
			}
			for _, ev := range fin {
				if ev.Kind != lexer.KCell || !q2 {
					if err := b.Sink(ev); err != nil {
						return b.Table(), err
					}
				}
			}
			if err := b.Finish(n); err != nil {
				return b.Table(), err
			}
			break
		}
		car = carry{Snap: snap, has: true}
}
	processed = totalProc
	return b.Table(), b.Err()
}

var processed int

// Processed 返回最近一次 Parse 的字节处理总次数。
func Processed() int { return processed }
