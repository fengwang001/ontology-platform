// Package undo 按撤销日志逆序恢复命名空间，支持截断分类与幂等撤销。
package undo

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"os"

	"ontology/apply"
	"ontology/name"
)

var (
	// ErrShortHeader 头部 13 字节不完整或魔数/步数无法解析。
	ErrShortHeader = errors.New("undo: incomplete header")
	// ErrBadRecord 某条记录帧不完整或 JSON 损坏。
	ErrBadRecord = errors.New("undo: incomplete record")
	// ErrCRC 帧完整但尾部 CRC/魔数缺失或不匹配。
	ErrCRC = errors.New("undo: crc mismatch or missing trailer")
)

// Record 是一条撤销记录。
type Record struct {
	From string `json:"from"`
	To   string `json:"to"`
	Ord  int    `json:"ord"`
}

// Result 报告撤销结果。
type Result struct {
	// Recovered 为按最大可恢复前缀成功撤销的步数。
	Recovered int
	// Total 为头部声明的总步数。
	Total int
	// Missing 为无法撤销的步骤序号（1 起）。
	Missing []int
	// Full 在全部步骤可撤销时为真。
	Full bool
}

// Parse 解析日志字节，返回最大可恢复前缀的记录与分类错误。
// 记录完整但尾部/CRC 不符时返回全部记录与 ErrCRC。
func Parse(data []byte) ([]Record, int, error) {
	if len(data) < apply.HeaderLen || string(data[0:5]) != apply.HeaderMagic {
		return nil, 0, ErrShortHeader
	}
	total := int(binary.BigEndian.Uint32(data[5:9]))
	var recs []Record
	pos := apply.HeaderLen
	for i := 0; i < total; i++ {
		if pos+3 > len(data) || data[pos] != 0x01 {
			return recs, total, ErrBadRecord
		}
		n := int(binary.BigEndian.Uint16(data[pos+1 : pos+3]))
		if pos+3+n > len(data) {
			return recs, total, ErrBadRecord
		}
		var r Record
		if err := json.Unmarshal(data[pos+3:pos+3+n], &r); err != nil {
			return recs, total, ErrBadRecord
		}
		recs = append(recs, r)
		pos += 3 + n
	}
	if len(data) < pos+apply.TrailerLen {
		return recs, total, ErrCRC
	}
	sum := binary.BigEndian.Uint32(data[pos : pos+4])
	if string(data[pos+4:pos+10]) != apply.TrailerMagic ||
		sum != crc32.ChecksumIEEE(append(data[0:9], data[apply.HeaderLen:pos]...)) {
		return recs, total, ErrCRC
	}
	return recs, total, nil
}

// UndoBytes 对日志字节执行最大可恢复前缀撤销。
func UndoBytes(ns *name.Namespace, data []byte) (Result, error) {
	recs, total, perr := Parse(data)
	ns.Lock()
	defer ns.Unlock()
	for i := len(recs) - 1; i >= 0; i-- {
		if err := ns.RenameLocked(recs[i].To, recs[i].From, true); err != nil {
			perr = err
			break
		}
	}
	rec := len(recs)
	missing := []int{}
	for ord := rec + 1; ord <= total; ord++ {
		missing = append(missing, ord)
	}
	return Result{Recovered: rec, Total: total, Missing: missing,
		Full: rec == total}, perr
}

// UndoFile 读取日志文件并撤销；成功后把日志改名为 .done，
// 再次对同一日志（含 .done）撤销为幂等无操作。
func UndoFile(ns *name.Namespace, path string) (Result, error) {
	donePath := path + ".done"
	if _, err := os.Stat(donePath); err == nil {
		return Result{Full: true}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	res, perr := UndoBytes(ns, data)
	if perr == nil {
		if err := os.Rename(path, donePath); err != nil {
			return res, err
		}
	}
	return res, perr
}
