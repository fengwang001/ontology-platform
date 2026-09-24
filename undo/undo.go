// Package undo 读取撤销日志，按最大可恢复前缀逆序撤销。
package undo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/apply"
	"ontology/name"
	"ontology/plan"
)

// 三类日志截断/损坏错误，可用 errors.Is 区分。
var (
	ErrHeaderIncomplete = errors.New("undo: log header incomplete")
	ErrRecordIncomplete = errors.New("undo: log record incomplete")
	ErrCRCMismatch      = errors.New("undo: log CRC mismatch")
)

// Result 报告撤销结果：Undone 为成功撤销的步数，Lost 为因日志
// 截断而无法撤销的步骤下标。
type Result struct {
	Undone int
	Lost   []int
}

// Parse 解析日志字节，返回最大可恢复前缀的记录、声明的总步数
// 与分类错误（无错误时为 nil）。
func Parse(data []byte) ([]plan.Step, int, error) {
	if len(data) < apply.HeaderSize || string(data[:4]) != apply.Magic {
		return nil, 0, ErrHeaderIncomplete
	}
	count := int(binary.BigEndian.Uint32(data[8:]))
	bodyLen := int(binary.BigEndian.Uint32(data[12:]))
	if len(data) < apply.HeaderSize+bodyLen {
		recs := parseRecords(data[apply.HeaderSize:])
		return recs, count, ErrRecordIncomplete
	}
	body := data[apply.HeaderSize : apply.HeaderSize+bodyLen]
	recs := parseRecords(body)
	if consumed(recs) != len(body) {
		return recs, count, ErrRecordIncomplete
	}
	if len(data) != apply.HeaderSize+bodyLen+apply.FooterSize {
		return recs, count, ErrCRCMismatch
	}
	want := binary.BigEndian.Uint32(data[apply.HeaderSize+bodyLen:])
	if crc32.ChecksumIEEE(data[:apply.HeaderSize+bodyLen]) != want {
		return recs, count, ErrCRCMismatch
	}
	return recs, count, nil
}

// parseRecords 顺序解析完整记录，返回可恢复前缀。
func parseRecords(b []byte) []plan.Step {
	var out []plan.Step
	for len(b) >= 4 {
		ol := int(binary.BigEndian.Uint16(b[0:]))
		nl := int(binary.BigEndian.Uint16(b[2:]))
		if len(b) < 4+ol+nl {
			break
		}
		out = append(out, plan.Step{Old: string(b[4 : 4+ol]), New: string(b[4+ol : 4+ol+nl])})
		b = b[4+ol+nl:]
	}
	return out
}

func consumed(recs []plan.Step) int {
	n := 0
	for _, r := range recs {
		n += 4 + len(r.Old) + len(r.New)
	}
	return n
}

// Undo 按日志逆序撤销。截断日志按最大可恢复前缀撤销并报告
// 无法撤销的步骤；同一日志重复撤销是幂等无操作。
func Undo(s *name.Space, logPath string) (*Result, error) {
	if _, err := os.Stat(logPath + ".done"); err == nil {
		return &Result{}, nil
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil, err
	}
	recs, count, perr := Parse(data)
	if errors.Is(perr, ErrHeaderIncomplete) {
		return nil, perr
	}
	s.Lock()
	for i := len(recs) - 1; i >= 0; i-- {
		if err := s.RenameLocked(recs[i].New, recs[i].Old); err != nil {
			s.Unlock()
			return nil, fmt.Errorf("undo: step %d: %w", i, err)
		}
	}
	s.Unlock()
	if err := os.WriteFile(logPath+".done", []byte("done"), 0o600); err != nil {
		return nil, err
	}
	res := &Result{Undone: len(recs)}
	for i := len(recs); i < count; i++ {
		res.Lost = append(res.Lost, i)
	}
	return res, perr
}
