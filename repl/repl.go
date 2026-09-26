// Package repl 实现单个副本：有序日志 + 确定性状态机按序复制。
// 依赖方向：repl -> sm，不允许反向依赖。
package repl

import (
	"errors"
	"math/rand"
	"sync"

	"ontology/sm"
)

// 三类失败各自对应一个互不相同的哨兵错误，供 errors.Is 判定。
var (
	ErrEmptyCommand     = errors.New("repl: empty or unrecognized command")
	ErrCommitOutOfRange = errors.New("repl: commit index out of range")
	ErrSnapOutOfRange   = errors.New("repl: snapshot index out of range")
)

// Replica 是一个副本。日志下标从 1 起（第 i 条在 log[i-1]）。
type Replica struct {
	mu          sync.Mutex
	log         []sm.Command
	committed   int // 已提交下标
	lastApplied int // 已应用下标
	state       int // 状态机当前状态
	// readCount 是最近一次 Apply 读取过的日志条目个数；非导出，永不进公开接口。
	readCount int
}

func New() *Replica { return &Replica{} }

// Append 把一条命令追加到日志末尾；空/未识别命令整体拒绝、不留痕。
func (r *Replica) Append(cmd sm.Command) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !cmd.Valid() {
		return ErrEmptyCommand
	}
	r.log = append(r.log, cmd)
	return nil
}

// Commit 置 committed=max(committed,i)；i 越界则整体拒绝、不留痕。
func (r *Replica) Commit(i int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i > len(r.log) {
		return ErrCommitOutOfRange
	}
	if i > r.committed {
		r.committed = i
	}
	return nil
}

// Apply 把 lastApplied+1..committed 的条目按下标升序依次应用；无增长则空操作。
// 每读取一条 readCount++，从 lastApplied 续读而非从下标 1 重扫。
func (r *Replica) Apply() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readCount = 0
	for i := r.lastApplied + 1; i <= r.committed; i++ {
		r.state = sm.Apply(r.log[i-1], r.state)
		r.readCount++
		r.lastApplied = i
	}
}

// Restart 从快照恢复：state=snapState、lastApplied=snapIndex；日志保留、committed 不变。
func (r *Replica) Restart(snapIndex, snapState int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if snapIndex < 0 || snapIndex > r.committed {
		return ErrSnapOutOfRange
	}
	r.state = snapState
	r.lastApplied = snapIndex
	return nil
}

// State / LastApplied / Committed 加锁读取，可被多 goroutine 并发调用。
func (r *Replica) State() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *Replica) LastApplied() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastApplied
}

func (r *Replica) Committed() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.committed
}

// randCmds 用确定性种子循环生成 n 条随机命令（供测试与自检使用）。
func randCmds(n int, seed int64) []sm.Command {
	rng := rand.New(rand.NewSource(seed))
	cs := make([]sm.Command, n)
	for i := range cs {
		if rng.Intn(2) == 0 {
			cs[i] = sm.Command{Op: sm.Add, K: rng.Intn(9) - 4}
		} else {
			cs[i] = sm.Command{Op: sm.Mul, K: rng.Intn(4) + 1}
		}
	}
	return cs
}

// fillAdds 追加 n 条 Add(1)。
func fillAdds(r *Replica, n int) {
	for i := 0; i < n; i++ {
		_ = r.Append(sm.Command{Op: sm.Add, K: 1})
	}
}
