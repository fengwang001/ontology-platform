// Package batch 定义导入批次及其清单的编解码。
package batch

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Record 是批次中的一条记录，Key 为业务键，空串合法。
type Record struct {
	Key string
}

// Batch 是一个导入批次：唯一 ID 加有序记录清单。
type Batch struct {
	ID      string
	Records []Record
}

// DupKeyError 表示同一业务键在批次内出现两次。
type DupKeyError struct {
	Key     string
	First   int
	Second  int
}

func (e *DupKeyError) Error() string {
	return fmt.Sprintf("batch: duplicate key %q at positions %d and %d", e.Key, e.First, e.Second)
}

// ErrEmptyID 表示批次 ID 为空。
var ErrEmptyID = errors.New("batch: empty batch id")

// Validate 校验批次：空 ID 拒绝；批内重复键拒绝并给出两处位置。
func (b *Batch) Validate() error {
	if b.ID == "" {
		return ErrEmptyID
	}
	seen := make(map[string]int, len(b.Records))
	for i, r := range b.Records {
		if first, ok := seen[r.Key]; ok {
			return &DupKeyError{Key: r.Key, First: first, Second: i}
		}
		seen[r.Key] = i
	}
	return nil
}

// Len 返回记录数。
func (b *Batch) Len() int { return len(b.Records) }

// KeyAt 返回第 i 条记录的业务键。
func (b *Batch) KeyAt(i int) string { return b.Records[i].Key }

// Encode 将清单编码成行文本：首行批次 ID，其后每行业务键。
func (b *Batch) Encode(w io.Writer) error {
	bw := bufio.NewWriter(w)
	if _, err := fmt.Fprintln(bw, b.ID); err != nil {
		return err
	}
	for _, r := range b.Records {
		if _, err := fmt.Fprintln(bw, r.Key); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// Decode 从行文本还原批次。
func Decode(r io.Reader) (*Batch, error) {
	br := bufio.NewReader(r)
	id, err := br.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("batch: missing id: %w", err)
	}
	b := &Batch{ID: chomp(id)}
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			b.Records = append(b.Records, Record{Key: chomp(line)})
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}

func chomp(s string) string {
	s = strings.TrimRight(s, "\n")
	if len(s) > 0 && s[len(s)-1] == '\r' {
		s = s[:len(s)-1]
	}
	return s
}
