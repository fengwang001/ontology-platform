// Package eol identifies line endings (\r\n, lone \r, \n) across stream cuts.
// A trailing \r is held pending until the next byte resolves it: \n pairs into
// CRLF, anything else makes it a lone CR line ending.
package eol

// Event marks one input byte. DelCR is a \r paired with a following \n.
// Newline is a line-ending byte position (\r alone or \n). Other is any
// ordinary content byte.
type Event uint8

const (
	Other   Event = iota // ordinary passed-through byte
	DelCR                // \r of a \r\n pair: deleted, next event is a \n Newline
	Newline              // emitted line ending at this byte (\r lone or \n)
)

// Machine is a one-byte-lookahead recognizer. Zero value is ready.
// It is not safe for concurrent use.
type Machine struct {
	pendingCR bool
}

// Feed resolves b against any held \r. Returned events align 1:1 with the
// input bytes, except that a held \r may prepend one event:
// len(ev) is 1+extra when a pending \r is resolved before b[0].
func (m *Machine) Feed(b byte) []Event {
	ev := make([]Event, 0, 2)
	if m.pendingCR {
		m.pendingCR = false
		if b == '\n' {
			ev = append(ev, DelCR)
		} else {
			ev = append(ev, Newline)
		}
	}
	switch b {
	case '\r':
		m.pendingCR = true
	case '\n':
		ev = append(ev, Newline)
	default:
		ev = append(ev, Other)
	}
	return ev
}

// Pending reports whether a \r is currently held unresolved.
func (m *Machine) Pending() bool { return m.pendingCR }

// Suspend detaches the pending \r for cross-segment handoff. It returns true
// when a \r must be carried to the next segment; the caller replays that \r
// there. State is reset as if no \r had ever been seen.
func (m *Machine) Suspend() bool {
	p := m.pendingCR
	m.pendingCR = false
	return p
}

// CloseAtEOF resolves a held \r as a lone line ending at stream end.
func (m *Machine) CloseAtEOF() Event {
	if m.pendingCR {
		m.pendingCR = false
		return Newline
	}
	return Other
}
