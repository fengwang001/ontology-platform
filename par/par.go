// Package par 把缓冲区按任意字节偏移切成 K 段并行解析并顺序拼接。
package par

import (
	"sync"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

// Result 是并行解析结果。
type Result struct {
	Table *table.Table
	Err   *cell.PosError
	Steps int64
}

func cuts(n, k int) []int {
	if n > 0 && k > n {
		k = n
	}
	b := make([]int, k+1)
	for i := 0; i <= k; i++ {
		b[i] = n * i / k
	}
	return b
}

// Parse 用 K 个 goroutine 并行解析 buf。
func Parse(buf []byte, k int, lim cell.Limits) Result {
	return ParseCuts(buf, cuts(len(buf), k), lim)
}

// ParseCuts 按切点 bounds（含 0 与 len(buf)）并行解析。
func ParseCuts(buf []byte, bounds []int, lim cell.Limits) Result {
	k := len(bounds) - 1
	outs := make([]lexer.Segment, k)
	ins := make([]lexer.Segment, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			last := i == k-1
			outs[i] = lexer.RunSegment(buf, bounds[i], bounds[i+1], lexer.ModeOut, lim, last)
			ins[i] = lexer.RunSegment(buf, bounds[i], bounds[i+1], lexer.ModeIn, lim, last)
		}(i)
	}
	wg.Wait()
	j := joiner{b: table.NewBuilder(lim), lim: lim, buf: buf, bounds: bounds, out: outs, in: ins}
	return j.run()
}

type joiner struct {
	b          *table.Builder
	lim        cell.Limits
	buf        []byte
	bounds     []int
	out, in    []lexer.Segment
	pend       *cell.Cell
	pendQuoCR  bool // 悬挂引号字段的结束是 CRLF
	pendQuoEnd int  // 悬挂引号字段闭合引号后偏移
	steps      int64
}

func (j *joiner) run() Result {
	mode := lexer.ModeOut
	for i := 0; i < len(j.out); i++ {
		j.steps += j.out[i].Steps + j.in[i].Steps
		switch mode {
		case lexer.ModeOut:
			j.replay(j.out[i].Events)
		case lexer.ModeIn:
			j.joinIn(i)
		case lexer.ModeOutCR:
			j.joinCR(i)
		case lexer.ModeOutQ:
			j.joinQ(i)
		}
		if j.b.Err() != nil {
			t, _ := j.b.Result()
			return Result{Table: t, Err: j.b.Err(), Steps: j.steps}
		}
		mode = j.nextMode(i, mode)
	}
	t, err := j.b.Result()
	return Result{Table: t, Err: err, Steps: j.steps}
}

func (j *joiner) nextMode(i int, used lexer.Mode) lexer.Mode {
	var ex lexer.Exit
	switch used {
	case lexer.ModeOut, lexer.ModeOutCR:
		ex = j.out[i].Exit
	case lexer.ModeIn, lexer.ModeOutQ:
		seg := lexer.RunSegment(j.buf, j.bounds[i]+1, j.bounds[i+1],
			lexer.ModeIn, j.lim, i == len(j.bounds)-2)
		j.steps += seg.Steps
		ex = seg.Exit
	}
	switch ex.StateCode() {
	case lexer.ExitQuo:
		return lexer.ModeIn
	case lexer.ExitCR:
		return lexer.ModeOutCR
	case lexer.ExitQuoQ:
		return lexer.ModeOutQ
	default:
		return lexer.ModeOut
	}
}

func (j *joiner) replay(evs []lexer.Event) {
	for _, ev := range evs {
		j.dispatch(ev)
	}
}

func (j *joiner) dispatch(ev lexer.Event) {
	switch ev.Kind {
	case lexer.EvCell:
		if j.pend != nil {
			ev.Cell.Value = j.pend.Value + ev.Cell.Value
			ev.Cell.Start = j.pend.Start
			j.pend = nil
		}
		j.b.Cell(ev.Cell)
	case lexer.EvEndRec:
		j.b.EndRec()
	case lexer.EvOpen:
		c := ev.Cell
		if j.pend != nil {
			c.Value = j.pend.Value + c.Value
			c.Start = j.pend.Start
		}
		j.pend = &c
	case lexer.EvErr:
		j.b.Error(ev.Err)
	}
}

func (j *joiner) joinIn(i int) {
	for k, ev := range j.in[i].Events {
		if ev.Kind == lexer.EvCell {
			j.dispatch(ev)
			j.replay(j.in[i].Events[k+1:])
			return
		}
		if ev.Kind == lexer.EvErr {
			j.b.Error(ev.Err)
			return
		}
		if ev.Kind == lexer.EvOpen {
			c := ev.Cell
			if j.pend != nil {
				c.Value = j.pend.Value + c.Value
				c.Start = j.pend.Start
			}
			j.pend = &c
			return
		}
	}
}

func (j *joiner) joinCR(i int) {
	start := j.bounds[i]
	if start >= len(j.buf) || j.buf[start] != '\n' {
		j.b.Error(&cell.PosError{Err: cell.ErrLoneCR, Offset: start - 1})
		return
	}
	end := start - 1
	if j.pend != nil {
		c := *j.pend
		if c.Quoted {
			c.End = j.pendQuoEnd
		} else {
			c.End = end
		}
		j.b.Cell(c)
		j.pend = nil
	}
	j.b.EndRec()
	j.replayAfterEnd(j.out[i].Events)
}

func (j *joiner) replayAfterEnd(evs []lexer.Event) {
	for k, ev := range evs {
		if ev.Kind == lexer.EvEndRec {
			j.replay(evs[k+1:])
			return
		}
		if ev.Kind == lexer.EvErr {
			j.b.Error(ev.Err)
			return
		}
	}
}

func (j *joiner) joinQ(i int) {
	start := j.bounds[i]
	if start >= len(j.buf) {
		if j.pend != nil {
			j.pend.Quoted = true
		}
		return
	}
	switch j.buf[start] {
	case '"':
		if j.pend != nil {
			j.pend.Value += "\""
		}
		seg := lexer.RunSegment(j.buf, start+1, j.bounds[i+1],
			lexer.ModeIn, j.lim, i == len(j.bounds)-2)
		j.steps += seg.Steps
		j.replay(seg.Events)
	case ',', '\n':
		if j.pend != nil {
			c := *j.pend
			c.Quoted = true
			c.End = start
			j.b.Cell(c)
			j.pend = nil
		}
		if j.buf[start] == '\n' {
			j.b.EndRec()
			j.replayAfterEnd(j.out[i].Events)
			return
		}
		j.replayAfterFirstCell(j.out[i].Events)
	case '\r':
		// out 引擎把它当 CR 待定；手动把悬挂引号字段闭合偏移记下后沿用 out 流。
		if j.pend != nil {
			j.pend.Quoted = true
			j.pendQuoEnd = start
		}
		j.replay(j.out[i].Events)
	default:
		j.b.Error(&cell.PosError{Err: cell.ErrGarbageAfterQuote, Offset: start})
	}
}

func (j *joiner) replayAfterFirstCell(evs []lexer.Event) {
	for k, ev := range evs {
		if ev.Kind == lexer.EvCell {
			j.replay(evs[k+1:])
			return
		}
		if ev.Kind == lexer.EvErr {
			j.b.Error(ev.Err)
			return
		}
	}
}
