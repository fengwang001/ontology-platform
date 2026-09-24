package parse

import (
	"strconv"
	"sync"

	"ontology/source"
)

// Record is one successfully parsed input record.
type Record struct {
	Pos   int64
	Key   string
	Value int64
}

// Parser parses deterministic "key=number" records and counts bad records.
type Parser struct {
	mu    sync.Mutex
	bad   int64
	last  int64
}

// Parse returns false for a bad record without mutating the bad aggregate.
func (p *Parser) Parse(raw source.Raw) (Record, bool) {
	text := string(raw.Data)
	eq := -1
	for i := range text {
		if text[i] == '=' {
			eq = i
			break
		}
	}
	if eq < 0 {
		p.noteBad(raw.Pos)
		return Record{}, false
	}
	value, err := strconv.ParseInt(text[eq+1:], 10, 64)
	if err != nil {
		p.noteBad(raw.Pos)
		return Record{}, false
	}
	p.mu.Lock()
	p.last = raw.Pos
	p.mu.Unlock()
	return Record{Pos: raw.Pos, Key: text[:eq], Value: value}, true
}

func (p *Parser) noteBad(pos int64) {
	p.mu.Lock()
	p.bad++
	p.last = pos
	p.mu.Unlock()
}

// Bad returns the number of malformed records observed.
func (p *Parser) Bad() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bad
}

// Last returns the highest source position seen by Parse.
func (p *Parser) Last() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}
