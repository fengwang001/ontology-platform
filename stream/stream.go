// Package stream aggregates lines into events and tracks reconnect state.
package stream

import (
	"errors"
	"strconv"
	"strings"

	"ontology/field"
	"ontology/linescan"
)

// ErrDataTooLong reports event data over the maximum; the rejected
// field changes no aggregated state.
var ErrDataTooLong = errors.New("stream: event data exceeds maximum size")

// Event is one dispatched event.
type Event struct {
	Data, Event, ID string
}

// Parser holds event aggregation and reconnect state.
type Parser struct {
	sc                *linescan.Scanner
	maxData, retry    int
	data              []byte
	event, lastID     string
	hasData, hasEvent bool
	pending           []Event
}

// New returns a Parser with per-line and per-event-data byte limits (<= 0: unlimited).
func New(maxLine, maxData int) *Parser {
	return &Parser{sc: linescan.New(maxLine), maxData: maxData}
}

// Feed consumes one chunk of the event stream.
func (p *Parser) Feed(chunk []byte) error { return p.sc.Feed(chunk, p.line) }

// Close flushes a trailing line and dispatches any pending half event.
func (p *Parser) Close() error {
	if err := p.sc.Flush(p.line); err != nil {
		return err
	}
	p.dispatch()
	return nil
}

// Reset clears parsing state for a reconnect, keeping last ID and retry.
func (p *Parser) Reset() { p.sc.Reset(); p.clearEvent(); p.pending = nil }

// LastEventID reports the last event ID seen; safe to query repeatedly.
func (p *Parser) LastEventID() string { return p.lastID }

// Retry reports the current reconnect interval in milliseconds.
func (p *Parser) Retry() int { return p.retry }

// Events drains the dispatched-event queue.
func (p *Parser) Events() []Event { ev := p.pending; p.pending = nil; return ev }

func (p *Parser) line(raw []byte) error {
	if len(raw) == 0 {
		p.dispatch()
		return nil
	}
	f, ok := field.Parse(raw)
	if !ok {
		return nil // comment
	}
	switch f.Name {
	case "data":
		add := len(f.Value)
		if p.hasData {
			add++
		}
		if p.maxData > 0 && len(p.data)+add > p.maxData {
			return ErrDataTooLong
		}
		if p.hasData {
			p.data = append(p.data, '\n')
		}
		p.data = append(p.data, f.Value...)
		p.hasData = true
	case "event":
		p.event, p.hasEvent = f.Value, true
	case "id":
		if strings.IndexByte(f.Value, 0) < 0 {
			p.lastID = f.Value
		}
	case "retry":
		if n, ok := decimal(f.Value); ok {
			p.retry = n
		}
	}
	return nil
}

func (p *Parser) dispatch() {
	if len(p.data) > 0 || p.hasEvent {
		p.pending = append(p.pending, Event{Data: string(p.data), Event: p.event, ID: p.lastID})
	}
	p.clearEvent()
}

func (p *Parser) clearEvent() {
	p.data, p.event = p.data[:0], ""
	p.hasData, p.hasEvent = false, false
}

func decimal(s string) (int, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
