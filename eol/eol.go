package eol

type Event struct {
	Keep      []Token
	Deletions [][2]int
}

type Token struct {
	OrigPos int
	Byte    byte
}

type Decoder struct {
	pendingCR bool
	crPos     int
	origPos   int
}

func (d *Decoder) Position() int { return d.origPos }

func (d *Decoder) Feed(b byte) Event {
		pos := d.origPos
	d.origPos++
	if b != '\r' && b != '\n' {
		if d.pendingCR {
			d.pendingCR = false
			return Event{Keep: []Token{{d.crPos, '\n'}, {pos, b}}}
		}
		return Event{Keep: []Token{{pos, b}}}
	}
	if d.pendingCR {
		if b == '\n' {
			d.pendingCR = false
			return Event{Keep: []Token{{pos, '\n'}}, Deletions: [][2]int{{d.crPos, d.crPos + 1}}}
		}
		old := d.crPos
		d.crPos = pos
		return Event{Keep: []Token{{old, '\n'}}}
	}
	if b == '\n' {
		return Event{Keep: []Token{{pos, '\n'}}}
	}
	d.pendingCR = true
	d.crPos = pos
	return Event{}
}

func (d *Decoder) Close() Event {
	if !d.pendingCR {
		return Event{}
	}
	d.pendingCR = false
	return Event{Keep: []Token{{d.crPos, '\n'}}}
}
