// Package stream 聚合逻辑行为事件，空行派发，并维护重连状态。
package stream

import (
	"errors"
	"strconv"
	"strings"

	"ontology/field"
	"ontology/linescan"
)

// ErrDataTooLarge 表示单事件数据字节数超限，返回时已聚合状态不变。
var ErrDataTooLarge = errors.New("stream: event data exceeds max bytes")

// DefaultRetry 是默认重连间隔（毫秒）。
const DefaultRetry = 3000

// Event 是派发的一个事件。Parser 是事件流解析器，跨 Write 调用保留全部状态。
type Event struct{ Name, Data, ID string }
type Parser struct {
	sc               *linescan.Scanner
	maxLine, maxData int
	parts            []string
	dataLen          int
	name             string
	hasName          bool
	pendID, lastID   string
	retry            int
}

// New 创建解析器，maxLine 为单行最大字节、maxData 为单事件数据最大字节（<=0 不限）。
func New(maxLine, maxData int) *Parser {
	return &Parser{sc: linescan.New(maxLine), maxLine: maxLine, maxData: maxData, retry: DefaultRetry}
}

// Write 喂入一个字节块，返回本次派发的事件。出错时状态不变。
func (p *Parser) Write(chunk []byte) ([]Event, error) {
	sp, ss := *p, *p.sc
	lines, err := p.sc.Feed(chunk)
	if err != nil {
		return nil, err
	}
	var evs []Event
	for _, ln := range lines {
		if err := p.line(ln, &evs); err != nil {
			*p, *p.sc = sp, ss
			return nil, err
		}
	}
	return evs, nil
}

// End 显式收尾：冲刷末尾半行并派发尚未派发的事件。
func (p *Parser) End() ([]Event, error) {
	sp, ss := *p, *p.sc
	var evs []Event
	for _, ln := range p.sc.End() {
		if err := p.line(ln, &evs); err != nil {
			*p, *p.sc = sp, ss
			return nil, err
		}
	}
	if ev, ok := p.dispatch(); ok {
		evs = append(evs, ev)
	}
	return evs, nil
}

// Reset 模拟重连：解析状态清零，lastEventID 与重连间隔保留。
func (p *Parser) Reset() {
	id, retry := p.lastID, p.retry
	*p = *New(p.maxLine, p.maxData)
	p.lastID, p.pendID, p.retry = id, id, retry
}

// LastEventID 返回最后派发事件的 id。Retry 返回当前重连间隔（毫秒）。
func (p *Parser) LastEventID() string { return p.lastID }
func (p *Parser) Retry() int          { return p.retry }
func (p *Parser) line(ln string, evs *[]Event) error {
	if ln == "" {
		if ev, ok := p.dispatch(); ok {
			*evs = append(*evs, ev)
		}
		return nil
	}
	f := field.Parse(ln) // 注释行字段名为空，落入 default 被忽略
	switch f.Name {
	case "data":
		add := len(f.Value) + min(len(p.parts), 1) // min 项是拼接用的 \n
		if p.maxData > 0 && p.dataLen+add > p.maxData {
			return ErrDataTooLarge
		}
		p.parts = append(p.parts, f.Value)
		p.dataLen += add
	case "event":
		p.name, p.hasName = f.Value, true
	case "id":
		if strings.IndexByte(f.Value, 0) < 0 {
			p.pendID = f.Value
		}
	case "retry":
		if n, err := strconv.Atoi(f.Value); err == nil && n >= 0 &&
			strings.TrimLeft(f.Value, "0123456789") == "" {
			p.retry = n
		}
	}
	return nil
}
func (p *Parser) dispatch() (Event, bool) {
	if p.dataLen == 0 && !p.hasName {
		return Event{}, false
	}
	p.lastID = p.pendID
	ev := Event{Name: p.name, Data: strings.Join(p.parts, "\n"), ID: p.lastID}
	p.parts, p.dataLen, p.name, p.hasName = nil, 0, "", false
	return ev, true
}
