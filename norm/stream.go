package norm

import "ontology/ws"

import "ontology/eol"

// Write 喂入一段字节。
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.done {
		return 0, Error{Kind: ErrClosed, Offset: n.ap}
	}
	for k, c := range p {
		pos := n.fed
		n.fed++
		if c == 0 && n.cfg.StrictNUL {
			return k, n.fail(ErrNUL, pos)
		}
		n.pend = append(n.pend, c)
		ev, resolved := n.dec.Feed(c)
		switch {
		case ev == eol.CRPending:
			if resolved == eol.CR {
				if err := n.crAlone(pos-1, len(n.pend)-1); err != nil {
					return k, err
				}
			}
		case ws.IsWhitespace(c):
			if n.wsRel < 0 {
				n.wsRel = pos - n.ap
			}
			if lim := n.cfg.WhitespaceLimit; lim > 0 && pos-(n.ap+n.wsRel)+1 > lim {
				n.pend = n.pend[:len(n.pend)-1]
				return k, n.fail(ErrWhitespaceLimit, pos)
			}
		case ev == eol.LF:
			if err := n.lfEnd(pos, resolved == eol.CRPending); err != nil {
				return k, err
			}
		default:
			if resolved == eol.CR {
				if err := n.crAlone(pos-1, len(n.pend)-1); err != nil {
					return k, err
				}
			}
			if n.cfg.OutputLimit > 0 && n.op+(pos-n.ap+1) > n.cfg.OutputLimit {
				n.pend = n.pend[:len(n.pend)-1]
				return k, n.fail(ErrOutputLimit, pos)
			}
			n.literals(pos - n.ap + 1)
		}
	}
	return len(p), nil
}

// Close 结束流：残留空白按行尾删除、残留 \r 独立成行，再套用末尾策略。
func (n *Normalizer) Close() error {
	if n.done {
		if n.term != 0 {
			return Error{Kind: n.term, Offset: n.ap}
		}
		return nil
	}
	n.done = true
	if n.dec.End() == eol.CR {
		if err := n.crAlone(n.fed-1, len(n.pend)); err != nil {
			return err
		}
	}
	if len(n.pend) > 0 {
		ws := n.wsRel
		if ws < 0 {
			ws = len(n.pend)
		}
		n.copyBytes(n.pend[:ws], false)
		n.deleteBytes(len(n.pend) - ws)
		n.pend = n.pend[:0]
	}
	return n.applyTrailing()
}
