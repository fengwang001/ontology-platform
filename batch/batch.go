// Package batch 定义导入批次及批次清单的流式编解码。
//
// 清单为长度前缀二进制格式：

//	[u32 BE 批次ID字节数][批次ID][u32 记录数]
//	每条记录：[u32 键字节数][键][u32 值字节数][值]
package batch

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	// ErrEmptyID 批次 ID 为空。
	ErrEmptyID = errors.New("batch: empty batch id")
)

// Record 一条待导入记录，Key 为业务键（空串合法）。
type Record struct {
	Key   string
	Value []byte
}

// Batch 批次：ID + 有序记录。
type Batch struct {
	ID      string
	Records []Record
}

// DupKeyError 批内业务键重复，指出重复键与两处下标。
type DupKeyError struct {
	Key string
	I   int
	J   int
}

func (e *DupKeyError) Error() string {
	return fmt.Sprintf("batch: duplicate key %q at positions %d and %d", e.Key, e.I, e.J)
}

// Validate 校验批次 ID 非空且批内无重复业务键。
func (b *Batch) Validate() error {
	if b.ID == "" {
		return ErrEmptyID
	}
	seen := make(map[string]int, len(b.Records))
	for i, r := range b.Records {
		if j, ok := seen[r.Key]; ok {
			return &DupKeyError{Key: r.Key, I: j, J: i}
		}
		seen[r.Key] = i
	}
	return nil
}

func writeStr(w io.Writer, s string) error {
	var ln [4]byte
	binary.BigEndian.PutUint32(ln[:], uint32(len(s)))
	if _, err := w.Write(ln[:]); err != nil {
		return err
	}
	_, err := io.WriteString(w, s)
	return err
}

func writeBytes(w io.Writer, b []byte) error {
	var ln [4]byte
	binary.BigEndian.PutUint32(ln[:], uint32(len(b)))
	if _, err := w.Write(ln[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

func readStr(r io.Reader) (string, error) {
	b, err := readBytes(r)
	return string(b), err
}

func readBytes(r io.Reader) ([]byte, error) {
	var ln [4]byte
	if _, err := io.ReadFull(r, ln[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(ln[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// Encode 将清单编码到 w（调用前应已 Validate）。
func (b *Batch) Encode(w io.Writer) error {
	bw := bufio.NewWriter(w)
	if err := writeStr(bw, b.ID); err != nil {
		return err
	}
	var ln [4]byte
	binary.BigEndian.PutUint32(ln[:], uint32(len(b.Records)))
	if _, err := bw.Write(ln[:]); err != nil {
		return err
	}
	for _, r := range b.Records {
		if err := writeStr(bw, r.Key); err != nil {
			return err
		}
		if err := writeBytes(bw, r.Value); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// Decode 从 r 完整解码清单。
func Decode(r io.Reader) (*Batch, error) {
	br := bufio.NewReader(r)
	id, err := readStr(br)
	if err != nil {
		return nil, err
	}
	var ln [4]byte
	if _, err := io.ReadFull(br, ln[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(ln[:])
	b := &Batch{ID: id, Records: make([]Record, 0, n)}
	for i := uint32(0); i < n; i++ {
		key, err := readStr(br)
		if err != nil {
			return nil, err
		}
		val, err := readBytes(br)
		if err != nil {
			return nil, err
		}
		b.Records = append(b.Records, Record{Key: key, Value: val})
	}
	return b, nil
}

// Marshal / Unmarshal 为便捷包装。
func (b *Batch) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	if err := b.Encode(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func Unmarshal(data []byte) (*Batch, error) {
	return Decode(bytes.NewReader(data))
}
