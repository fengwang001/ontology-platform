package norm

import (
	"ontology/eol"
	"ontology/ws"
)

// Write feeds one cut. On terminal error the instance enters the terminal
// state and earlier output is retained.
func (n *Normalizer) Write(p []byte) (int, error) {
	if n.done() {
		return 0, ErrClosed
	}
	for _, b := range p {
		if n.opt.StrictNUL && b == 0 {
			return 0, n.fail(&NULError{Off: n.off})
		}
		e0, ok0, e1 := n.dec.Feed(b)
		base := n.off
		if ok0 && e0.N == 2 {
			base--
		}
		if ok0 && n.event(e0, base) != nil {
			return 0, n.err
		}
		if e1.N != 0 && n.event(e1, n.off) != nil {
			return 0, n.err
		}
		n.off++
	}
	return len(p), nil
}

func (n *Normalizer) event(ev eol.Event, start int64) error {
	switch ev.Kind {
	case eol.Line:
		return n.line(start, ev.N, ev.B)
	case eol.Data:
		return n.data(ev.B, start)
	}
	return nil // Pending: the \r awaits its following byte
}

func (n *Normalizer) data(b byte, start int64) error {
	if ws.IsWS(b) {
		if !n.tr.Active() {
			n.wsStart = start
		}
		if err := n.tr.Add(b, start); err != nil {
			return n.fail(err)
		}
		n.buf = append(n.buf, b)
		return nil
	}
	if err := n.flushWS(false); err != nil {
		return err
	}
	if err := n.room(1); err != nil {
		return err
	}
	n.sb.Add(start+1, n.sb.OLen()+1)
	n.out = append(n.out, b)
	return nil
}

func (n *Normalizer) flushWS(del bool) error {
	if !n.tr.Active() {
		return nil
	}
	l := int64(n.tr.Len())
	if del {
		n.sb.Add(n.wsStart+l, n.sb.OLen())
	} else {
		if err := n.room(l); err != nil {
			return err
		}
		n.out = append(n.out, n.buf...)
		n.sb.Add(n.wsStart+l, n.sb.OLen()+l)
	}
	n.tr.Flush()
	n.buf = n.buf[:0]
	return nil
}

func (n *Normalizer) line(start int64, consumed int, b byte) error {
	if err := n.room(1); err != nil {
		return err
	}
	ol := n.sb.OLen()
	switch {
	case consumed == 2: // \r\n: delete \r, keep \n
		n.sb.Add(start+1, ol)
		n.sb.Add(start+2, ol+1)
	case b == '\n': // plain \n
		n.sb.Add(start+1, ol+1)
	default: // lone \r: delete \r, insert the normalized \n
		n.sb.Add(start+1, ol)
		n.sb.Add(start+1, ol+1)
	}
	n.out = append(n.out, '\n')
	n.tr.Flush()
	return nil
}
