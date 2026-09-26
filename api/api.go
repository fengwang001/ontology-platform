// Package api 漏桶对外接口。依赖 sched。
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/lb"
	"ontology/sched"
)

// ErrConfig 配置非法（capacity<1 或 interval<1）的哨兵错误。
var ErrConfig = errors.New("api: invalid capacity or interval")

// Leaky 漏桶限流器，并发安全。
type Leaky struct{ s *sched.Submitter }

// New 构造漏桶；capacity<1 或 interval<1 返回 ErrConfig。
func New(capacity, interval int64) (*Leaky, error) {
	if capacity < 1 || interval < 1 {
		return nil, ErrConfig
	}
	return &Leaky{s: sched.New(lb.New(capacity, interval))}, nil
}

// Submit 提交一个携带时间戳 t 的请求。
func (l *Leaky) Submit(t int64) (dep int64, admitted bool, err error) {
	return l.s.Submit(t)
}

// InSystem 返回系统内（未漏出）项数。
func (l *Leaky) InSystem() int { return l.s.InSystem() }

// naive 朴素参照：显式维护出发时间列表。
type naive struct {
	cap, interval int64
	deps          []int64
}

func (n *naive) submit(t int64) (int64, bool) {
	i := 0
	for i < len(n.deps) && n.deps[i] <= t {
		i++
	}
	n.deps = n.deps[i:]
	if int64(len(n.deps)) == n.cap {
		return 0, false
	}
	dep := t + n.interval
	if len(n.deps) > 0 {
		dep = n.deps[len(n.deps)-1] + n.interval
	}
	n.deps = append(n.deps, dep)
	return dep, true
}

// SelfCheck 对内置请求序列核验四条不变量，全部通过返回 nil。
func (l *Leaky) SelfCheck() error {
	for _, cfg := range [][2]int64{{1, 1}, {3, 5}, {7, 2}} {
		if err := checkSequence(cfg[0], cfg[1]); err != nil {
			return err
		}
	}
	return checkFailures()
}

// checkSequence 核验不变量 1（朴素参照一致）、2（平滑性）、3（容量不越界）。
func checkSequence(cap, interval int64) error {
	l, err := New(cap, interval)
	if err != nil {
		return err
	}
	n := &naive{cap: cap, interval: interval}
	r := rand.New(rand.NewSource(cap*1000 + interval))
	var t, prevDep int64
	for i := 0; i < 200; i++ {
		t += r.Int63n(3 * interval)
		dep, ok, err := l.Submit(t)
		if err != nil {
			return fmt.Errorf("selfcheck: unexpected err: %w", err)
		}
		ndep, nok := n.submit(t)
		if ok != nok || (ok && dep != ndep) || l.InSystem() != len(n.deps) {
			return fmt.Errorf("selfcheck: mismatch with naive at t=%d", t)
		}
		if ok {
			if dep-prevDep < interval {
				return fmt.Errorf("selfcheck: smoothing violated at t=%d", t)
			}
			prevDep = dep
		}
		if int64(l.InSystem()) > cap {
			return fmt.Errorf("selfcheck: capacity exceeded at t=%d", t)
		}
	}
	return nil
}

// checkFailures 核验不变量 4（失败不留痕）与三类可判定错误。
func checkFailures() error {
	if _, err := New(0, 1); !errors.Is(err, ErrConfig) {
		return fmt.Errorf("selfcheck: bad capacity accepted")
	}
	if _, err := New(1, 0); !errors.Is(err, ErrConfig) {
		return fmt.Errorf("selfcheck: bad interval accepted")
	}
	l, _ := New(2, 5)
	if _, _, err := l.Submit(10); err != nil {
		return fmt.Errorf("selfcheck: unexpected err: %w", err)
	}
	before := l.InSystem()
	_, _, e1 := l.Submit(-1)
	_, _, e2 := l.Submit(9)
	if e1 == nil || e2 == nil || e1 == e2 || l.InSystem() != before {
		return fmt.Errorf("selfcheck: rejection left trace")
	}
	if _, ok, err := l.Submit(11); err != nil || !ok {
		return fmt.Errorf("selfcheck: unusable after rejection")
	}
	return nil
}
