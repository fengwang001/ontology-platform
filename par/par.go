// Package par 按任意字节偏移把缓冲切成 K 段并行解析后顺序拼接。
package par

import (
	"sync"

	"ontology/lexer"
	"ontology/table"
)

type evKind int

const (
	evStart evKind = iota
	evData
	evQuote
	evEnd
	evRecord
)

// ev 是一个 lexer 事件的段内记录；off 为段内字节偏移（除 evData）。
type ev struct {
	kind   evKind
	off    int
	lfOff  int
	quoted bool
	data   string
}

// collector 记录一个假设的全部事件与终态；每个 goroutine 私有，无共享。
type collector struct {
	evs     []ev
	lex     *lexer.Lexer
	recEnds int
	seps    int
}

func (c *collector) CellStart(off int) error {
	c.evs = append(c.evs, ev{kind: evStart, off: off})
	return nil
}
func (c *collector) Data(p []byte) error {
	c.evs = append(c.evs, ev{kind: evData, data: string(p)})
	return nil
}
func (c *collector) EscapedQuote(off int) error {
	c.evs = append(c.evs, ev{kind: evQuote, off: off})
	return nil
}
func (c *collector) CellEnd(off int, quoted bool) error {
	c.evs = append(c.evs, ev{kind: evEnd, off: off, quoted: quoted})
	c.seps++
	return nil
}
func (c *collector) RecordEnd(lfOff int) error {
	c.evs = append(c.evs, ev{kind: evRecord, lfOff: lfOff})
	c.recEnds++
	c.seps = 0
	return nil
}

type result struct{ a, b *collector }

func runOne(chunk []byte, lim lexer.Limits, resumeQuoted bool) *collector {
	c := &collector{}
	if resumeQuoted {
		c.lex = lexer.NewResumeQuoted(c, lim)
	} else {
		c.lex = lexer.New(c, lim)
	}
	_ = c.lex.Feed(chunk)
	if c.lex.FinalErr() == nil {
		_ = c.lex.Close()
	}
	return c
}

// selected 记录被选中假设与其此前已完成记录数（供错误号换算）。
type selected struct {
	c          *collector
	prevRecs   int
	prevFields int
	base       int
}

// Parse 把 buf 按字节近似均分为 K 段并行解析，再顺序拼接，结果与 table.Parse 相同。
func Parse(buf []byte, K int, lim lexer.Limits) (*table.Table, table.Stats, error) {
	if K < 1 {
		K = 1
	}
	if K > len(buf) {
		K = len(buf)
	}
	if len(buf) == 0 {
		return table.Parse(buf, lim)
	}
	res := make([]result, K)
	var wg sync.WaitGroup
	for k := 0; k < K; k++ {
		start := k * len(buf) / K
		end := (k + 1) * len(buf) / K
		wg.Add(1)
		go func(k, start, end int) {
			defer wg.Done()
			chunk := buf[start:end]
			ca, cb := runOne(chunk, lim, false), runOne(chunk, lim, true)
			if end < len(buf) {
				if ca.lex.PendingCR() {
					_ = ca.lex.PeekNext(buf[end])
				}
				if cb.lex.PendingCR() {
					_ = cb.lex.PeekNext(buf[end])
				}
			}
			res[k] = result{a: ca, b: cb}
		}(k, start, end)
	}
	wg.Wait()

	// 第一遍：从左到右确定每段真实假设，并累计此前记录/字段数。
	picks := make([]selected, 0, K)
	prevInQuoted := false
	var totalBytes int64
	prevRecs, prevFields := 0, 0
	for k := range res {
		base := k * len(buf) / K
		totalBytes += res[k].a.lex.ByteCount() + res[k].b.lex.ByteCount()
		c := res[k].a
		if prevInQuoted {
			c = res[k].b
		}
		picks = append(picks, selected{c: c, prevRecs: prevRecs, prevFields: prevFields, base: base})
		prevInQuoted = c.lex.InQuoted()
		prevRecs += c.recEnds
		if c.recEnds > 0 {
			prevFields = 0
		}
		prevFields += c.seps
	}

	// 第二遍：把选定事件重放进 Builder，直到首个错误；跳过跨段续接字段的重复 CellStart。
	b := table.NewBuilder(lim)
	prevOpen := false
	var firstErr error
	var failPick selected
	for _, s := range picks {
		skipStart := prevOpen
		if e := replay(b, s.c, s.base, skipStart); e != nil {
			firstErr, failPick = e, s
			break
		}
		prevOpen = s.c.lex.InQuoted()
	}
	if firstErr != nil {
		firstErr = renumber(firstErr, failPick)
	}
	return b.Table(), table.Stats{BytesProcessed: totalBytes}, firstErr
}

func replay(b *table.Builder, c *collector, base int, skipStart bool) error {
	for _, e := range c.evs {
		switch e.kind {
		case evStart:
			if skipStart {
				skipStart = false
				continue
			}
			if err := b.CellStart(base + e.off); err != nil {
				return err
			}
		case evData:
			if err := b.Data([]byte(e.data)); err != nil {
				return err
			}
		case evQuote:
			if err := b.EscapedQuote(base + e.off); err != nil {
				return err
			}
		case evEnd:
			if err := b.CellEnd(base+e.off, e.quoted); err != nil {
				return err
			}
		case evRecord:
			if err := b.RecordEnd(base + e.lfOff); err != nil {
				return err
			}
		}
	}
	return b.Err()
}

// renumber 把错误的段内坐标换算为全局：偏移加段基址，记录/字段号加此前完成量。
func renumber(err error, s selected) error {
	e := table.AsError(err)
	if e == nil {
		return err
	}
	e.Offset += s.base
	e.Record += s.prevRecs
	if s.prevRecs == 0 && s.prevFields > 0 {
		e.Field += s.prevFields
	}
	return e
}
