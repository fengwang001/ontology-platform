// Package lexer 是可暂停续传的 CSV 逐字节状态机，依赖 cell 包的错误定义。
package lexer

import "ontology/cell"

// State 是状态机的五种状态。
type State uint8

const (
	SStart State = iota // 字段起点
	SUnq                // 未引号字段中
	SQte                // 引号字段中
	SSqp                // 引号字段中刚见引号
	SCRp                // 行尾 CR 待定
)

// Kind 标识词法事件种类。
type Kind uint8

const (
	KField Kind = iota // 一个字段片段完成（跨段同字段会出现多个且无 Sep/Rec 间隔）
	KSep               // 逗号：字段边界
	KRec               // 换行：记录边界，Offset 为 \n 的全局偏移
)

// Event 是词法事件；Data 仅 KField 有效，偏移均为全局字节偏移。
type Event struct {
	Kind        Kind
	Slot, Off   int
	Quoted      bool
	Data        []byte
}

// Snap 是状态机可序列化的暂停点，供 par 段间携带。
type Snap struct {
	St         State
	Slot       int
	FieldBytes int
	CRoff      int
	RecOpen    bool // 当前记录体内是否已出现过字段槽/内容（用于区分空行）
}

// Run 在给定片段上跑一次状态机。base 为片段首字节的全局偏移，snap 为入口状态。
// 字段超长检查基于 snap.FieldBytes 继续计数，超限立即返回错误。
func Run(p []byte, base int, snap Snap, maxField int) (ev []Event, end Snap, n int64, err error) {
	st, slot, fb, cr := snap.St, snap.Slot, snap.FieldBytes, snap.CRoff
	recOpen := snap.RecOpen
	val := []byte{}
	open, quoted := st == SQte || st == SUnq, st == SQte
	add := func(b byte, at int) bool {
		val = append(val, b)
		fb++
		if maxField > 0 && fb > maxField {
			err = mkErr(cell.ErrFieldTooLong, at)
			return false
		}
		return true
	}
	endField := func(off, sl int, q bool) {
		ev = append(ev, Event{Kind: KField, Slot: sl, Off: off, Quoted: q, Data: val})
		val = []byte{}
		fb = 0
	}
	for i := 0; i < len(p); i++ {
		n++
		at := base + i
		b := p[i]
		switch st {
		case SStart:
			switch {
			case b == ',':
				endField(at, at, false)
				ev = append(ev, Event{Kind: KSep})
				slot, open, recOpen = at+1, false, true
			case b == '"':
				slot, open, quoted, recOpen, st = at, true, true, true, SQte
			case b == '\r':
				cr, recOpen, st = at, true, SCRp
			case b == '\n':
				if !recOpen {
					ev = append(ev, Event{Kind: KRec, Off: at, Slot: -1})
					slot = at + 1
					break
				}
				ev = append(ev, Event{Kind: KRec, Off: at})
				slot, recOpen, st = at+1, false, SStart
			default:
				slot, open, quoted, recOpen = at, true, false, true
				if !add(b, at) {
					return
				}
				st = SUnq
			}
		case SUnq:
			switch {
			case b == ',':
				endField(at, slot, quoted)
				ev = append(ev, Event{Kind: KSep})
				slot, open = at+1, false
			case b == '"':
				err = mkErr(cell.ErrQuoteInField, at)
				return
			case b == '\r':
				cr, st = at, SCRp
			case b == '\n':
				endField(at, slot, quoted)
				ev = append(ev, Event{Kind: KRec, Off: at})
				slot, open, recOpen, st = at+1, false, false, SStart
			default:
				if !add(b, at) {
					return
				}
			}
		case SQte:
			if b == '"' {
				st = SSqp
			} else if !add(b, at) {
				return
			}
		case SSqp:
			switch {
			case b == ',':
				endField(at, slot, quoted)
				ev = append(ev, Event{Kind: KSep})
				slot, open, recOpen, st = at+1, false, true, SStart
			case b == '"':
				if !add('"', at) {
					return
				}
				st = SQte
			case b == '\r':
				endField(at, slot, quoted)
				open, cr, st = false, at, SCRp
			case b == '\n':
				endField(at, slot, quoted)
				ev = append(ev, Event{Kind: KRec, Off: at})
				slot, open, st = at+1, false, SStart
			default:
				err = mkErr(cell.ErrAfterQuote, at)
				return
			}
		case SCRp:
			if b == '\n' {
				if open {
					endField(cr, slot, quoted)
				}
				ev = append(ev, Event{Kind: KRec, Off: at})
				slot, open, recOpen, st = at+1, false, false, SStart
			} else {
				err = mkErr(cell.ErrBareCR, cr)
				return
			}
		}
	}
	end = Snap{St: st, Slot: slot, FieldBytes: fb, CRoff: cr, RecOpen: recOpen}
	return
}

func mkErr(sentinel error, off int) error {
	return &cell.Error{Op: "lex", Off: off, Err: sentinel}
}

// L 是单线程流式状态机包装；非并发安全。
type L struct {
	snap     Snap
	maxField int
	base     int
	count    int64
	term     error
}

// New 创建流式 lexer；maxField<=0 表示不限制单字段字节数。
func New(maxField int) *L { return &L{snap: Snap{St: SStart}, maxField: maxField} }

// Feed 喂入一段字节，返回本段产生的词法事件。终态后返回钉住的错误。
func (l *L) Feed(p []byte) ([]Event, error) {
	if l.term != nil {
		return nil, l.term
	}
	ev, end, n, err := Run(p, l.base, l.snap, l.maxField)
	l.count += n
	l.base += len(p)
	if err != nil {
		l.term = err
		return ev, err
	}
	l.snap = end
	return ev, nil
}

// Close 宣告流结束；返回尾部事件（最后一条无换行记录）或结尾错误。
func (l *L) Close() ([]Event, error) {
	if l.term != nil {
		return nil, l.term
	}
	st := l.snap.St
	switch st {
	case SQte:
		l.term = errors.Join(cell.ErrUnclosedQuote, posErr(l.snap.Slot, 0, 0))
		return nil, l.term
	case SCRp:
		l.term = errors.Join(cell.ErrBareCR, posErr(l.snap.CRoff, 0, 0))
		return nil, l.term
	case SUnq, SSqp:
		off := l.base
		ev := []Event{{Kind: KField, Slot: l.snap.Slot, Off: off, Quoted: st == SSqp}}
		ev = append(ev, Event{Kind: KRec, Off: off})
		return ev, nil
	}
	return nil, nil
}

// Count 返回字节被状态机处理的总次数。
func (l *L) Count() int64 { return l.count }
