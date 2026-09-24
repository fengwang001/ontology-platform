// Package api 对外暴露撤回日志按水位回收器：包装 gcer，提供 Watermark 与 SelfCheck。
package api

import (
	"errors"
	"fmt"

	"ontology/gcer"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrOutOfRange       = gcer.ErrOutOfRange
	ErrReclaimed        = gcer.ErrReclaimed
	ErrNoSuchSnapshot   = gcer.ErrNoSuchSnapshot
	ErrTooManySnapshots = gcer.ErrTooManySnapshots
)

// Engine 是对外回收器，全部状态在进程内存。
type Engine struct{ l *gcer.Log }

// New 创建回收器，maxSnapshots 为活跃快照上限。
func New(maxSnapshots int) *Engine { return &Engine{l: gcer.New(maxSnapshots)} }

// Append 追加撤回记录，返回新 Seq（从 1 连续递增）。
func (e *Engine) Append(key, oldVal string) int64 { return e.l.Append(key, oldVal) }

// Open 打开读快照，水位 = 当前 Seq；超限返回 ErrTooManySnapshots。
func (e *Engine) Open() (int64, error) { return e.l.Open() }

// Close 关闭快照；不存在返回 ErrNoSuchSnapshot。
func (e *Engine) Close(id int64) error { return e.l.Close(id) }

// Replay 重放位点 seq；越界 ErrOutOfRange，已回收 ErrReclaimed。
func (e *Engine) Replay(id, seq int64) (key, oldVal string, err error) {
	return e.l.Replay(id, seq)
}

// GC 回收所有 Seq<=G 的记录，G=最小活跃水位（无活跃快照时全收）。
func (e *Engine) GC() { e.l.GC() }

// Watermark 返回当前回收上界 G。
func (e *Engine) Watermark() int64 { return e.l.Watermark() }

// SelfCheck 在独立内部实例上执行内置操作序列，核验四条不变量；全过返回 nil。
func (e *Engine) SelfCheck() error {
	l := gcer.New(4)
	for i := 0; i < 3; i++ {
		l.Append(fmt.Sprintf("k%d", i), "v")
	}
	s1, err := l.Open() // W=3
	if err != nil {
		return fmt.Errorf("selfcheck open: %w", err)
	}
	l.Append("k3", "v")
	l.Append("k4", "v") // Seq=5
	s2, _ := l.Open()   // W=5
	l.GC()              // G=min(3,5)=3
	if l.Watermark() != 3 {
		return errors.New("selfcheck: invariant2 G != min active watermark")
	}
	for seq := int64(4); seq <= 5; seq++ { // 不变量1：G<seq<=W 必可重放
		if _, _, err := l.Replay(s2, seq); err != nil {
			return fmt.Errorf("selfcheck: invariant1 lost seq %d: %w", seq, err)
		}
	}
	prev := l.Watermark()
	if err := l.Close(s1); err != nil {
		return fmt.Errorf("selfcheck close: %w", err)
	}
	l.GC() // 最小水位重估为 5
	if l.Watermark() < prev {
		return errors.New("selfcheck: invariant3 watermark regressed")
	}
	if l.Watermark() != 5 {
		return errors.New("selfcheck: invariant2 close did not re-estimate min")
	}
	if err := l.Close(s2); err != nil {
		return fmt.Errorf("selfcheck close: %w", err)
	}
	l.Append("k5", "v") // Seq=6
	l.GC()              // 无活跃快照全收
	if l.Watermark() != 6 {
		return errors.New("selfcheck: invariant2 no-snapshot full reclaim")
	}
	// 不变量4：四类拒绝互不相同且不留痕，拒绝后仍可正常使用
	before := l.Watermark()
	s3, _ := l.Open() // W=6
	if _, _, err := l.Replay(s3, 7); !errors.Is(err, ErrOutOfRange) {
		return errors.New("selfcheck: invariant4 out-of-range not rejected")
	}
	if _, _, err := l.Replay(s3, 1); !errors.Is(err, ErrReclaimed) {
		return errors.New("selfcheck: invariant4 reclaimed not rejected")
	}
	if err := l.Close(1 << 40); !errors.Is(err, ErrNoSuchSnapshot) {
		return errors.New("selfcheck: invariant4 close-unknown not rejected")
	}
	l2 := gcer.New(1)
	if _, err := l2.Open(); err != nil {
		return fmt.Errorf("selfcheck open: %w", err)
	}
	if _, err := l2.Open(); !errors.Is(err, ErrTooManySnapshots) {
		return errors.New("selfcheck: invariant4 overflow not rejected")
	}
	if l.Watermark() != before {
		return errors.New("selfcheck: invariant4 state changed after rejection")
	}
	if _, _, err := l.Replay(s3, 6); err != nil {
		return fmt.Errorf("selfcheck: invariant4 unusable after rejection: %w", err)
	}
	return nil
}
