// Package undo 按撤销日志逆序撤销批量重命名，支持截断日志的分类恢复。
package undo

import (
	"bytes"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io/fs"
	"os"
	"slices"
	"strconv"

	"ontology/apply"
	"ontology/name"
)

// 三类日志截断/损坏的哨兵错误，可用 errors.Is 区分。
var (
	ErrHeaderIncomplete = errors.New("undo: journal header incomplete")
	ErrRecordIncomplete = errors.New("undo: journal record incomplete")
	ErrCRCMismatch      = errors.New("undo: journal record CRC mismatch")
)

// Report 描述一次撤销的结果。
type Report struct {
	Undone        int   // 实际撤销的步数
	Unrecoverable []int // 无法恢复（丢失）的步骤下标
}

// Undo 读取 journal 并按最大可恢复前缀逆序撤销。日志损坏时返回可分类
// 的错误，同时仍撤销已恢复的前缀；丢失的尾部记录与状态不符而无法撤销
// 的记录都在 Report.Unrecoverable 中报告下标。
// 幂等：成功撤销后删除日志，重复调用为无操作。
func Undo(ns *name.Namespace, journal string) (Report, error) {
	data, err := os.ReadFile(journal)
	if errors.Is(err, fs.ErrNotExist) {
		return Report{}, nil
	}
	if err != nil {
		return Report{}, err
	}
	recs, total, perr := Parse(data)
	rep := Report{}
	if err := ns.Transact(func(tx *name.Tx) error {
		for i := len(recs) - 1; i >= 0; i-- {
			r := recs[i]
			if !tx.Has(r.New) || tx.Has(r.Old) {
				rep.Unrecoverable = append(rep.Unrecoverable, i)
				continue // 状态与该记录不符，无法撤销
			}
			if err := tx.Rename(r.New, r.Old); err != nil {
				return err
			}
			rep.Undone++
		}
		return nil
	}); err != nil {
		return rep, err
	}
	for i := len(recs); i < total; i++ {
		rep.Unrecoverable = append(rep.Unrecoverable, i)
	}
	slices.Sort(rep.Unrecoverable)
	os.Remove(journal)
	return rep, perr
}

// Parse 解析日志内容，返回最大可恢复前缀的记录、头部声明的总步数
// 以及分类错误（完全合法时 err 为 nil）。
func Parse(data []byte) (recs []name.Rename, total int, err error) {
	nl := bytes.IndexByte(data, '\n')
	if nl < 0 {
		return nil, 0, ErrHeaderIncomplete
	}
	var head struct {
		V int `json:"v"`
		N int `json:"n"`
	}
	if json.Unmarshal(data[:nl], &head) != nil || head.V != 1 {
		return nil, 0, ErrHeaderIncomplete
	}
	body := data[nl+1:]
	for len(body) > 0 {
		i := bytes.IndexByte(body, '\n')
		if i < 0 {
			return recs, head.N, diagnose(body) // 末尾不完整行
		}
		r, rerr := parseRecord(body[:i])
		if rerr != nil {
			return recs, head.N, rerr
		}
		recs = append(recs, r)
		body = body[i+1:]
	}
	if len(recs) < head.N {
		return recs, head.N, ErrRecordIncomplete // 记录数不足头部声明
	}
	return recs, head.N, nil
}

// diagnose 对未以换行结尾的截断行分类：JSON 可解析但 CRC 不符归
// ErrCRCMismatch，其余（含记录未终结）归 ErrRecordIncomplete。
func diagnose(partial []byte) error {
	if _, err := parseRecord(partial); err != nil {
		return err
	}
	return ErrRecordIncomplete
}

// parseRecord 解析一条完整记录行：JSON + 空格 + 8 位十六进制 CRC32。
func parseRecord(line []byte) (name.Rename, error) {
	i := bytes.LastIndexByte(line, ' ')
	if i < 0 {
		return name.Rename{}, ErrRecordIncomplete
	}
	var rec apply.Record
	if json.Unmarshal(line[:i], &rec) != nil {
		return name.Rename{}, ErrRecordIncomplete
	}
	crc, err := strconv.ParseUint(string(line[i+1:]), 16, 32)
	if err != nil {
		return name.Rename{}, ErrRecordIncomplete
	}
	if uint32(crc) != crc32.ChecksumIEEE(line[:i]) {
		return name.Rename{}, ErrCRCMismatch
	}
	return name.Rename{Old: rec.Old, New: rec.New}, nil
}
