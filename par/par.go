// Package par 把缓冲区按任意字节偏移切成 K 段并行解析后拼接，
// 结果与 table 单线程流式解析完全一致。
package par

import (
	"sync"

	"ontology/lexer"
	"ontology/table"
)

type opKind int

const (
	opStart opKind = iota
	opData
	opEnd
	opRec
)

type op struct {
	k      opKind
	off    int
	quoted bool
	data   []byte
}

type recorder struct{ ops []op }

func (r *recorder) FieldStart(off int, quoted bool) {
	r.ops = append(r.ops, op{k: opStart, off: off, quoted: quoted})
}
func (r *recorder) FieldData(p []byte) {
	cp := make([]byte, len(p))
	copy(cp, p)
	r.ops = append(r.ops, op{k: opData, data: cp})
}
func (r *recorder) FieldEnd(off int) { r.ops = append(r.ops, op{k: opEnd, off: off}) }
func (r *recorder) Record()          { r.ops = append(r.ops, op{k: opRec}) }

type result struct {
	a0ops []op
	a1ops []op
	st0   lexer.Status
	st1   lexer.Status
	err0  error
	err1  error
	off0  int
	off1  int
	n     int
}

// P 是一次并行解析的句柄，持有非导出的字节处理计数。
type P struct{ processed int }

func runSeg(buf []byte, base int) result {
	r0, r1 := recorder{}, recorder{}
	m0 := lexer.New(&r0)
	m1 := lexer.NewInQuote(&r1)
	e0 := m0.Feed(buf)
	e1 := m1.Feed(buf)
	return result{
		r0.ops, r1.ops, m0.Status(), m1.Status(), e0, e1,
		m0.ErrOffset(), m1.ErrOffset(), m0.Processed() + m1.Processed(),
	}
}

type carry int

const (
	cNone carry = iota // 段首是干净的字段/记录边界
	cU                // 跨段：未引号字段中间
	cQ                // 跨段：引号字段中间
	cE                // 跨段：引号字段刚见引号（待定）
	cC                // 跨段：\r 待定
	cP                // 跨段：逗号/行尾后的待开始字段（空行判定关键）
)

// Processed 返回所有状态机处理字节的总次数。
func (p *P) Processed() int { return p.processed }

// Parse 把 buf 切成 K 段并行解析。K<=1 等价单段。
func (p *P) Parse(buf []byte, k int, lim table.Limits, header bool) (*table.Table, error) {
	if k < 1 {
		k = 1
	}
	n := len(buf)
	if k > n {
		k = n
	}
	if k == 0 {
		k = 1
}
	bounds := make([]int, k+1)
	for i := 0; i < k; i++ {
		bounds[i] = n * i / k
	}
	bounds[k] = n
	res := make([]result, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i] = runSeg(buf[bounds[i]:bounds[i+1]], bounds[i])
		}(i)
	}
	wg.Wait()
	p.processed = 0
	for i := range res {
		p.processed += res[i].n
	}
	return p.stitch(buf, bounds, res, lim, header)
}

func carryOf(st lexer.Status) carry {
	switch st.State {
	case lexer.SQuoted:
		return cQ
	case lexer.SQuoteEnd:
		return cE
	case lexer.SCR:
		return cC
	case lexer.SUnquoted:
		return cU
	case lexer.SStart:
		if st.Pending {
			return cNone
		}
		return cU // 段恰好在逗号后结束，字段已开始尚无字节
	}
	return cNone
}

func emit(b *table.Builder, e op) {
	switch e.k {
	case opStart:
		b.FieldStart(e.off, e.quoted)
	case opData:
		b.FieldData(e.data)
	case opEnd:
		b.FieldEnd(e.off)
	case opRec:
		b.Record()
	}
}

func play(b *table.Builder, ops []op) {
	for _, e := range ops {
		emit(b, e)
		if b.Err() != nil {
			return
		}
	}
}

// replay 回放一段词法事件；语法错误用当前字段坐标包装后返回。
func replay(b *table.Builder, ops []op, serr error, soff int) error {
	play(b, ops)
	if b.Err() != nil {
		return b.Err()
	}
	if serr != nil {
		return b.Fail(serr, soff)
	}
	return nil
}

