// Package importer 编排批量导入：Prepare → Write → Verify → Commit，
// 支持断点续传、幂等重放与基于进度目录的批次占用锁。
package importer

import (
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"ontology/batch"
	"ontology/progress"
	"ontology/store"
)

// ErrInProgress 表示同一批次正被另一导入器处理（可判定错误）。
var ErrInProgress = errors.New("importer: batch is being imported")

// Result 是 Import 的返回结果。
type Result struct {
	AlreadyDone bool // 本批次此前已 Commit
	Rewritten   int  // 本次调用中幂等重写的记录数
}

// Importer 导入编排器。计数器字段非导出，经方法读取。
type Importer struct {
	Store   *store.Store
	Dir     string
	N       int // 进度粒度：每 N 条刷一次进度区间
	Now     func() time.Time
	LockTTL time.Duration

	rewrites atomic.Int64 // 续传时幂等重写的记录总数
	peak     atomic.Int64 // 工作缓冲驻留记录数的历史峰值
}

// New 创建导入器；n 为进度粒度。
func New(st *store.Store, dir string, n int) *Importer {
	return &Importer{Store: st, Dir: dir, N: n, Now: time.Now, LockTTL: 30 * time.Second}
}

// Rewrites 返回累计幂等重写条数。
func (im *Importer) Rewrites() int { return int(im.rewrites.Load()) }

// Peak 返回工作缓冲驻留记录数的历史峰值。
func (im *Importer) Peak() int { return int(im.peak.Load()) }

func (im *Importer) lockPath(id string) string   { return filepath.Join(im.Dir, id+".lock") }
func (im *Importer) commitPath(id string) string { return filepath.Join(im.Dir, id+".commit") }
func (im *Importer) progressPath(id string) string {
	return filepath.Join(im.Dir, id+".progress")
}

// Committed 报告批次是否已有提交标记。
func (im *Importer) Committed(id string) bool {
	_, err := os.Stat(im.commitPath(id))
	return err == nil
}

// acquire 占用批次锁；锁未过期返回 ErrInProgress，过期则安全接管。
func (im *Importer) acquire(id string) error {
	if err := os.MkdirAll(im.Dir, 0o755); err != nil {
		return err
	}
	path := im.lockPath(id)
	expiry := im.Now().Add(im.LockTTL).UnixNano()
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d", expiry)
			f.Close()
			return nil
		}
		if !os.IsExist(err) {
			return err
		}
		data, rerr := os.ReadFile(path)
		old, perr := strconv.ParseInt(string(data), 10, 64)
		if rerr == nil && perr == nil && im.Now().UnixNano() < old {
			return ErrInProgress
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
}

func (im *Importer) release(id string) { os.Remove(im.lockPath(id)) }

// Acquire 显式占用批次锁（模拟在途持有者）；配合 Release 使用。
func (im *Importer) Acquire(id string) error { return im.acquire(id) }

// Release 释放批次锁。
func (im *Importer) Release(id string) { im.release(id) }

// storePrefix 二分查找存储中本批次记录的实际前缀长度 M（不重头扫描）。
func (im *Importer) storePrefix(b *batch.Batch) int {
	lo, hi := 0, len(b.Records)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if im.Store.Has(b.Records[mid-1].Key) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// Import 执行四阶段导入；可安全重入续传。
func (im *Importer) Import(b *batch.Batch) (Result, error) {
	if err := b.Validate(); err != nil {
		return Result{}, err
	}
	if im.Committed(b.ID) {
		return Result{AlreadyDone: true}, nil
	}
	if err := im.acquire(b.ID); err != nil {
		return Result{}, err
	}
	defer im.release(b.ID)
	manifest := b.Encode()
	if err := os.WriteFile(filepath.Join(im.Dir, b.ID+".manifest"), manifest, 0o644); err != nil {
		return Result{}, err
	}
	pf, _, err := progress.Open(im.progressPath(b.ID), b.ID)
	if err != nil {
		return Result{}, err
	}
	n := len(b.Records)
	pos := min(pf.Contiguous(), im.storePrefix(b))
	var res Result
	for pos < n {
		end := min(pos+im.N, n)
		chunk := append([]batch.Record(nil), b.Records[pos:end]...)
		if v := int64(len(chunk)); v > im.peak.Load() {
			im.peak.Store(v)
		}
		for _, r := range chunk {
			existed, err := im.Store.Put(r.Key, r.Value)
			if err != nil {
				return Result{}, err
			}
			if existed {
				im.rewrites.Add(1)
				res.Rewritten++
			}
		}
		if err := pf.Flush(batch.Interval{Start: pos, End: end}); err != nil {
			return Result{}, err
		}
		pos = end
	}
	for _, r := range b.Records {
		if !im.Store.Has(r.Key) {
			return Result{}, fmt.Errorf("importer: verify failed, missing key %q", r.Key)
		}
	}
	commit := fmt.Sprintf("%d %08x", n, crc32.ChecksumIEEE(manifest))
	if err := os.WriteFile(im.commitPath(b.ID), []byte(commit), 0o644); err != nil {
		return Result{}, err
	}
	return res, nil
}
