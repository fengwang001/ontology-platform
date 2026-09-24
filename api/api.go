// Package api 对外提供迟到率自适应水位线的并发安全入口。依赖 adapt。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/adapt"
	"ontology/wmline"
)

// 五类可判定的哨兵错误，互不相同。
var (
	ErrMinGtMax    = errors.New("minDelay > maxDelay")
	ErrNegativeMin = errors.New("minDelay < 0")
	ErrBadStep     = errors.New("step <= 0")
	ErrBadWindow   = errors.New("W < 1")
	ErrBadThresh   = errors.New("阈值越界: 需满足 0<=lo<hi<=W")
)

// Store 是并发安全的水位线入口。
type Store struct {
	mu  sync.RWMutex
	ctl *adapt.Controller
}

// New 先校验全部参数，任一不合法则整体失败、不产生任何状态。
func New(minDelay, maxDelay, step, W, hi, lo int64) (*Store, error) {
	switch {
	case minDelay < 0:
		return nil, ErrNegativeMin
	case minDelay > maxDelay:
		return nil, ErrMinGtMax
	case step <= 0:
		return nil, ErrBadStep
	case W < 1:
		return nil, ErrBadWindow
	case lo < 0 || hi > W || lo >= hi:
		return nil, ErrBadThresh
	}
	return &Store{ctl: adapt.New(wmline.New(minDelay, maxDelay), step, W, hi, lo)}, nil
}

// Feed 处理一个事件，返回是否迟到。
func (s *Store) Feed(ts int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctl.Feed(ts)
}

// WM 返回当前水位线；Delay 返回当前滞后量。
func (s *Store) WM() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ctl.WM()
}

func (s *Store) Delay() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ctl.Delay()
}

// SelfCheck 用内置事件序列核验四条不变量；只用本地新建的实例，可并发调用。
func (s *Store) SelfCheck() error {
	checks := []func() error{checkFixedEquiv, checkBounds, checkConverge, checkRejectClean}
	names := []string{"不变量1(固定delay与朴素一致)", "不变量2(delay在界内)", "不变量3(收敛)", "不变量4(失败不留痕)"}
	for i, fn := range checks {
		if err := fn(); err != nil {
			return fmt.Errorf("%s: %w", names[i], err)
		}
	}
	return nil
}

// 不变量1：delay 固定时逐条迟到判定等于朴素「TS < 此前最大TS - delay」。
func checkFixedEquiv() error {
	const d = int64(2)
	st, _ := New(d, 1<<40, 1, 4, 4, 0) // hi=W,lo=0；下面每窗恰 1 条迟到，落在 (0,4) 内不触发调整
	maxSeen := int64(-1) << 62
	for i := int64(0); i < 40; i++ {
		ts := 1000 + i
		if i%4 == 3 {
			ts -= 4 // 每窗第 4 条下潜，必迟到
		}
		if got, want := st.Feed(ts), ts < maxSeen-d; got != want {
			return fmt.Errorf("第%d条: got=%v want=%v", i, got, want)
		}
		if ts > maxSeen {
			maxSeen = ts
		}
	}
	if st.Delay() != d {
		return fmt.Errorf("delay 被意外调整为 %d", st.Delay())
	}
	return nil
}

// 不变量2：极端序列下每一步 delay 都在 [minDelay, maxDelay]。
func checkBounds() error {
	st, _ := New(2, 10, 3, 2, 2, 0)
	st.Feed(1000) // 确立 maxSeen，wm=998
	for i := int64(0); i < 80; i++ {
		ts := int64(0) // 前 40 条持续迟到 → delay 升向上限 10 并钳住
		if i >= 40 {
			ts = 2000 + i // 后 40 条持续正常 → delay 降回下限 2 并钳住
		}
		st.Feed(ts)
		if v := st.Delay(); v < 2 || v > 10 {
			return fmt.Errorf("第%d步 delay=%d 越界", i, v)
		}
	}
	return nil
}

// 不变量3：迟到密集窗口上调 delay，随后连续正常窗口逐步回落到 minDelay。
func checkConverge() error {
	st, _ := New(2, 10, 2, 3, 2, 0)
	for _, ts := range []int64{10, 11, 9, 5, 6, 15, 16, 17, 18} { // NOTES.md 九事件序列
		st.Feed(ts)
	}
	if st.Delay() != 2 { // 第6步上调到4，第9步回落到2
		return fmt.Errorf("九事件后 delay=%d, 期望 2", st.Delay())
	}
	for i := int64(0); i < 30; i++ { // 稳定流上保持收敛在 minDelay
		st.Feed(100 + i)
	}
	if st.Delay() != 2 {
		return fmt.Errorf("稳定流后 delay=%d, 期望收敛于 2", st.Delay())
	}
	return nil
}

// 不变量4：五类非法参数各自整体失败且错误互不相同。
func checkRejectClean() error {
	bads := [][6]int64{{5, 2, 1, 1, 1, 0}, {-1, 2, 1, 1, 1, 0}, {0, 2, 0, 1, 1, 0}, {0, 2, 1, 0, 1, 0}, {0, 2, 1, 1, 1, 1}}
	seen := map[error]bool{}
	for i, b := range bads {
		_, err := New(b[0], b[1], b[2], b[3], b[4], b[5])
		if err == nil {
			return fmt.Errorf("第%d组非法参数未被拒绝", i)
		}
		if seen[err] {
			return fmt.Errorf("第%d组错误与之前雷同: %v", i, err)
		}
		seen[err] = true
	}
	return nil
}
