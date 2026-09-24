// Package api 对外提供跨重启无空洞的序列号分配器。
package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ontology/check"
	"ontology/seq"
)

// 三类可判定哨兵错误，互不相同。
var (
	// ErrInvalidDir 配置非法（New 的 dir 为空或不可用）。
	ErrInvalidDir = errors.New("api: invalid dir")
	// ErrPersist 持久化失败（重导出自 check 包）。
	ErrPersist = check.ErrPersist
	// ErrCorrupt checkpoint 损坏（重导出自 check 包）。
	ErrCorrupt = check.ErrCorrupt
)

// Allocator 是对外句柄，方法委托给 seq.Allocator。
type Allocator struct {
	inner *seq.Allocator
}

// New 创建分配器，next 初始为 0；dir 为空报 ErrInvalidDir。
func New(dir string) (*Allocator, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidDir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDir, err)
	}
	st, err := check.Open(dir)
	if err != nil {
		return nil, err
	}
	return &Allocator{inner: seq.NewAllocator(st)}, nil
}

// Next 先持久化后发放；持久化失败报 ErrPersist 且不留痕。
func (a *Allocator) Next() (int64, error) { return a.inner.Next() }

// Recover 读 checkpoint 重建 next；损坏报 ErrCorrupt 且状态不变。
func (a *Allocator) Recover() error { return a.inner.Recover() }

// SimulateCrash 丢弃内存状态（仅测试用）。
func (a *Allocator) SimulateCrash() { a.inner.SimulateCrash() }

// SelfCheck 用内置操作序列核验四条不变量，全部通过返回 nil。
func (a *Allocator) SelfCheck() error {
	dir, err := os.MkdirTemp("", "selfcheck")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	fresh, err := New(dir)
	if err != nil {
		return err
	}
	// 不变量 1/2/3：八步序列，崩溃恢复后不重复、不空洞、与位点一致。
	for want := int64(0); want < 4; want++ {
		got, err := fresh.Next()
		if err != nil || got != want {
			return fmt.Errorf("selfcheck: step %d got %d err %v", want, got, err)
		}
	}
	fresh.SimulateCrash()
	if err := fresh.Recover(); err != nil {
		return fmt.Errorf("selfcheck: recover: %v", err)
	}
	for want := int64(4); want < 6; want++ {
		got, err := fresh.Next()
		if err != nil || got != want {
			return fmt.Errorf("selfcheck: post-recover got %d want %d err %v", got, want, err)
		}
	}
	// 不变量 4：坏 checkpoint 必须报 ErrCorrupt，且状态不变、仍可续用。
	if err := os.WriteFile(filepath.Join(dir, "checkpoint"), []byte("bad"), 0o644); err != nil {
		return err
	}
	if err := fresh.Recover(); !errors.Is(err, ErrCorrupt) {
		return fmt.Errorf("selfcheck: corrupt not detected: %v", err)
	}
	if got, err := fresh.Next(); err != nil || got != 6 {
		return fmt.Errorf("selfcheck: state changed after failed recover: got %d err %v", got, err)
	}
	return nil
}
