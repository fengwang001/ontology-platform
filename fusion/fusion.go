// Package fusion 实现带来源时效与名次惯性的多路检索得分归一化融合器。
package fusion

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// 拒绝原因（可用 errors.Is 判定）。
var (
	// ErrInvalidArgument 参数非法（空名称、越界权重/ttl、空 hits、空 doc、分数越界、负时钟、k<1）。
	ErrInvalidArgument = errors.New("fusion: invalid argument")
	// ErrDuplicateSource 来源名称已登记。
	ErrDuplicateSource = errors.New("fusion: duplicate source")
	// ErrSourceNotFound 来源未登记。
	ErrSourceNotFound = errors.New("fusion: source not found")
	// ErrClockRegression 时钟回退（now 小于水位）。
	ErrClockRegression = errors.New("fusion: clock regression")
	// ErrNoFusableSources 此刻没有任何有效来源。
	ErrNoFusableSources = errors.New("fusion: no fusable sources")
)

const (
	maxWeight   = 1000
	maxTTL      = 1000000
	maxScoreAbs = 1000000000000000 // 10^15
)

// Hit 是一条检索命中：doc 为非空文档标识，score 为原始分数。
type Hit struct {
	Doc   string
	Score int64
}

// Item 是 Fuse 返回的一项：doc 与既约分数文本（"分子/分母"，分母为正）。
type Item struct {
	Doc   string
	Score string
}

type source struct {
	weight       int
	higherBetter bool
	ttl          int64
	scores       map[string]int64 // 去重后的当前列表
	submitted    bool
	submitTime   int64
}

// Fuser 是多路检索得分归一化融合器，全部方法可并发调用。
type Fuser struct {
	mu       sync.Mutex
	sources  map[string]*source
	order    []string // 登记顺序
	h        int64    // 水位：至今所有成功 Submit 与 Fuse 的 now 最大值
	prevRank map[string]int
}

// NewFuser 创建一个空的融合器。
func NewFuser() *Fuser {
	return &Fuser{
		sources:  make(map[string]*source),
		prevRank: make(map[string]int),
	}
}

// AddSource 按登记先后加入来源。weight 为 1..1000，ttl 为 1..1000000。
func (f *Fuser) AddSource(name string, weight int, higherBetter bool, ttl int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name == "" || weight < 1 || weight > maxWeight || ttl < 1 || ttl > maxTTL {
		return fmt.Errorf("%w: AddSource name/weight/ttl", ErrInvalidArgument)
	}
	if _, ok := f.sources[name]; ok {
		return fmt.Errorf("%w: AddSource %q", ErrDuplicateSource, name)
	}
	f.sources[name] = &source{weight: weight, higherBetter: higherBetter, ttl: ttl}
	f.order = append(f.order, name)
	return nil
}

// Submit 用 hits 整体替换该来源当前的结果列表并记下提交时刻 now。
func (f *Fuser) Submit(name string, hits []Hit, now int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	src, ok := f.sources[name]
	if !ok {
		return fmt.Errorf("%w: Submit %q", ErrSourceNotFound, name)
	}
	if len(hits) == 0 || now < 0 {
		return fmt.Errorf("%w: Submit empty hits or negative now", ErrInvalidArgument)
	}
	for _, hit := range hits {
		if hit.Doc == "" || hit.Score > maxScoreAbs || hit.Score < -maxScoreAbs {
			return fmt.Errorf("%w: Submit doc/score", ErrInvalidArgument)
		}
	}
	if now < f.h {
		return fmt.Errorf("%w: Submit now=%d < H=%d", ErrClockRegression, now, f.h)
	}
	scores := make(map[string]int64, len(hits))
	for _, hit := range hits {
		cur, seen := scores[hit.Doc]
		if !seen || (src.higherBetter && hit.Score > cur) || (!src.higherBetter && hit.Score < cur) {
			scores[hit.Doc] = hit.Score
		}
	}
	src.scores = scores
	src.submitted = true
	src.submitTime = now
	if now > f.h {
		f.h = now
	}
	return nil
}

// Fuse 融合各有效来源并返回前 k 个结果。
func (f *Fuser) Fuse(now int64, k int) ([]Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if k < 1 || now < 0 {
		return nil, fmt.Errorf("%w: Fuse k/now", ErrInvalidArgument)
	}
	if now < f.h {
		return nil, fmt.Errorf("%w: Fuse now=%d < H=%d", ErrClockRegression, now, f.h)
	}
	type validSource struct {
		src    *source
		lo, hi int64
	}
	var valid []validSource
	for _, name := range f.order {
		src := f.sources[name]
		if !src.submitted || now-src.submitTime >= src.ttl {
			continue
		}
		var lo, hi int64
		first := true
		for _, s := range src.scores {
			if first || s < lo {
				lo = s
			}
			if first || s > hi {
				hi = s
			}
			first = false
		}
		valid = append(valid, validSource{src: src, lo: lo, hi: hi})
	}
	if len(valid) == 0 {
		return nil, ErrNoFusableSources
	}
	totals := make(map[string]*big.Rat)
	for _, vs := range valid {
		weight := big.NewRat(int64(vs.src.weight), 1)
		span := vs.hi - vs.lo
		for doc, s := range vs.src.scores {
			var n *big.Rat
			if span == 0 {
				n = big.NewRat(1, 1)
			} else if vs.src.higherBetter {
				n = big.NewRat(s-vs.lo, span)
			} else {
				n = big.NewRat(vs.hi-s, span)
			}
			n.Mul(n, weight)
			total, seen := totals[doc]
			if !seen {
				total = new(big.Rat)
				totals[doc] = total
			}
			total.Add(total, n)
		}
	}
	docs := make([]string, 0, len(totals))
	for doc := range totals {
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool {
		a, b := docs[i], docs[j]
		if c := totals[a].Cmp(totals[b]); c != 0 {
			return c > 0
		}
		ra, oka := f.prevRank[a]
		rb, okb := f.prevRank[b]
		if oka != okb {
			return oka
		}
		if oka && ra != rb {
			return ra < rb
		}
		return a < b
	})
	f.prevRank = make(map[string]int, len(docs))
	for i, doc := range docs {
		f.prevRank[doc] = i
	}
	if now > f.h {
		f.h = now
	}
	if k > len(docs) {
		k = len(docs)
	}
	items := make([]Item, k)
	for i := 0; i < k; i++ {
		items[i] = Item{Doc: docs[i], Score: ratString(totals[docs[i]])}
	}
	return items, nil
}

// ratString 输出既约分数文本 "分子/分母"，分母为正，整数也写成 "x/1"。
func ratString(r *big.Rat) string {
	return r.Num().String() + "/" + r.Denom().String()
}
