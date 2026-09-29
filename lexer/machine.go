package lexer

func (c *Carry) add(b byte, lim Limits) error {
	if lim.MaxFieldBytes > 0 && len(c.val) >= lim.MaxFieldBytes {
		return ErrFieldTooLong
	}
	c.val = append(c.val, b)
	return nil
}

func (c *Carry) closeRec(s Sink) {
	if c.recStarted {
		c.emit(s)
		s.EndRecord(c.pos, false)
	} else {
		s.EndRecord(c.pos, true)
	}
	c.state, c.recStarted = StStart, false
	c.rec, c.fld, c.quoted, c.fstart = c.rec+1, 1, false, c.pos+1
	c.val = c.val[:0]
}

func (c *Carry) comma(s Sink, lim Limits) error {
	c.recStarted = true
	c.emit(s)
	c.state, c.quoted, c.fld, c.fstart = StStart, false, c.fld+1, c.pos+1
	c.val = c.val[:0]
	if lim.MaxFields > 0 && c.fld > lim.MaxFields {
		return ErrTooManyFields
	}
	return nil
}

// 动作位：追加字节 / 追加转义引号 / 逗号 / 换行 / 进CR待定 / 开引号。
const (
	aAdd byte = 1 << iota
	aEsc
	aComma
	aNL
	aCR
	aQOpen
)

type rule struct {
	act, next byte
	err       error
}

// tab[state][class]，class: 0逗号 1引号 2\n 3\r 4其他。
var tab = [5][5]rule{
	StStart:        {{aComma, StStart, nil}, {aQOpen, StQuoted, nil}, {aNL, StStart, nil}, {aCR, StCR, nil}, {aAdd, StUnquoted, nil}},
	StUnquoted:     {{aComma, StStart, nil}, {0, 0, ErrQuote}, {aNL, StStart, nil}, {aCR, StCR, nil}, {aAdd, StUnquoted, nil}},
	StQuoted:       {{aAdd, StQuoted, nil}, {0, StQuotePending, nil}, {aAdd, StQuoted, nil}, {aAdd, StQuoted, nil}, {aAdd, StQuoted, nil}},
	StQuotePending: {{aComma, StStart, nil}, {aEsc, StQuoted, nil}, {aNL, StStart, nil}, {aCR, StCR, nil}, {0, 0, ErrGarbage}},
	StCR:           {{0, 0, ErrBareCR}, {0, 0, ErrBareCR}, {aNL, StStart, nil}, {0, 0, ErrBareCR}, {0, 0, ErrBareCR}},
}

func class(b byte) int {
	switch b {
	case ',':
		return 0
	case '"':
		return 1
	case '\n':
		return 2
	case '\r':
		return 3
	}
	return 4
}

// Process 从当前现场消费 buf；每字节恰好处理一次，不回扫。
func Process(c *Carry, buf []byte, s Sink, lim Limits) error {
	for _, b := range buf {
		c.steps++
		r := tab[c.state][class(b)]
		if r.err != nil {
			c.pos++
			return fail(c, r.err)
		}
		var err error
		switch {
		case r.act&aAdd != 0:
			if c.state == StStart && !c.recStarted {
				if e := s.BeginRecord(c.pos); e != nil {
					c.pos++
					return e
				}
			}
			err = c.add(b, lim)
			if c.state == StStart {
				c.recStarted = true
			}
		case r.act&aEsc != 0:
			err = c.add('"', lim)
		case r.act&aComma != 0:
			err = c.comma(s, lim)
		case r.act&aNL != 0:
			c.closeRec(s)
		case r.act&aQOpen != 0:
			if e := s.BeginRecord(c.pos); e != nil {
				c.pos++
				return e
			}
			c.recStarted, c.quoted = true, true
		}
		c.state = r.next
		c.pos++
		if err != nil {
			return fail(c, err)
		}
	}
	return nil
}

// Finalize 在流结束时裁决悬而未决的现场。
func Finalize(c *Carry, s Sink) error {
	switch c.state {
	case StQuoted:
		return fail(c, ErrUnclosedQuote)
	case StCR:
		return fail(c, ErrBareCR)
	case StUnquoted, StQuotePending:
		c.emit(s)
		s.EndRecord(c.pos, false)
	case StStart:
		if c.recStarted {
			c.emit(s)
			s.EndRecord(c.pos, false)
		}
	}
	return nil
}
