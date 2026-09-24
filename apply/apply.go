// Package apply 执行批量重命名并写撤销日志；中途失败自动回滚。
package apply

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/name"
	"ontology/plan"
)

// Record 是一条撤销日志记录（JSON 部分）。
type Record struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// HeaderLine 返回日志头部行（含换行）：版本 v 与总步数 n。
func HeaderLine(n int) []byte {
	b, _ := json.Marshal(struct {
		V int `json:"v"`
		N int `json:"n"`
	}{V: 1, N: n})
	return append(b, '\n')
}

// RecordLine 返回一条记录行（含换行）：JSON + 空格 + 对 JSON 字节的 CRC32。
func RecordLine(r name.Rename) []byte {
	b, _ := json.Marshal(Record{Old: r.Old, New: r.New})
	return []byte(fmt.Sprintf("%s %08x\n", b, crc32.ChecksumIEEE(b)))
}

// Options 控制执行过程（故障注入与并发测试用）。
type Options struct {
	FailAt int            // 在该下标的步骤前注入失败；负数表示不注入
	Hook   func(step int) // 每步执行前调用（持锁期间）
}

// Execute 检测冲突、编排顺序，并在命名空间整把锁内逐步执行，每步向
// journal 追加一条记录。任何一步失败都在锁内逆序回滚已执行步骤并删除
// 日志，使命名空间逐元素复原。冲突在修改前检测，命中则整批拒绝。
func Execute(ns *name.Namespace, reqs []name.Rename, journal string, opts *Options) (*plan.Plan, error) {
	p, err := plan.Build(ns, reqs)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &Options{FailAt: -1}
	}
	f, err := os.Create(journal)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(HeaderLine(len(p.Steps))); err != nil {
		return nil, err
	}
	done := 0
	execErr := ns.Transact(func(tx *name.Tx) error {
		for i, s := range p.Steps {
			if opts.FailAt == i {
				rollback(tx, p.Steps, done)
				return fmt.Errorf("apply: injected failure at step %d", i)
			}
			if opts.Hook != nil {
				opts.Hook(i)
			}
			if err := tx.Rename(s.Old, s.New); err != nil {
				rollback(tx, p.Steps, done)
				return err
			}
			if _, err := f.Write(RecordLine(s)); err != nil {
				rollback(tx, p.Steps, done)
				return err
			}
			done++
		}
		return nil
	})
	if execErr != nil {
		os.Remove(journal)
		return p, execErr
	}
	return p, nil
}

// rollback 在持锁状态下把已执行的 done 步逆序改回。
func rollback(tx *name.Tx, steps []name.Rename, done int) {
	for i := done - 1; i >= 0; i-- {
		_ = tx.Rename(steps[i].New, steps[i].Old)
	}
}
