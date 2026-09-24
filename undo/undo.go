// Package undo 解析撤销日志，分类截断损坏，并按最大可恢复前缀逆序撤销。
package undo

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"

	"ontology/name"
)

// 三类截断错误，全部可用 errors.Is 区分。
var (
	ErrHeaderIncomplete = errors.New("undo: header incomplete")
	ErrRecordIncomplete = errors.New("undo: record incomplete")
	ErrCRC              = errors.New("undo: crc mismatch")
)

var magic = []byte("RNLOG001")

type rec struct {
	old, new string
	flag     byte
}

// Report 描述一次撤销的结果。
type Report struct {
	Undone      int // 实际撤销（翻转 flag）的步骤数
	Noop        int // 已是撤销态、跳过的记录数
	Lost        int // 因截断无法恢复的步骤数
	Kind        error
	Recoverable bool
}

// Parse 从数据起始解析记录，返回完整记录与解析错误。
func parse(data []byte) ([]rec, error) {
	if len(data) < len(magic) {
		return nil, ErrHeaderIncomplete
	}
	p := len(magic)
	var recs []rec
	for p < len(data) {
		start := p
		if len(data)-p < 2+0+2+0+1+4 { // 连最短的一条记录都放不下
			return recs, ErrRecordIncomplete
		}
		if p+2 > len(data) {
			return recs, ErrRecordIncomplete
		}
		oldLen := int(binary.BigEndian.Uint16(data[p : p+2]))
		p += 2
		if p+oldLen+2 > len(data) {
			return recs, ErrRecordIncomplete
		}
		oldName := string(data[p : p+oldLen])
		p += oldLen
		newLen := int(binary.BigEndian.Uint16(data[p : p+2]))
		p += 2
		if p+newLen+1+4 > len(data) {
			return recs, ErrRecordIncomplete
		}
		newName := string(data[p : p+newLen])
		p += newLen
		flag := data[p]
		p++
		gotCRC := binary.BigEndian.Uint32(data[p : p+4])
		p += 4
		want := crc32.ChecksumIEEE(data[start : p-4])
		if gotCRC != want {
			return recs, ErrCRC
		}
		recs = append(recs, rec{oldName, newName, flag})
	}
	return recs, nil
}

// Classify 对截断后的整份文件数据做分类（无损坏返回 nil）。
func Classify(data []byte) error {
	_, err := parse(data)
	return err
}

// FromFile 按日志逆序撤销命名空间改动，并把恢复后的日志重写回文件。
// 截断时只处理最大可恢复前缀，Lost 报告无法撤销的步骤（按完整日志应有记录数对照）。
func FromFile(ns *name.Namespace, path string, expectSteps int) (Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}
	recs, kind := parse(data)
	rep := Report{Kind: kind, Recoverable: true}

	if !ns.TryLock() {
		return Report{}, name.ErrLocked
	}
	defer ns.Unlock()
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if r.flag == 1 {
			rep.Noop++
			continue
		}
		if err := ns.UndoMoveLocked(r.old, r.new); err != nil {
			rep.Recoverable = false
			return rep, err
		}
		rep.Undone++
	}
	if expectSteps > len(recs) {
		rep.Lost = expectSteps - len(recs)
	}
	out := append([]byte(nil), magic...)
	for _, r := range recs {
		out = append(out, encode(r.old, r.new, 1)...)
	}
	if werr := os.WriteFile(path, out, 0o600); werr != nil {
		return rep, werr
	}
	return rep, kind
}

func encode(oldName, newName string, flag byte) []byte {
	buf := make([]byte, 0, 2+len(oldName)+2+len(newName)+1+4)
	l := make([]byte, 2)
	binary.BigEndian.PutUint16(l, uint16(len(oldName)))
	buf = append(buf, l...)
	buf = append(buf, oldName...)
	binary.BigEndian.PutUint16(l, uint16(len(newName)))
	buf = append(buf, l...)
	buf = append(buf, newName...)
	buf = append(buf, flag)
	c := make([]byte, 4)
	binary.BigEndian.PutUint32(c, crc32.ChecksumIEEE(buf))
	return append(buf, c...)
}
