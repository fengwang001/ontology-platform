// Package source 提供可注入的数据源：可配置产出速率、在指定偏移
// 报错、提前结束，并支持从给定偏移重放。
package source

import (
	"errors"
	"time"
)

// ErrFail 是脚本在 FailAt 偏移注入的哨兵错误。
var ErrFail = errors.New("source: injected failure")

// Script 描述一段确定性数据源。
type Script struct {
	Total  int           // 正常情况下产出的总条数
	Every  time.Duration // 每条产出间隔；0 表示全速
	FailAt int           // >=0 时，产出到该偏移前返回 ErrFail；-1 表示不报错
	EndAt  int           // >0 时，仅产出该条数即提前正常结束
}

// Source 按 Script 产出记录，记录偏移即其下标（从 0 开始）。
type Source struct {
	s      Script
	offset int
}

// New 从 from 偏移开始创建数据源（恢复时传入已消费位置）。
func New(s Script, from int) *Source {
	if s.FailAt == 0 {
		s.FailAt = -1
	}
	return &Source{s: s, offset: from}
}

// Emit 产出下一条记录。data 为原始字节，off 为其偏移；
// ok=false 表示数据源结束（正常或报错），err 非 nil 时为中途失败。
func (s *Source) Emit() (data []byte, off int, ok bool, err error) {
	if s.offset >= s.s.Total {
		return nil, s.offset, false, nil
	}
	if s.s.EndAt > 0 && s.offset >= s.s.EndAt {
		return nil, s.offset, false, nil
	}
	if s.s.FailAt >= 0 && s.offset >= s.s.FailAt {
		return nil, s.offset, false, ErrFail
	}
	if s.s.Every > 0 {
		time.Sleep(s.s.Every)
	}
	data = Encode(s.offset)
	off = s.offset
	s.offset++
	return data, off, true, nil
}

// Offset 返回下一条待产出的偏移。
func (s *Source) Offset() int { return s.offset }

// Encode 把偏移编码成一条原始记录： "<off>|k<off%%keyN>|v<off>"，
// keyN 个不同分组，保证记录确定且可解析。
func Encode(off int) []byte {
	const keyN = 16
	return []byte(itoa(off) + "|k" + itoa(off%keyN) + "|v" + itoa(off))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