// dropFirstStart 丢掉本段首字节产生的多余 FieldStart（续接字段修正用）。
func dropFirstStart(ops []op) []op {
	if len(ops) > 0 && ops[0].k == opStart {
		return ops[1:]
	}
	return ops
}

func (p *P) stitch(buf []byte, bounds []int, res []result, lim table.Limits, header bool) (*table.Table, error) {
	b := table.NewBuilder(lim, header)
	// carryNone 表示真实状态在"行首/逗号后的待开始"，且本段首个字节由本段机器处理。
	// cU/cE/cC 的首字节不能用 A0 的解释，需要按左段微状态逐字节修正。
	kind := cNone
	last := lexer.Status{}
	k := len(res)
	for i := 0; i < k; i++ {
		seg := buf[bounds[i]:bounds[i+1]]
		if len(seg) == 0 {
			continue // 空段不改变跨段状态
		}
		r := res[i]
		base := bounds[i]
		var ops []op
		var serr error
		var soff int
		var st lexer.Status
		switch kind {
		case cQ:
			ops, serr, soff, st = r.a1ops, r.err1, r.off1, r.st1
		default:
			ops, serr, soff, st = r.a0ops, r.err0, r.off0, r.st0
		}
		// 用真实跨段微状态修正本段第一个字节的解释（最多修正 1 字节）。
		if kind == cU || kind == cE || kind == cC || kind == cP {
			first := seg[0]
			ops = dropFirstStart(ops)
			switch kind {
			case cU:
				switch first {
				case '"':
					return nil, b.Fail(lexer.ErrBareQuote, base)
				case '\r':
					// 进入 CR 待定；后续由下段首字节或 EOF 定性
				case '\n':
					ops = append([]op{{k: opEnd, off: base}, {k: opRec}}, ops...)
				case ',':
					ops = append([]op{{k: opEnd, off: base}}, ops...)
				default:
					// A0 已把该字节作为新字段内容，修正为续接字段内容
				}
			case cE:
				switch first {
				case '"':
					ops = append([]op{{k: opData, data: []byte{'"'}}}, ops...)
				case '\n':
					ops = append([]op{{k: opEnd, off: base}, {k: opRec}}, ops...)
				case ',':
					ops = append([]op{{k: opEnd, off: base}}, ops...)
				case '\r':
				default:
					return nil, b.Fail(lexer.ErrCharsAfterQuote, base)
				}
			case cC:
				switch first {
				case '\n':
					if last.CRFromField {
						ops = append([]op{{k: opEnd, off: base + 1}, {k: opRec}}, ops...)
					} // 空行：A0 自身也跳过，去掉其可能产生的 FieldStart 即可
				default:
					return nil, b.Fail(lexer.ErrLoneCR, last.CROff)
				}
			case cP:
				switch first {
				case '\n':
					// 真实：空行（跳过）；A0 的 SStart+\n 也跳过，无需补事件
				case '\r':
					// CR 待定（空行位置）；由下段首字节或 EOF 定性
				default:
					ops = append([]op{{k: opStart, off: base, quoted: false}}, ops...)
				}
			}
		}
		if err := replay(b, ops, serr, soff); err != nil {
			return &tbl{b}, err
		}
		last = st
		switch kind {
		case cQ:
			kind = carryOf(st)
		default:
			kind = carryOf(st)
		}
		// 首字节修正可能改变出口微状态，按下段首字节在拼接时再修正，
		// 这里仅在 \r 待定时保留 CR 偏移。
	}
	// EOF：消解待定微状态。
	switch kind {
	case cQ:
		return nil, b.Fail(lexer.ErrUnclosedQuote, len(buf))
	case cC:
		if last.CRFromField {
			b.FieldEnd(len(buf))
			b.Record()
		}
		if err := b.Err(); err != nil {
			return &tbl{b}, err
		}
		return nil, b.Fail(lexer.ErrLoneCR, last.CROff)
	default:
		// 未结束的字段（cU/cE/干净待开始的未完成记录）补成记录
		if kind == cU || kind == cE {
			b.FieldEnd(len(buf))
			b.Record()
		}
		if err := b.Err(); err != nil {
			return &tbl{b}, err
		}
	}
	return &tbl{b}, nil
}

// tbl 是个小辅助，避免在每个返回点写 b.Table()。
type tbl = table.Table
