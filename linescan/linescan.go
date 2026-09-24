// Package linescan 把任意大小的字节块增量切分成逻辑行。
// 支持 \n、\r\n、\r 三种行尾，跨块半行被缓存，且 \r\n 绝不会产生两个空行。
package linescan

import (
	"bytes"
	"errors"
)

// ErrLineTooLong 表示某一行（不含行尾）超过配置的最大字节数。
var ErrLineTooLong = errors.New("linescan: line too long")

// State 是 Splitter 的可序列化检查点，用于原子性地回滚一次喂入。
type State struct {
	Buf       []byte
	CRPending bool
	BOMSeen   bool
	BOMPrefix []byte
}

// Splitter 是有状态的增量切行器，零值不可用，请用 New 构造。
type Splitter struct {
	buf       []byte
	crPending bool
	bomSeen   bool
	bomPrefix []byte
	maxLine   int
}

// New 创建切行器；maxLine<=0 表示不限制单行字节数。
func New(maxLine int) *Splitter {
	return &Splitter{maxLine: maxLine}
}

var bom = []byte{0xEF, 0xBB, 0xBF}

func (s *Splitter) emitTo(f func([]byte), out *[]string) {
	line := string(s.buf)
	s.buf = s.buf[:0]
	if f != nil {
		f([]byte(line))
	} else {
		*out = append(*out, line)
	}
}

func (s *Splitter) addByte(c byte, f func([]byte), out *[]string) error {
	s.buf = append(s.buf, c)
	if s.maxLine > 0 && len(s.buf) > s.maxLine {
		return ErrLineTooLong
	}
	return nil
}

// Push 喂入一个字节块，对每个完整行调用一次 onLine（或收集进返回切片）。
// 返回错误时本次调用产生的任何输出应由上层连同检查点一并丢弃。
func (s *Splitter) Push(chunk []byte, onLine func([]byte)) ([]string, error) {
	var lines []string
	emit := func() { s.emitTo(onLine, &lines) }
	for _, c := range chunk {
		if !s.bomSeen { // 仅在流首做 BOM 前缀匹配
			if len(s.bomPrefix) < 3 && c == bom[len(s.bomPrefix)] {
				s.bomPrefix = append(s.bomPrefix, c)
				if len(s.bomPrefix) == 3 {
					s.bomSeen, s.bomPrefix = true, s.bomPrefix[:0]
				}
				continue
			}
			if len(s.bomPrefix) > 0 { // 匹配中断：已存前缀是普通字节
				for _, p := range s.bomPrefix {
					if err := s.addByte(p, onLine, &lines); err != nil {
						return lines, err
					}
				}
				s.bomPrefix = s.bomPrefix[:0]
			}
			s.bomSeen = true
		}
		switch {
		case c == '\r':
			emit()
			s.crPending = true
		case c == '\n':
			if s.crPending { // 上一块末尾 \r 的配对 LF：吞掉
				s.crPending = false
			} else {
				emit()
			}
		default:
			s.crPending = false
			if err := s.addByte(c, onLine, &lines); err != nil {
				return lines, err
			}
		}
	}
	return lines, nil
}

// Flush 返回尚未以行尾结束的残余半行；没有残余时返回 nil，不产生空行。
func (s *Splitter) Flush() []byte {
	if len(s.buf) == 0 {
		return nil
	}
	tail := bytes.Clone(s.buf)
	s.buf = s.buf[:0]
	return tail
}

// Snapshot 捕获当前全部状态。
func (s *Splitter) Snapshot() State {
	return State{bytes.Clone(s.buf), s.crPending, s.bomSeen, bytes.Clone(s.bomPrefix)}
}

// Restore 恢复检查点。
func (s *Splitter) Restore(st State) {
	s.buf, s.crPending, s.bomSeen, s.bomPrefix = bytes.Clone(st.Buf), st.CRPending, st.BOMSeen, bytes.Clone(st.BOMPrefix)
}

// Reset 清空切行状态（BOM 视为重新出现于流首），不改变行长上限。
func (s *Splitter) Reset() {
	s.buf, s.crPending, s.bomSeen, s.bomPrefix = nil, false, false, nil
}
