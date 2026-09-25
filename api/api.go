// Package api 是 PCP 锁管理器的对外入口，仅依赖 lock（依赖方向单向）。
package api

import (
	"errors"
	"fmt"

	"ontology/lock"
)

// 四类哨兵错误自 lock 提升：对外只需依赖本包，两两可判定、互不相同。
var (
	ErrUnknownTask     = lock.ErrUnknownTask
	ErrUnknownResource = lock.ErrUnknownResource
	ErrAlreadyHeld     = lock.ErrAlreadyHeld
	ErrNotHeld         = lock.ErrNotHeld
)

type Manager struct{ l *lock.Manager }

func New() *Manager                                  { return &Manager{l: lock.NewManager()} }
func (m *Manager) AddTask(id string, p int)          { m.l.AddTask(id, p) }
func (m *Manager) AddResource(n string)              { m.l.AddResource(n) }
func (m *Manager) Use(t, r string) error             { return m.l.Use(t, r) }
func (m *Manager) Acquire(t, r string) (bool, error) { return m.l.Acquire(t, r) }
func (m *Manager) Release(t, r string) error         { return m.l.Release(t, r) }
func (m *Manager) SystemCeiling() int                { return m.l.SystemCeiling() }
func (m *Manager) EffectivePriority(t string) int    { return m.l.EffectivePriority(t) }
func (m *Manager) Ceiling(r string) int              { return m.l.Ceiling(r) }

// naive 是规范的朴素参照：每次 Acquire 扫描全部已持有资源求系统天花板。
type naive struct {
	prio map[string]int
	ceil map[string]int
	hold map[string]string
}

func newNaive() *naive { return &naive{map[string]int{}, map[string]int{}, map[string]string{}} }
func (n *naive) sys(t string) (b int) {
	for r, h := range n.hold {
		if h != t && n.ceil[r] > b {
			b = n.ceil[r]
		}
	}
	return
}
func (n *naive) acq(t, r string) bool {
	if _, busy := n.hold[r]; busy || n.prio[t] <= n.sys(t) {
		return false
	}
	n.hold[r] = t
	return true
}

// SelfCheck 在第三节八步序列上逐条核验四条不变量；全成立返回 nil。
// 每次使用全新内部状态，可重复、可并发调用，不影响接收者状态。
func (m *Manager) SelfCheck() error {
	c, n := New(), newNaive()
	c.AddTask("T1", 5)
	c.AddTask("T2", 3)
	c.AddTask("T3", 1)
	n.prio = map[string]int{"T1": 5, "T2": 3, "T3": 1}
	c.AddResource("R_x")
	c.AddResource("R_y")
	for _, u := range [][2]string{{"T1", "R_x"}, {"T3", "R_x"}, {"T2", "R_y"}, {"T3", "R_y"}} {
		if err := c.Use(u[0], u[1]); err != nil {
			return err
		}
		if n.prio[u[0]] > n.ceil[u[1]] {
			n.ceil[u[1]] = n.prio[u[0]]
		}
	}
	if c.Ceiling("R_x") != 5 || c.Ceiling("R_y") != 3 { // 不变量3：天花板正确
		return fmt.Errorf("ceiling Rx=%d Ry=%d want 5,3", c.Ceiling("R_x"), c.Ceiling("R_y"))
	}
	type step struct {
		t, r    string
		rel     bool
		granted bool
		sys, ep int
	}
	steps := []step{
		{"T3", "R_x", false, true, 5, 5}, {"T2", "R_y", false, false, 5, 3},
		{"T1", "R_x", false, false, 5, 5}, {"T3", "R_x", true, false, 0, 1},
		{"T1", "R_x", false, true, 5, 5}, {"T2", "R_y", false, false, 5, 3},
		{"T1", "R_x", true, false, 0, 5}, {"T2", "R_y", false, true, 3, 3},
	}
	for i, s := range steps {
		if s.rel {
			if err := c.Release(s.t, s.r); err != nil {
				return fmt.Errorf("step %d release: %w", i+1, err)
			}
			delete(n.hold, s.r) // 固定序列中该步必为本人持有
		} else {
			got, err := c.Acquire(s.t, s.r)
			want := n.acq(s.t, s.r)
			if err != nil || got != want || got != s.granted { // 不变量1：与朴素参照一致
				return fmt.Errorf("step %d granted=%v naive=%v want=%v err=%v", i+1, got, want, s.granted, err)
			}
		}
		if c.SystemCeiling() != s.sys { // 不变量2：系统天花板与授予一致
			return fmt.Errorf("step %d sys=%d want %d", i+1, c.SystemCeiling(), s.sys)
		}
		if ep := c.EffectivePriority(s.t); ep != s.ep { // 不变量3：持锁即抬升
			return fmt.Errorf("step %d EP(%s)=%d want %d", i+1, s.t, ep, s.ep)
		}
	}
	return selfCheckErrors() // 不变量4：失败不留痕
}

// selfCheckErrors 核验四类哨兵错误互不相同、被拒后状态不变且可继续使用。
func selfCheckErrors() error {
	d := New()
	d.AddTask("T1", 5)
	d.AddTask("T2", 3)
	d.AddResource("R_x")
	if err := d.Use("T1", "R_x"); err != nil {
		return err
	}
	reject := func(got, want error) error {
		if !errors.Is(got, want) {
			return fmt.Errorf("got %v want %v", got, want)
		}
		return nil
	}
	_, e := d.Acquire("??", "R_x")
	if err := reject(e, ErrUnknownTask); err != nil {
		return err
	}
	if _, e := d.Acquire("T1", "??"); !errors.Is(e, ErrUnknownResource) {
		return reject(e, ErrUnknownResource)
	}
	if g, e := d.Acquire("T1", "R_x"); !g || e != nil {
		return fmt.Errorf("setup acquire g=%v e=%v", g, e)
	}
	if _, e := d.Acquire("T1", "R_x"); !errors.Is(e, ErrAlreadyHeld) {
		return reject(e, ErrAlreadyHeld)
	}
	if e := d.Release("T2", "R_x"); !errors.Is(e, ErrNotHeld) {
		return reject(e, ErrNotHeld)
	}
	if d.SystemCeiling() != 5 || d.EffectivePriority("T1") != 5 {
		return errors.New("state changed after rejected ops")
	}
	s := []error{ErrUnknownTask, ErrUnknownResource, ErrAlreadyHeld, ErrNotHeld}
	if errors.Is(s[0], s[1]) || errors.Is(s[1], s[2]) || errors.Is(s[2], s[3]) {
		return errors.New("sentinels not distinct")
	}
	return d.Release("T1", "R_x") // 被拒后仍可正常使用
}
