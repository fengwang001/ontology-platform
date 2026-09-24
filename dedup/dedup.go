// Package dedup 实现两层（current/history）布隆过滤器去重器。
package dedup

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/bf"
)

// 哨兵错误：三类非法输入互不相同，errors.Is 可判定。
var (
	ErrInvalidM   = errors.New("dedup: m must be a positive integer")
	ErrInvalidCap = errors.New("dedup: cap must be a positive integer")
	ErrEmptyKey   = errors.New("dedup: key must not be empty")
)

type Deduper struct {
	mu         sync.RWMutex
	m, cap     int
	cur, hist  *bf.Bloom
	histN      int // 已封存各层键数累计
	lastProbes int // 最近一次判定检查的位位置数；非导出，不在公开接口
}

// New 构造两层去重器；m/cap 非正在构造任何状态前失败（不变量 4）。
func New(m, cap int) (*Deduper, error) {
	if m <= 0 {
		return nil, ErrInvalidM
	}
	if cap <= 0 {
		return nil, ErrInvalidCap
	}
	cur, _ := bf.New(m)
	hist, _ := bf.New(m)
	return &Deduper{m: m, cap: cap, cur: cur, hist: hist}, nil
}

// rotate 饱和切换：当前层 OR 进历史层（单调），累加封存键数，清空当前层（不变量 2）。
func (d *Deduper) rotate() {
	d.hist.Merge(d.cur)
	d.histN += d.cur.Count()
	d.cur.Reset()
}

// feedOne 处理单键（调用方持锁且已校验）；两层都不含才判新（不变量 1）。
func (d *Deduper) feedOne(key string) bool {
	if d.cur.Count() == d.cap {
		d.rotate()
	}
	d.lastProbes = bf.K
	isNew := !d.cur.Contains(key)
	if isNew {
		d.lastProbes += bf.K
		isNew = !d.hist.Contains(key)
	}
	if isNew {
		d.cur.Add(key)
	}
	return isNew
}

// Feed 逐键判定（true=新）；先整批校验，任一键空则整批不生效（不变量 4）。
func (d *Deduper) Feed(keys []string) ([]bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, key := range keys {
		if key == "" {
			return nil, ErrEmptyKey
		}
	}
	out := make([]bool, len(keys))
	for i, key := range keys {
		out[i] = d.feedOne(key)
	}
	return out, nil
}

// layerFP 单层假阳性率：(1-(1-1/m)^(k·n))^k。
func layerFP(m, n int) float64 {
	e := math.Pow(1-1/float64(m), float64(bf.K*n))
	return math.Pow(1-e, bf.K)
}

// EstimateFP = 1-(1-FP当前)(1-FP历史)，历史键数取封存累计（不变量 3）。
func (d *Deduper) EstimateFP() float64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return 1 - (1-layerFP(d.m, d.cur.Count()))*(1-layerFP(d.m, d.histN))
}

// SelfCheck 用内置序列核验四条不变量与探针复杂度；只操作自建实例，不改接收者。
func (d *Deduper) SelfCheck() error {
	s, _ := New(8, 2)
	keys := []string{"a", "b", "c", "d", "i", "f", "a", "b"}
	want := []bool{true, true, true, true, false, true, false, false}
	seen := map[string]struct{}{}
	for i, k := range keys { // 第三节八步：判定/切换状态、真新键不二次判新
		got := s.feedOne(k)
		_, dup := seen[k]
		seen[k] = struct{}{}
		if got != want[i] || (got && dup) {
			return fmt.Errorf("step %d key %q: got %v want %v", i+1, k, got, want[i])
		}
	}
	if !s.hist.Contains("a") || !s.hist.Contains("b") ||
		!s.hist.Contains("c") || !s.hist.Contains("d") { // 不变量 2：历史层=封存位并集
		return fmt.Errorf("history missing sealed key")
	}
	if s.hist.Contains("f") || s.cur.Count() != 1 || s.histN != 4 ||
		s.EstimateFP() != 1-(1-layerFP(8, 1))*(1-layerFP(8, 4)) {
		return fmt.Errorf("post-rotation state or formula wrong")
	}
	long, _ := New(256, 1000) // 不变量 1：长序列对朴素精确集合无假阴性
	for i, exact := 0, map[string]struct{}{}; i < 500; i++ {
		k := fmt.Sprintf("k%d", (i*37)%211)
		_, known := exact[k]
		if long.feedOne(k) && known {
			return fmt.Errorf("false negative at %q", k)
		}
		exact[k] = struct{}{}
	}
	fresh, _ := New(1<<16, 100) // 不变量 4：拒绝不留痕，之后仍可用
	if _, e := fresh.Feed([]string{"q", ""}); !errors.Is(e, ErrEmptyKey) || fresh.EstimateFP() != 0 {
		return fmt.Errorf("reject: err=%v or state changed", e)
	}
	if r, _ := fresh.Feed([]string{"z"}); !r[0] {
		return fmt.Errorf("unusable after reject")
	}
	pc, _ := New(1<<20, 100001) // 复杂度：探针恒 ≤2k，不随 N 增长
	seq := 0
	kk := func(i int) string { return string(bytes.Repeat([]byte{0xff}, i+1)) }
	for _, n := range []int{100, 1000, 10000} {
		for ; pc.cur.Count() < n; seq++ {
			pc.feedOne(kk(seq))
		}
		pc.feedOne(kk(seq))
		if pc.lastProbes > 2*bf.K {
			return fmt.Errorf("N=%d probes=%d > %d", n, pc.lastProbes, 2*bf.K)
		}
		seq++
	}
	return nil
}
