// Package importer 编排四阶段导入 Prepare → Write → Verify → Commit，
// 支持幂等重复导入、断点续传与续传重放计数。
package importer

import (
	"errors"
	"ontology/batch"
	"ontology/progress"
	"ontology/store"
)

// Config 是导入器配置。
type Config struct {
	Dir          string // 进度与清单的本地临时目录
	Granularity  int    // 进度刷盘粒度 N（重放条数上界）
	Window       int    // 同时驻留记录上限 M
	LockTTL      int64  // 占用锁有效期（纳秒）
}

// Importer 执行单个批次的一次导入尝试。
type Importer struct {
	cfg   Config
	st    *store.Store
	src   batch.Source
	bid   string
	lock  *progress.Lock
	pro   *progress.Progress
	start int
}

var (
	// ErrAlreadyComplete 表示批次此前已完成提交（幂等，非失败）。
	ErrAlreadyComplete = errors.New("batch already complete")
)

// New 创建导入器。
func New(cfg Config, st *store.Store, src batch.Source, batchID string) *Importer {
	return nil
}

// Rewritten 返回本次续传实际重新写入的记录数。
func (im *Importer) Rewritten() int { return 0 }

// PeakRecords 返回本进程历史同时驻留记录峰值。
func PeakRecords() int { return 0 }

// Prepare 落清单、取占用、读进度并以存储为准探测续传起点。
func (im *Importer) Prepare() error { return nil }

// Write 从续传起点写入全部记录，按粒度刷进度。
func (im *Importer) Write() error { return nil }

// Verify 复核存储连续前缀覆盖全部记录。
func (im *Importer) Verify() error { return nil }

// Commit 提交批次并将进度置为 committed；已完成返回 ErrAlreadyComplete。
func (im *Importer) Commit() error { return nil }

// Run 依次执行四阶段；已完成属幂等成功，返回 ErrAlreadyComplete。
func (im *Importer) Run() error { return nil }

// Release 释放占用锁。
func (im *Importer) Release() {}
