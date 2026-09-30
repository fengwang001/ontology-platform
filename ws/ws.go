// Package ws 延迟判定行尾空白：只有看到行尾或流结束才知道一串空白的归属。
package ws

import "errors"

// ErrWhitespaceLimit 表示待定空白超过配置缓冲上限。
var ErrWhitespaceLimit = errors.New("ws: trailing whitespace buffer limit exceeded")

func isWS(b byte) bool { return b == ' ' || b == '\t' }

// Tracker 缓存当前自上一个非空白字节起连续的空格与制表符。非并发安全。
type Tracker struct {
	buf []byte
}

// New 返回干净的 Tracker。limit<=0 表示不限。
func New(limit int) *Tracker { return &Tracker{} }

// Feed 喂入一个字节。若 b 是空白则缓存；否则之前缓存的空白被判定为行内空白，
// 以返回值 run 原样交回（b 本身由调用方另行处理）。ws 报告 b 是否空白。
func (t *Tracker) Feed(b byte, limit int) (run []byte, ws bool, err error) {
	if isWS(b) {
		if limit > 0 && len(t.buf) >= limit {
			return nil, true, ErrWhitespaceLimit
		}
		t.buf = append(t.buf, b)
		return nil, true, nil
	}
	if len(t.buf) > 0 {
		run = t.buf
		t.buf = t.buf[:0]
	}
	return run, false, nil
}

// Drop 在行尾确认时丢弃整串待定空白，返回被丢弃的字节数。
func (t *Tracker) Drop() int {
	n := len(t.buf)
	t.buf = t.buf[:0]
	return n
}

// Flush 在流结束时结算：没有行尾，剩余空白是行内空白，原样交回。
func (t *Tracker) Flush() []byte {
	if len(t.buf) == 0 {
		return nil
	}
	run := t.buf
	t.buf = t.buf[:0]
	return run
}

// Len 返回当前已缓存尚未判定的空白字节数。
func (t *Tracker) Len() int { return len(t.buf) }
