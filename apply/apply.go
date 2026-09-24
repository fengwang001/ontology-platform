// Package apply 在持锁的命名空间上执行重命名计划并写撤销日志。
//
// 整个批次（含冲突检测）在命名空间锁内执行，因此并发修改会被阻塞
// 到批次结束。任一步失败时，已执行的步骤按逆序立即回滚。
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

// 日志格式常量（全部大端），见 DESIGN.md。
const (
	HeaderMagic = "RNLOG001"
	FooterMagic = "RNEND001"
	HeaderLen   = 12 // magic(8) + 记录数 u32(4)
	FooterLen   = 12 // magic(8) + crc32(4)
)

// ErrInjected 是故障注入触发的可判定错误。
var ErrInjected = errors.New("apply: 注入的执行失败")

// Executor 执行批量重命名。
type Executor struct {
	NS      *name.Namespace
	LogPath string // 撤销日志路径，为空则不写日志
	// FailBefore 非零时，在执行第 FailBefore 步（从 1 计）之前注入失败。
	FailBefore int
	// OnStep 可选回调，在每步执行前调用（持锁状态），用于并发测试。
	OnStep func(i int)
}

// Run 构建计划并执行。任何失败都会回滚已执行步骤，使命名空间复原。
func (e *Executor) Run(reqs []plan.Request) (*plan.Plan, error) {
	e.NS.Lock()
	defer e.NS.Unlock()
	p, err := plan.Build(reqs, e.NS.Has)
	if err != nil {
		return nil, err
	}
	done := 0
	rollback := func() {
		for j := done - 1; j >= 0; j-- {
			s := p.Steps[j]
			e.NS.Rename(s.New, s.Old)
		}
	}
	for i, s := range p.Steps {
		if e.OnStep != nil {
			e.OnStep(i)
		}
		if e.FailBefore == i+1 {
			rollback()
			return p, fmt.Errorf("%w: 第 %d 步", ErrInjected, i+1)
		}
		if !e.NS.Rename(s.Old, s.New) {
			rollback()
			return p, fmt.Errorf("apply: 第 %d 步目标名 %q 已存在", i+1, s.New)
		}
		done++
	}
	if e.LogPath != "" {
		if err := os.WriteFile(e.LogPath, EncodeLog(p.Steps), 0o644); err != nil {
			rollback()
			return p, err
		}
	}
	return p, nil
}

// EncodeLog 把执行步骤序列化为撤销日志。
func EncodeLog(steps []plan.Step) []byte {
	size := HeaderLen + FooterLen
	for _, s := range steps {
		size += 9 + len(s.Old) + len(s.New)
	}
	buf := make([]byte, 0, size)
	buf = append(buf, HeaderMagic...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(steps)))
	for _, s := range steps {
		kind := byte(0)
		if s.Temp {
			kind = 1
		}
		payload := []byte{kind, byte(len(s.Old)), byte(len(s.New))}
		payload = append(payload, s.Old...)
		payload = append(payload, s.New...)
		buf = binary.BigEndian.AppendUint16(buf, uint16(len(payload)))
		buf = binary.BigEndian.AppendUint32(buf, crc32.ChecksumIEEE(payload))
		buf = append(buf, payload...)
	}
	buf = append(buf, FooterMagic...)
	buf = binary.BigEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
	return buf
}
