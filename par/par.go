// Package par 把一个缓冲区按任意字节偏移切成 K 段并行解析后拼接。
package par

import (
	"sync"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
)

type evKind int

const (
	evCell evKind = iota
	evRec
	evBlank
)

type ev struct {
	kind evKind
	c    cell.Cell
}

type run struct {
	evs     []ev
	st      lexer.State
	pending *cell.Cell
	err     *lexer.Error
}

type collector struct{ evs []ev }

func (c *collector) add(cell0 cell.Cell, rec, blank bool) {
	switch {
	case blank:
		c.evs = append(c.evs, ev{kind: evBlank})
	case rec:
		c.evs = append(c.evs, ev{kind: evRec})
	default:
		c.evs = append(c.evs, ev{kind: evCell, c: cell0})
	}
}

// oneRun 用一个前置假字节模拟「起点在引号外(U)」或「起点在引号内(Q)」解析 seg。
// 假字节落在 -1 偏移，事件偏移均为绝对偏移。
func oneRun(seg []byte, base int, quoteStart, final bool, cfg lexer.Config) *run {
	col := &collector{}
	l := lexer.New(cfg, col.add)
	prep := byte('x')
	if quoteStart {
		prep = '"'
	}
	in := append([]byte{prep}, seg...)
	r := &run{}
	if err := l.Feed(in); err != nil {
		e := err.(*lexer.Error)
		r.err = fixErr(e, base)
	} else if final {
		if err := l.Close(); err != nil {
			e := err.(*lexer.Error)
			r.err = fixErr(e, base)
		}
	}
	st, pend, _ := l.Snapshot()
	r.st, r.pending = st, pend
	for _, e := range col.evs {
		if e.kind == evCell && e.c.Start < base && base > 0 {
			continue // 中段：假字节造成的假单元由拼接端与左段未闭合字段合并
		}
		r.evs = append(r.evs, e)
	}
	if r.pending != nil {
		c := *r.pending
		if quoteStart {
			c.Value, c.Start = c.Value[1:], c.Start-1
		}
		r.pending = &c
	}
	return r
}

func fixErr(e *lexer.Error, base int) *lexer.Error {
	cp := *e
	cp.Byte = e.Byte - 1 + base
	return &cp
}

// Parse 把 buf 切成 K 段并行解析，结果等价于单线程流式解析。
func Parse(buf []byte, k int, cfg lexer.Config) (*table.Table, error) {
	if k < 1 {
		k = 1
	}
	if k > len(buf) {
		k = len(buf)
	}
	bounds := make([]int, k+1)
	for i := 0; i < k; i++ {
		bounds[i] = len(buf) * i / k
	}
	bounds[k] = len(buf)
	runs := make([][2]*run, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seg := buf[bounds[i]:bounds[i+1]]
			final := i == k-1
			runs[i][0] = oneRun(seg, bounds[i], false, final, cfg)
			runs[i][1] = oneRun(seg, bounds[i], true, final, cfg)
		}(i)
	}
	wg.Wait()
	b := table.NewBuilder()
	em := b.Emit()
	state := lexer.StateFS
	for i := 0; i < k; i++ {
		r := pick(runs[i], state)
		if r.err != nil {
			return nil, r.err
		}
		adapt(r, state, bounds[i], i == 0, em)
		state = r.st
	}
	return &b.Table, b.Err()
}

func pick(rs [2]*run, st lexer.State) *run {
	if st == lexer.StateQ {
		return rs[1]
	}
	return rs[0]
}

// adapt 把段内事件按入口状态归约为全局事件。
// U 遍在 FS 入口会凭空造出一个首单元（前置 x 与首个分隔符之间），须抑制到首个记录边界；
// U/Q 遍在字段中间入口时，前置字节造出的首单元与左段未闭合字段合并。
func adapt(r *run, st lexer.State, base int, firstSeg bool, em lexer.Emit) {
	inField := st == lexer.StateU || st == lexer.StateQ
	suppress := st == lexer.StateFS
	merged := false
	for _, e := range r.evs {
		switch {
		case inField && !merged:
			if e.kind == evCell {
				if e.c.Quoted {
					e.c.Value = e.c.Value[1:]
				}
				e.c.Start = base
				em(e.c, false, false)
				merged = true
			} else {
				em(e.c, false, e.kind == evBlank)
			}
		case suppress:
			if e.kind == evCell {
				// 跳过该假单元
			} else {
				suppress = false
				em(e.c, false, e.kind == evBlank)
			}
		default:
			em(e.c, e.kind == evRec, e.kind == evBlank)
		}
	}
	if suppress && firstSeg {
		// 整段只有一个假单元且无边界（例如空输入）：无事件即无表，正确
	}
}
