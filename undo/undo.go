// Package undo 解析撤销日志并按逆序撤销已执行的重命名。
//
// 截断的日志按最大可恢复前缀撤销，并报告无法撤销的步数；
// 三类截断错误可用 errors.Is 区分。重复撤销是幂等无操作。
package undo

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"strings"

	"ontology/apply"
	"ontology/name"
	"ontology/plan"
)

// 三类日志截断错误，可用 errors.Is 区分。
var (
	ErrBadHeader   = errors.New("undo: 日志头部不完整")
	ErrTruncated   = errors.New("undo: 日志记录不完整")
	ErrCRCMismatch = errors.New("undo: 日志 CRC 不匹配")
)

// Record 是一条撤销记录。
type Record struct {
	Old  string
	New  string
	Temp bool
}

// Result 汇总撤销结果。
type Result struct {
	Undone    int // 实际撤销的步数
	Recovered int // 从日志恢复的记录数
	Remaining int // 无法撤销的步数（截断丢失 + 被占用阻塞）
}

// Parse 解析日志，返回最大可恢复前缀的记录、声明的总步数与分类错误。
// 日志完整时错误为 nil。
func Parse(data []byte) ([]Record, int, error) {
	if len(data) < apply.HeaderLen || string(data[:8]) != apply.HeaderMagic {
		return nil, 0, ErrBadHeader
	}
	total := int(binary.BigEndian.Uint32(data[8:12]))
	off := apply.HeaderLen
	var recs []Record
	for {
		rem := data[off:]
		if len(rem) == 0 {
			return recs, total, ErrTruncated // 记录完整但缺尾部
		}
		if len(rem) >= 8 && string(rem[:8]) == apply.FooterMagic {
			if len(rem) < apply.FooterLen {
				return recs, total, ErrCRCMismatch // 尾部被截
			}
			if crc32.ChecksumIEEE(data[:off+8]) != binary.BigEndian.Uint32(rem[8:12]) {
				return recs, total, ErrCRCMismatch
			}
			return recs, total, nil
		}
		if len(rem) < 9 {
			if strings.HasPrefix(apply.FooterMagic, string(rem)) {
				return recs, total, ErrCRCMismatch // 尾部 magic 被截
			}
			return recs, total, ErrTruncated // 帧头被截
		}
		reclen := int(binary.BigEndian.Uint16(rem[0:2]))
		if 6+reclen > len(rem) {
			return recs, total, ErrTruncated // 记录体被截
		}
		payload := rem[6 : 6+reclen]
		if crc32.ChecksumIEEE(payload) != binary.BigEndian.Uint32(rem[2:6]) {
			return recs, total, ErrCRCMismatch
		}
		oldLen, newLen := int(payload[1]), int(payload[2])
		if reclen != 3+oldLen+newLen {
			return recs, total, ErrCRCMismatch
		}
		recs = append(recs, Record{
			Old:  string(payload[3 : 3+oldLen]),
			New:  string(payload[3+oldLen:]),
			Temp: payload[0] == 1,
		})
		off += 6 + reclen
	}
}

// Undo 读取日志并按逆序撤销。截断日志按最大可恢复前缀撤销：
// 逆序处理时某步仍被后续步骤占用则跳过该步（更早的步骤无法为其
// 腾出名字，见 DESIGN.md），并计入 Remaining。
// 返回的分类错误可用 errors.Is 判定。
//
// 幂等：环形批次撤销前后集合相同，基于状态无法区分"未撤销"，
// 因此 Undo 采用日志消费语义——撤销完成后把日志重写为剩余未撤销
// 的记录（通常为空），重复撤销即无操作。
func Undo(ns *name.Namespace, path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	recs, total, perr := Parse(data)
	res := Result{Recovered: len(recs), Remaining: total - len(recs)}
	ns.Lock()
	defer ns.Unlock()
	var left []plan.Step
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if ns.Has(r.Old) && !ns.Has(r.New) {
			continue // 已撤销过，幂等跳过
		}
		if ns.Rename(r.New, r.Old) {
			res.Undone++
		} else {
			res.Remaining++
			left = append(left, plan.Step{Old: r.Old, New: r.New, Temp: r.Temp})
		}
	}
	for i, j := 0, len(left)-1; i < j; i, j = i+1, j-1 {
		left[i], left[j] = left[j], left[i] // 恢复为执行顺序
	}
	if werr := os.WriteFile(path, apply.EncodeLog(left), 0o644); werr != nil && perr == nil {
		perr = werr
	}
	return res, perr
}
