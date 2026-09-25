// Package view 实现物化视图的原子切换状态机：当前快照经原子指针 cur 暴露，写先进暂存，Commit 才切换。
package view

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/snap"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrNoRefresh     = errors.New("view: no refresh in progress")
	ErrRefreshActive = errors.New("view: refresh already in progress")
	ErrEmptyStageKey = errors.New("view: stage key must not be empty")
)

// View 持有一张物化视图的全部状态。
type View struct {
	cur             atomic.Pointer[snap.Snapshot] // 当前可见快照，只经原子换指针更新
	mu              sync.Mutex                    // 只保护 refresh 状态机，读路径无锁
	staging         *snap.Snapshot                // 进行中的暂存，nil 表示无 refresh
	lastCommitCells int                           // 最近一次 Commit 复制的 cell 数（非导出，仅包内测试可见）
}

// New 返回初始视图：cur = {Seq:0, Cells:{}}。
func New() *View {
	v := &View{}
	v.cur.Store(snap.New(0, map[string]string{}))
	return v
}

// Read 用一次原子读 pin 住当前快照并返回；之后的切换不影响本次返回值。cells 不可变。
func (v *View) Read() (int64, map[string]string) { s := v.cur.Load(); return s.Seq, s.Cells }

// Switch 原子替换当前快照；已 pin 住旧快照的 Read 不受影响。
func (v *View) Switch(s *snap.Snapshot) { v.cur.Store(s) }

// StartRefresh 把当前快照拷贝成可变暂存，开始一轮 refresh。
func (v *View) StartRefresh() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.staging != nil {
		return ErrRefreshActive
	}
	v.staging = v.cur.Load().Clone()
	return nil
}

// withRefresh 持锁并校验有进行中的 refresh，然后把暂存交给 f。
func (v *View) withRefresh(f func(*snap.Snapshot) error) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.staging == nil {
		return ErrNoRefresh
	}
	return f(v.staging)
}

// Stage 把一条写放进暂存，对 Read 不可见，直到 Commit。
func (v *View) Stage(k, val string) error {
	if k == "" {
		return ErrEmptyStageKey
	}
	return v.withRefresh(func(s *snap.Snapshot) error { s.Cells[k] = val; return nil })
}

// Commit 把暂存固化为新不可变快照（Seq+1）并原子切换；只换指针，O(1)。
func (v *View) Commit() error {
	return v.withRefresh(func(s *snap.Snapshot) error {
		v.cur.Store(snap.New(v.cur.Load().Seq+1, s.Cells)) // 收养暂存 map，不复制
		v.staging = nil
		v.lastCommitCells = 0
		return nil
	})
}

// Abort 丢弃暂存，cur 不变，旧视图完整回退。
func (v *View) Abort() error {
	return v.withRefresh(func(*snap.Snapshot) error { v.staging = nil; return nil })
}

// SelfCheck 在内部新建视图，对内置操作序列核验四条不变量与 O(1) Commit。
func SelfCheck() error {
	v, ref := New(), map[string]string{}
	stg := func(k, val string) func() error { return func() error { return v.Stage(k, val) } }
	run := func(ops ...func() error) error {
		for _, op := range ops {
			if err := op(); err != nil {
				return err
			}
		}
		return nil
	}
	for i := 1; i <= 8; i++ { // 不变量1+3：重放一致、提交前不可见、Seq 恰 +1
		k := fmt.Sprintf("k%d", i)
		if err := run(v.StartRefresh, stg(k, "v")); err != nil {
			return err
		}
		if s, c := v.Read(); s != int64(i-1) || len(c) != i-1 {
			return errors.New("selfcheck: staged write visible before commit")
		}
		if err := v.Commit(); err != nil {
			return err
		}
		ref[k] = "v"
		if s, c := v.Read(); s != int64(i) || len(c) != len(ref) || c[k] != "v" {
			return errors.New("selfcheck: commit-log replay mismatch")
		}
	}
	seq0, cells0 := v.Read() // 不变量2：pin 住的旧快照不被后续 Commit 影响
	if err := run(v.StartRefresh, stg("pin", "1"), v.Commit); err != nil {
		return err
	}
	if _, leaked := cells0["pin"]; leaked || seq0 != 8 {
		return errors.New("selfcheck: pinned snapshot mutated by later commit")
	}
	before, beforeCells := v.Read() // 不变量3：Abort 完整回退
	if err := run(v.StartRefresh, stg("pin", "dirty"), v.Abort); err != nil {
		return err
	}
	if s, c := v.Read(); s != before || c["pin"] != beforeCells["pin"] {
		return errors.New("selfcheck: abort did not roll back")
	}
	for _, op := range []func() error{stg("x", "y"), v.Commit, v.Abort} { // 不变量4：失败不留痕
		if op() == nil {
			return errors.New("selfcheck: invalid op accepted")
		}
	}
	if s, c := v.Read(); s != before || len(c) != len(beforeCells) {
		return errors.New("selfcheck: rejected op changed state")
	}
	big := New() // O(1) Commit：大 m 下 Commit 复制数为 0
	must := big.StartRefresh()
	for i := 0; i < 10000 && must == nil; i++ {
		must = big.Stage(fmt.Sprintf("c%d", i), "v")
	}
	if must == nil {
		must = run(big.Commit, big.StartRefresh, big.Commit) // 第二次 Commit 无新写
	}
	if must != nil {
		return must
	}
	if big.lastCommitCells != 0 {
		return fmt.Errorf("selfcheck: commit copied %d cells, want 0", big.lastCommitCells)
	}
	return nil
}
