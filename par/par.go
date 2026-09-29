// Package par 把大缓冲区按任意字节偏移切成 K 段并行规范化后拼接，
// 输出与映射和单线程完全一致。
package par

import (
	"sync"

	"ontology/eol"
	"ontology/norm"
	"ontology/span"
)

type chunk struct {
	out       []byte
	runs      []span.Run
	tailWS    int // 被行尾/CR 删除的尾部空白数（必然删除）
	tailCR    bool
	pendCR    bool
	pendBytes []byte // 段尾未判定空白
	leadKind  byte   // 跳过段首空白后第一个字节：\n \r 'o' 或 0（整段空白/空）
	leadWS    int
	err       *norm.Error
}

func classify(p []byte) byte {
	for i, b := range p {
		if b == ' ' || b == '\t' {
			continue
		}
		if i > 0 && b == '\n' {
			return '\n'
		}
		if i > 0 && b == '\r' {
			// 仅当它是单独行尾（其后不是 \n）才算作删除判定。
			if i+1 >= len(p) || p[i+1] != '\n' {
				return '\n'
			}
			if i+1 < len(p) {
				return '\n' // \r\n 同样是行尾
			}
		}
		return 'o'
	}
	return 0
}

// Run 把 p 按 cuts（切分点原文偏移，升序）切成 len(cuts)+1 段并行处理。
func Run(p []byte, cuts []int, cfg norm.Config) ([]byte, *span.Map, error) {
	k := len(cuts) + 1
	bounds := append(append([]int{0}, cuts...), len(p))
	chunks := make([]chunk, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := norm.Config{MaxPending: cfg.MaxPending, StrictNUL: cfg.StrictNUL,
				MaxOutput: cfg.MaxOutput, RawSegments: true}
			nr := norm.New(c)
			seg := p[bounds[i]:bounds[i+1]]
			_, err := nr.Write(seg)
			if err == nil {
				err = nr.Close()
			}
			chunks[i] = chunk{
				out:       nr.Output(),
				runs:      nr.Runs(),
				tailCR:    nr.TailCR,
				pendCR:    nr.PendCR,
				pendBytes: nr.Pending(),
				leadKind:  classify(seg),
				leadWS:    nr.LeadWS,
			}
			chunks[i].tailWS = nr.TailWS
			if e, ok := err.(*norm.Error); ok {
				chunks[i].err = e
			}
		}(i)
	}
	wg.Wait()

	// 段内错误偏移平移为全局原文偏移，取最先触发者。
	for i := range chunks {
		if chunks[i].err != nil {
			e := *chunks[i].err
			e.Offset += bounds[i]
			return nil, nil, &e
		}
	}

	// 从右向左决定每段"段尾未判定空白"的命运。
	deletePend := make([]bool, k)
	fate := byte('\n') // 最后一段 EOF 按行尾删除
	for i := k - 1; i >= 0; i-- {
		if chunks[i].leadKind == 0 {
			deletePend[i] = fate == '\n'
			continue
		}
		deletePend[i] = chunks[i].leadKind == '\n'
		fate = chunks[i].leadKind
	}

	mp := span.New()
	var out []byte
	op, np := 0, 0
	for i := range chunks {
		c := &chunks[i]
		co, cn := c.out, c.runs
		// 缝修正 1：左段待定 \r + 右段首字节 \n（右段直接以 \n 开始）
		if i > 0 && chunks[i-1].pendCR && firstByteIsLF(p[bounds[i]:]) {
			co = co[:len(co)-1]
			cn = mutateLastKeepToDelete(cn)
		}
		// 段尾未判定空白按命运改 keep/delete。
		if len(c.pendBytes) > 0 {
			if deletePend[i] {
				cn = appendDelete(cn, len(c.pendBytes))
			} else {
				cn = appendKeep(cn, len(c.pendBytes))
				co = append(co, c.pendBytes...)
			}
		}
		for _, r := range cn {
			switch {
			case r.OL > 0 && r.NL > 0:
				mp.Keep(r.OL, r.NL)
			case r.OL > 0:
				mp.Delete(r.OL)
			case r.NL > 0:
				mp.Insert(r.NL)
			}
		}
		out = append(out, co...)
		_ = op
		_ = np
	}

	// 待定空白上限（跨缝累计）与单流一致校验。
	if cfg.MaxPending > 0 {
		if off, ok := wsLimitOffset(p, cfg.MaxPending); ok {
			return nil, nil, &norm.Error{Code: norm.WSBufferLimit, Offset: off}
		}
	}
	out, mp = norm.ApplyEnding(mp, out, cfg.Ending)
	if cfg.MaxOutput > 0 && len(out) > cfg.MaxOutput {
		return nil, nil, &norm.Error{Code: norm.OutputLimit, Offset: len(p)}
	}
	return out, mp, nil
}

func firstByteIsLF(seg []byte) bool { return len(seg) > 0 && seg[0] == '\n' }

func mutateLastKeepToDelete(rs []span.Run) []span.Run {
	out := make([]span.Run, len(rs))
	copy(out, rs)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].OL > 0 && out[i].NL > 0 {
			if out[i].OL == 1 {
				out[i].NL = 0
			} else {
				last := out[i]
				out[i].OL--
				out[i].NL--
				out = append(out[:i+1], append([]span.Run{{
					OP: last.OP + last.OL - 1, OL: 1}}, out[i+1:]...)...)
			}
			break
		}
	}
	return out
}

func appendDelete(rs []span.Run, n int) []span.Run {
	if len(rs) > 0 && rs[len(rs)-1].NL == 0 {
		rs[len(rs)-1].OL += n
		return rs
	}
	lastOP := 0
	for _, r := range rs {
		lastOP = r.OP + r.OL
	}
	return append(rs, span.Run{OP: lastOP, OL: n})
}

func appendKeep(rs []span.Run, n int) []span.Run {
	lastOP, lastNP := 0, 0
	for _, r := range rs {
		lastOP, lastNP = r.OP+r.OL, r.NP+r.NL
	}
	return append(rs, span.Run{OP: lastOP, OL: n, NP: lastNP, NL: n})

}

// wsLimitOffset 以单流语义计算待定空白超限触发偏移。
func wsLimitOffset(p []byte, limit int) (int, bool) {
	d := eol.New()
	pend := 0
	for i, b := range p {
		switch {
		case b == ' ' || b == '\t':
			if d.Pending() {
				continue
			}
			if pend == limit {
				return i, true
			}
			pend++
		case b == '\r':
			d.Feed(b)
			pend = 0
		case b == '\n':
			d.Feed(b)
			pend = 0
		default:
			pend = 0
		}
	}
	return 0, false
}
