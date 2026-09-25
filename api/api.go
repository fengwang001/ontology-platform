// Package api 是对外公开接口：New / Commit / View / Retries / SelfCheck。依赖 commit。
package api

import (
	"fmt"
	"maps"
	"sync"

	"ontology/commit"
)

// Op 是对外暴露的操作类型。
type Op = commit.Op

// 对外可判定的哨兵错误（与 commit 包同一批）。
var (
	ErrEmptyBatch = commit.ErrEmptyBatch
	ErrEmptyKey   = commit.ErrEmptyKey
	ErrZeroDelta  = commit.ErrZeroDelta
	ErrBadConfig  = commit.ErrBadConfig
	ErrContended  = commit.ErrContended
)

// Engine 是物化视图提交引擎。
type Engine struct {
	c *commit.Committer
}

// New 构造引擎；maxRetries < 1 时返回 ErrBadConfig。
func New(maxRetries int) (*Engine, error) {
	c, err := commit.New(maxRetries)
	if err != nil {
		return nil, err
	}
	return &Engine{c: c}, nil
}

// Commit 提交一批操作。
func (e *Engine) Commit(batch []Op) error { return e.c.Commit(batch) }

// View 返回当前 Key -> val 快照。
func (e *Engine) View() map[string]int64 { return e.c.View() }

// Retries 返回累计「冲突后重试并最终成功」的次数。
func (e *Engine) Retries() int64 { return e.c.Retries() }

// SelfCheck 用一台全新的内部引擎跑内置并发提交序列，核验四条不变量：
// 1) 与朴素重放一致；2) 无部分提交；3) 版本恰 +1（间接：并发下同 Key
// 不丢更新）；4) 失败不留痕。全部通过返回 nil，否则返回首个失败项。
func (e *Engine) SelfCheck() error {
	c, err := commit.New(8)
	if err != nil {
		return err
	}
	// 不变量 1：并发提交与「逐 Key 净增量求和」的朴素重放一致。
	batches := [][]Op{
		{{Key: "a", Delta: 1}, {Key: "b", Delta: 2}}, {{Key: "a", Delta: 3}, {Key: "c", Delta: 5}}, {{Key: "b", Delta: -1}, {Key: "c", Delta: 1}},
		{{Key: "a", Delta: -2}, {Key: "d", Delta: 7}}, {{Key: "c", Delta: 4}, {Key: "d", Delta: -3}}, {{Key: "b", Delta: 6}, {Key: "d", Delta: 1}},
	}
	want := map[string]int64{}
	for _, b := range batches {
		for _, op := range b {
			want[op.Key] += op.Delta
		}
	}
	var wg sync.WaitGroup
	for _, b := range batches {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Commit(b) }()
	}
	wg.Wait()
	if got := c.View(); !maps.Equal(got, want) {
		return fmt.Errorf("selfcheck: naive replay mismatch: got %v want %v", got, want)
	}
	// 不变量 2：并发读者任何时候都看不到「改了一半」的批（x+y 恒为 0）。
	c2, _ := commit.New(8)
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	bad := make(chan string, 1)
	for i := 0; i < 4; i++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v := c2.View()
				if v["x"]+v["y"] != 0 {
					select {
					case bad <- "partial commit observed":
					default:
					}
					return
				}
			}
		}()
	}
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c2.Commit([]Op{{Key: "x", Delta: 1}, {Key: "y", Delta: -1}}) }()
	}
	wg.Wait()
	close(stop)
	rwg.Wait()
	select {
	case msg := <-bad:
		return fmt.Errorf("selfcheck: %s", msg)
	default:
	}
	// 不变量 3（间接）：N 个写者并发同 Key 各 +1，不丢更新则版本校验生效。
	c3, _ := commit.New(16)
	const n = 64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c3.Commit([]Op{{Key: "k", Delta: 1}}) }()
	}
	wg.Wait()
	if got := c3.View()["k"]; got != n {
		return fmt.Errorf("selfcheck: lost update: got %d want %d", got, n)
	}
	// 不变量 4：四类拒绝后值与重试计数不变，且引擎仍可用。
	before, retries := c3.View(), c3.Retries()
	rejects := [][]Op{nil, {{Key: "", Delta: 1}}, {{Key: "k", Delta: 0}}, {{Key: "k", Delta: 1}, {Key: "k", Delta: -1}}}
	for _, b := range rejects {
		if err := c3.Commit(b); err == nil {
			return fmt.Errorf("selfcheck: batch %v unexpectedly accepted", b)
		}
	}
	if !maps.Equal(c3.View(), before) || c3.Retries() != retries {
		return fmt.Errorf("selfcheck: rejected commit left a trace")
	}
	if err := c3.Commit([]Op{{Key: "k", Delta: 1}}); err != nil || c3.View()["k"] != n+1 {
		return fmt.Errorf("selfcheck: engine unusable after rejects")
	}
	return nil
}
