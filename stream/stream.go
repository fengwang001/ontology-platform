// Package stream aggregates SSE fields into events and keeps reconnect state.
package stream

import (
	"errors"
	"strconv"
	"strings"

	"ontology/field"
	"ontology/linescan"
)

var ErrLineTooLong = errors.New("stream: line exceeds maximum length")
var ErrDataTooLong = errors.New("stream: event data exceeds maximum length")

type Event struct{ Event, Data, ID string }

// Parser incrementally parses an event stream; limits <= 0 mean unlimited.
type Parser struct {
	scan                *linescan.Scanner
	maxData             int
	emit                func(Event)
	data, event, lastID string
	hasData, hasEvent   bool
	retry               int
}

func New(maxLine, maxData int, emit func(Event)) *Parser {
	return &Parser{scan: linescan.New(maxLine), maxData: maxData, emit: emit}
}

func (p *Parser) LastEventID() string { return p.lastID }
func (p *Parser) Retry() int          { return p.retry }

func (p *Parser) Reset() {
	p.scan.Reset()
	p.data, p.event, p.hasData, p.hasEvent = "", "", false, false
}

func (p *Parser) Finish() {
	for _, ln := range p.scan.FlushLines() {
		_ = p.line(ln)
	}
	if p.hasData || p.hasEvent {
		p.dispatch()
	}
}

func (p *Parser) dispatch() {
	name := p.event
	if !p.hasEvent {
		name = "message"
	}
	p.emit(Event{Event: name, Data: p.data, ID: p.lastID})
	p.data, p.event, p.hasData, p.hasEvent = "", "", false, false
}

// Feed consumes a chunk; a limit error emits nothing and leaves state unchanged.
func (p *Parser) Feed(chunk []byte) error {
	lines, err := p.scan.Feed(chunk)
	if err != nil {
		return ErrLineTooLong
	}
	d, ev, li, hd, he, st := p.data, p.event, p.lastID, p.hasData, p.hasEvent, p.scan.Snapshot()
	for _, ln := range lines {
		if err := p.line(ln); err != nil {
			p.data, p.event, p.lastID = d, ev, li
			p.hasData, p.hasEvent = hd, he
			p.scan.Restore(st)
			return err
		}
	}
	return nil
}

func (p *Parser) line(ln string) error {
	if ln == "" {
		if p.hasData || p.hasEvent {
			p.dispatch()
		}
		return nil
	}
	f := field.Parse(ln)
	if f.Comment {
		return nil
	}
	switch f.Name {
	case "data":
		next := f.Value
		if p.hasData {
			next = "\n" + next
		}
		if p.maxData > 0 && len(p.data)+len(next) > p.maxData {
			return ErrDataTooLong
		}
		p.data += next
		p.hasData = true
	case "event":
		p.event, p.hasEvent = f.Value, true
	case "id":
		if !strings.ContainsRune(f.Value, 0) {
			p.lastID = f.Value
		}
	case "retry":
		if n, ok := atoiDecimal(f.Value); ok {
			p.retry = n
		}
	}
	return nil
}

func atoiDecimal(s string) (int, bool) {
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
