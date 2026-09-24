// Package linescan 把任意切分的字节块增量切分为逻辑行。
// 支持 \n、\r\n、\r 三种行尾，流首 UTF-8 BOM 被吃掉。
package linescan

import "errors"

// ErrLineTooLong 表示单行字节数超过配置上限，返回时内部状态不变。
var ErrLineTooLong = errors.New("linescan: line exceeds max bytes")

var bom = []byte{0xEF, 0xBB, 0xBF}

// Scanner 是增量行切分器，跨块保留行缓冲与行尾状态。
type Scanner struct {
	max     int    // 单行最大字节，<=0 表示不限
	buf     []byte // 当前未完成的行
	pending []byte // 流首尚未判定是否 BOM 的字节
	afterCR bool   // 上一字节是 \r，等待可能的 \n
	bomDone bool   // 流首 BOM 判定已完成
}

// New 创建切分器，maxLine 为单行最大字节数（<=0 不限）。
func New(maxLine int) *Scanner { return &Scanner{max: maxLine} }

// Feed 处理一个字节块，返回本次产出的完整行。
// 返回错误时切分器状态保持不变。
func (s *Scanner) Feed(chunk []byte) ([]string, error) {
	snap := *s
	snap.buf = append([]byte(nil), s.buf...)
	snap.pending = append([]byte(nil), s.pending...)
	var lines []string
	for _, b := range chunk {
		if !s.bomDone {
			if err := s.stepBOM(b, &lines); err != nil {
				*s = snap
				return nil, err
			}
			continue
		}
		if err := s.step(b, &lines); err != nil {
			*s = snap
			return nil, err
		}
	}
	return lines, nil
}

// End 返回流末尾未以行尾结束的半行（没有则返回空）。
func (s *Scanner) End() []string {
	s.bomDone = true
	if len(s.pending) == 0 && len(s.buf) == 0 {
		return nil
	}
	line := string(append(s.pending, s.buf...))
	s.pending, s.buf = nil, nil
	return []string{line}
}

func (s *Scanner) stepBOM(b byte, lines *[]string) error {
	s.pending = append(s.pending, b)
	if len(s.pending) < len(bom) && s.pending[len(s.pending)-1] == bom[len(s.pending)-1] {
		return nil // 仍是 BOM 前缀，继续等待
	}
	saved := s.pending
	s.pending = nil
	s.bomDone = true
	if len(saved) == len(bom) {
		return nil // 完整匹配 BOM，丢弃
	}
	for _, pb := range saved { // 不是 BOM，按普通字节重放
		if err := s.step(pb, lines); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scanner) step(b byte, lines *[]string) error {
	if s.afterCR {
		s.afterCR = false
		if b == '\n' {
			return nil // \r\n 的右半边，吞掉
		}
	}
	switch b {
	case '\r':
		*lines = append(*lines, string(s.buf))
		s.buf = s.buf[:0]
		s.afterCR = true
	case '\n':
		*lines = append(*lines, string(s.buf))
		s.buf = s.buf[:0]
	default:
		if s.max > 0 && len(s.buf)+1 > s.max {
			return ErrLineTooLong
		}
		s.buf = append(s.buf, b)
	}
	return nil
}
