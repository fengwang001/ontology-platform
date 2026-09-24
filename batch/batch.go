// Package batch 定义批次清单（批次 ID + 有序业务键）及其编解码与校验。
package batch

import (
	"errors"
	"fmt"
	"strings"
)

// Manifest 是一个批次的清单：批次 ID 与按序排列的业务键。
type Manifest struct {
	ID   string
	Keys []string
}

// ErrEmptyID 表示批次 ID 为空串，非法。
var ErrEmptyID = errors.New("batch: empty batch ID")

// DupKeyError 表示同一批次内业务键重复，携带重复键与两处下标。
type DupKeyError struct {
	Key    string
	First  int
	Second int
}

func (e *DupKeyError) Error() string {
	return fmt.Sprintf("batch: duplicate key %q at positions %d and %d", e.Key, e.First, e.Second)
}

// Validate 校验清单：空批次 ID 拒绝；键重复拒绝整批；空串键合法。
func (m Manifest) Validate() error {
	if m.ID == "" {
		return ErrEmptyID
	}
	seen := make(map[string]int, len(m.Keys))
	for i, k := range m.Keys {
		if j, ok := seen[k]; ok {
			return &DupKeyError{Key: k, First: j, Second: i}
		}
		seen[k] = i
	}
	return nil
}

// Encode 把清单编码为文本：首行 "BATCH <id> <count>"，随后每行一个键。
// 空串键编码为空行，解码按 count 精确读取，不产生歧义。
func Encode(m Manifest) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "BATCH %s %d\n", m.ID, len(m.Keys))
	for _, k := range m.Keys {
		b.WriteString(k)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// Decode 解码 Encode 的产物。
func Decode(data []byte) (Manifest, error) {
	lines := strings.Split(string(data), "\n")
	var m Manifest
	var n int
	if _, err := fmt.Sscanf(lines[0], "BATCH %s %d", &m.ID, &n); err != nil {
		return m, fmt.Errorf("batch: bad header: %w", err)
	}
	if len(lines)-1 < n {
		return m, fmt.Errorf("batch: want %d key lines, got %d", n, len(lines)-1)
	}
	m.Keys = append(m.Keys, lines[1:1+n]...)
	return m, nil
}
