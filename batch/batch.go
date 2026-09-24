// Package batch 定义批次、记录与区间，并提供批次清单的确定性编解码。
package batch

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Record 是一条待导入记录，Key 为业务键（空串合法），Value 为载荷。
type Record struct {
	Key   string
	Value []byte
}

// Batch 是一个导入批次：唯一 ID 加有序记录列表。
type Batch struct {
	ID      string
	Records []Record
}

// Interval 表示记录下标的左闭右开区间 [Start, End)。
type Interval struct {
	Start int
	End   int
}

// ErrEmptyBatchID 表示批次 ID 为空串，整批拒绝。
var ErrEmptyBatchID = errors.New("batch: empty batch id")

// DupKeyError 表示同一批次内业务键重复，携带键与两处下标。
type DupKeyError struct {
	Key    string
	First  int
	Second int
}

func (e *DupKeyError) Error() string {
	return fmt.Sprintf("batch: duplicate key %q at positions %d and %d", e.Key, e.First, e.Second)
}

// Validate 校验批次：空 ID 拒绝；批内重复键拒绝并报告两处位置。
func (b *Batch) Validate() error {
	if b.ID == "" {
		return ErrEmptyBatchID
	}
	seen := make(map[string]int, len(b.Records))
	for i, r := range b.Records {
		if j, ok := seen[r.Key]; ok {
			return &DupKeyError{Key: r.Key, First: j, Second: i}
		}
		seen[r.Key] = i
	}
	return nil
}

// Encode 将批次清单编码为确定性字节序列（长度前缀，支持任意字节）。
func (b *Batch) Encode() []byte {
	var w bytes.Buffer
	w.WriteString("BATCH1\n")
	fmt.Fprintf(&w, "%d\n", len(b.ID))
	w.WriteString(b.ID)
	fmt.Fprintf(&w, "\n%d\n", len(b.Records))
	for _, r := range b.Records {
		fmt.Fprintf(&w, "%d %d\n", len(r.Key), len(r.Value))
		w.WriteString(r.Key)
		w.Write(r.Value)
	}
	return w.Bytes()
}

// Decode 解析 Encode 产出的清单字节，任一字段损坏即报错。
func Decode(data []byte) (*Batch, error) {
	r := bufio.NewReader(bytes.NewReader(data))
	magic, err := r.ReadString('\n')
	if err != nil || strings.TrimSuffix(magic, "\n") != "BATCH1" {
		return nil, errors.New("batch: bad manifest header")
	}
	idLen, err := readNum(r)
	if err != nil {
		return nil, fmt.Errorf("batch: bad id length: %w", err)
	}
	id := make([]byte, idLen)
	if _, err := io.ReadFull(r, id); err != nil {
		return nil, fmt.Errorf("batch: truncated id: %w", err)
	}
	if _, err := r.ReadString('\n'); err != nil {
		return nil, fmt.Errorf("batch: missing id terminator: %w", err)
	}
	count, err := readNum(r)
	if err != nil {
		return nil, fmt.Errorf("batch: bad record count: %w", err)
	}
	b := &Batch{ID: string(id), Records: make([]Record, 0, count)}
	for i := 0; i < count; i++ {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("batch: bad record %d header: %w", i, err)
		}
		var klen, vlen int
		if _, err := fmt.Sscanf(strings.TrimSuffix(line, "\n"), "%d %d", &klen, &vlen); err != nil {
			return nil, fmt.Errorf("batch: bad record %d lengths: %w", i, err)
		}
		buf := make([]byte, klen+vlen)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("batch: truncated record %d: %w", i, err)
		}
		b.Records = append(b.Records, Record{Key: string(buf[:klen]), Value: buf[klen:]})
	}
	if r.Buffered() > 0 {
		return nil, errors.New("batch: trailing garbage")
	}
	return b, nil
}

func readNum(r *bufio.Reader) (int, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSuffix(line, "\n"))
}
