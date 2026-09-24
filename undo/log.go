// Package undo 定义重命名执行日志的编码/解码、截断分类与逆序撤销。
package undo

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/name"
)

const header = "RNL01"

var (
	ErrLogHeader = errors.New("undo: log header incomplete or corrupted")
	ErrLogRecord = errors.New("undo: log record incomplete")
	ErrLogCRC    = errors.New("undo: log record crc mismatch")
)

// Step 是日志中的一条移动记录。
type Step struct{ From, To string }

// Log 表示一个执行日志文件。
type Log struct{ Path string }

// Create 在临时目录中新建日志并写入头部。
func Create(dir, base string) (*Log, error) {
	f, err := os.CreateTemp(dir, base+".*.log")
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(header); err != nil {
		f.Close()
		return nil, err
	}
	path := f.Name()
	f.Close()
	return &Log{Path: path}, nil
}

// Append 追加一条记录：[4 字节长度][JSON][4 字节 CRC32]。
func (l *Log) Append(s Step) error {
	payload, err := json.Marshal(struct {
		F string `json:"f"`
		T string `json:"t"`
	}{s.From, s.To})
	if err != nil {
		return err
	}
	var rec [8]byte
	binary.BigEndian.PutUint32(rec[:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(rec[4:], crc32.ChecksumIEEE(payload))
	f, err := os.OpenFile(l.Path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(rec[:4]); err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		return err
	}
	if _, err := f.Write(rec[4:]); err != nil {
		return err
	}
	return f.Sync()
}

// Exists 报告日志文件是否仍存在（完成撤销后会被改名）。
func (l *Log) Exists() bool {
	_, err := os.Stat(l.Path)
	return err == nil
}

// Result 是一次文件级撤销的结果。
type Result struct {
	Undone        int  // 成功撤销的步数
	Unrecoverable int  // 因截断无法撤销的步数（仅在提供总数时可知）
	NoOp          bool // 重复撤销（日志已不存在）
}

// Run 读取日志、加命名空间写锁、逆序撤销；成功后将日志改名 .done，
// 因此对同一日志再次 Run 为无操作（幂等）。
func (l *Log) Run(sp *name.Space) (Result, error) {
	data, err := os.ReadFile(l.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{NoOp: true}, nil
		}
		return Result{}, err
	}
	sp.Lock()
	done, derr := Restore(sp, data)
	sp.Unlock()
	if derr == nil {
		_ = os.Rename(l.Path, l.Path+".done")
	}
	return Result{Undone: done}, derr
}

// Decode 解析日志，返回最大可恢复前缀与遇到的错误。
func Decode(data []byte) ([]Step, error) {
	if len(data) < len(header) || string(data[:len(header)]) != header {
		return nil, ErrLogHeader
	}
	pos := len(header)
	var steps []Step
	for pos < len(data) {
		if len(data)-pos < 4 {
			return steps, fmt.Errorf("%w: length field truncated", ErrLogRecord)
		}
		n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if len(data)-pos < n {
			return steps, fmt.Errorf("%w: payload truncated", ErrLogRecord)
		}
		payload := data[pos : pos+n]
		pos += n
		if len(data)-pos < 4 {
			return steps, fmt.Errorf("%w: crc truncated", ErrLogCRC)
		}
		want := binary.BigEndian.Uint32(data[pos : pos+4])
		pos += 4
		if crc32.ChecksumIEEE(payload) != want {
			return steps, fmt.Errorf("%w: checksum mismatch", ErrLogCRC)
		}
		var e struct {
			F string `json:"f"`
			T string `json:"t"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return steps, fmt.Errorf("%w: %v", ErrLogRecord, err)
		}
		steps = append(steps, Step{e.F, e.T})
	}
	return steps, nil
}

// Restore 从日志字节逆序撤销。caller 必须已持有命名空间写锁。
// 返回成功撤销的步数；遇到截断错误时停止并返回该错误。
func Restore(sp *name.Space, data []byte) (int, error) {
	steps, err := Decode(data)
	done := 0
	for i := len(steps) - 1; i >= 0; i-- {
		if e := sp.MoveLocked(steps[i].To, steps[i].From); e != nil {
			return done, e
		}
		done++
	}
	return done, err
}
