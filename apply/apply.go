// Package apply 在持锁状态下执行重命名计划，失败自动回滚，并写撤销日志。
//
// 日志格式：16 字节头部（magic "RNLG" + u32 版本 + u32 记录数 + u32 头部
// CRC32），随后每条记录为 u32 旧名长 + u32 新名长 + 旧名 + 新名 + u32 记录
// CRC32。整数均为大端。
package apply

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/name"
	"ontology/plan"
)

// ErrStepFailed 表示某一步执行失败（含注入失败），已完成步骤已回滚。
var ErrStepFailed = errors.New("apply: step failed")

// 日志格式常量，undo 包据此解析。
const (
	Magic      = "RNLG"
	Version    = 1
	HeaderSize = 16
)

// Options 控制执行行为。
type Options struct {
	LogPath string               // 撤销日志路径，空表示不写日志
	FailAt  int                  // 在该步（从 1 计）执行前注入失败，0 表示不注入
	OnStep  func(done, total int) // 每步完成后回调（仍持锁），供测试观察
}

// Exec 持锁执行整个计划。任一步失败时，已执行步骤立即逆序回滚并删除日志。
func Exec(ns *name.NS, p *plan.Plan, opts Options) error {
	ns.Lock()
	defer ns.Unlock()
	if opts.LogPath != "" {
		if err := WriteLog(opts.LogPath, p.Steps); err != nil {
			return err
		}
	}
	rollback := func(done int) error {
		for i := done - 1; i >= 0; i-- {
			s := p.Steps[i]
			ns.RenameLocked(s.New, s.Old)
		}
		if opts.LogPath != "" {
			os.Remove(opts.LogPath)
		}
		return fmt.Errorf("%w: step %d (%q -> %q)", ErrStepFailed, done+1, p.Steps[done].Old, p.Steps[done].New)
	}
	for i, s := range p.Steps {
		if opts.FailAt == i+1 {
			return rollback(i)
		}
		if !ns.RenameLocked(s.Old, s.New) {
			return rollback(i)
		}
		if opts.OnStep != nil {
			opts.OnStep(i+1, len(p.Steps))
		}
	}
	return nil
}

// WriteLog 把步骤序列写成撤销日志。
func WriteLog(path string, steps []plan.Step) error {
	buf := make([]byte, 0, HeaderSize*(1+len(steps)))
	hdr := make([]byte, HeaderSize)
	copy(hdr, Magic)
	binary.BigEndian.PutUint32(hdr[4:], Version)
	binary.BigEndian.PutUint32(hdr[8:], uint32(len(steps)))
	binary.BigEndian.PutUint32(hdr[12:], crc32.ChecksumIEEE(hdr[:12]))
	buf = append(buf, hdr...)
	for _, s := range steps {
		rec := make([]byte, 8, 8+len(s.Old)+len(s.New)+4)
		binary.BigEndian.PutUint32(rec[0:], uint32(len(s.Old)))
		binary.BigEndian.PutUint32(rec[4:], uint32(len(s.New)))
		rec = append(rec, s.Old...)
		rec = append(rec, s.New...)
		var crc [4]byte
		binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(rec))
		buf = append(append(buf, rec...), crc[:]...)
	}
	return os.WriteFile(path, buf, 0o600)
}
