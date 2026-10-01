// Package fusion 提供带来源时效（TTL 与水位时钟）与名次惯性的多路检索
// 得分归一化融合器。
package fusion

import (
	"errors"
	"math/big"
	"sort"
)

const (
	minWeight = 1
	maxWeight = 1000
	minTTL    = int64(1)
	maxTTL    = int64(1_000_000)
	maxScore  = int64(1_000_000_000_000_000)
)

// 可区分的拒绝原因。
var (
	ErrInvalidArgument = errors.New("fusion: invalid argument")
	ErrDuplicateName   = errors.New("fusion: source name already registered")
	ErrSourceNotFound  = errors.New("fusion: source not found")
	ErrClockRollback   = errors.New("fusion: clock rollback")
	ErrNothingToFuse   = errors.New("fusion: no valid source to fuse")
)

// Hit 是一条来源内的原始检索结果。
type Hit struct {
	Doc   string
	Score int64
}

// ResultItem 是融合输出中的一项；Score 为既约分数文本 "分子/分母"。
type ResultItem struct {
	Doc   string
	Score string
}

// Merger 是并发安全的多路检索融合器。
// 零值不可用，请使用 New 创建。
type Merger struct {
	mu     chan struct{}
	order  []string
	source map[string]*sourceState

	// water 是水位 H：所有成功 Submit / Fuse 所用 now 的最大值。
	water int64
	// lastRanks 是上一次成功 Fuse 的完整名次表（名次 -> doc）。
	lastRanks []string
}

type sourceState struct {
	name         string
	weight       int
	higherBetter bool
	ttl          int64

	submitted bool
	submitAt  int64
	// 去重后的提交结果：doc -> 该来源内最好分数。
	scores map[string]int64
}

// New 创建一个空的融合器。
func New() *Merger {
	return &Merger{
		mu:     make(chan struct{}, 1),
		source: make(map[string]*sourceState),
	}
}

// AddSource 按登记先后加入一个来源。
func (m *Merger) AddSource(name string, weight int, higherBetter bool, ttl int64) error {
	if name == "" || weight < minWeight || weight > maxWeight || ttl < minTTL || ttl > maxTTL {
		return ErrInvalidArgument
	}
	m.lock()
	defer m.unlock()
	if _, ok := m.source[name]; ok {
		return ErrDuplicateName
	}
	m.source[name] = &sourceState{
		name:         name,
		weight:       weight,
		higherBetter: higherBetter,
		ttl:          ttl,
		scores:       make(map[string]int64),
	}
	m.order = append(m.order, name)
	return nil
}

// Submit 用 hits 整体替换某来源当前的结果列表。
func (m *Merger) Submit(name string, hits []Hit, now int64) error {
	m.lock()
	defer m.unlock()
	src, ok := m.source[name]
	if !ok {
		// 并发删除不会发生（没有删除接口），此处仅为防御性检查。
		return ErrSourceNotFound
	}
	if len(hits) == 0 || now < 0 {
		return ErrInvalidArgument
	}
	for _, h := range hits {
		if h.Doc == "" || h.Score < -maxScore || h.Score > maxScore {
			return ErrInvalidArgument
		}
	}
	if now < m.water {
		return ErrClockRollback
	}
	dedup := make(map[string]int64, len(hits))
	for _, h := range hits {
		if existing, ok := dedup[h.Doc]; ok {
			if src.higherBetter == (h.Score > existing) {
				dedup[h.Doc] = h.Score
			}
		} else {
			dedup[h.Doc] = h.Score
		}
	}
	src.scores = dedup
	src.submitted = true
	src.submitAt = now
	if now > m.water {
		m.water = now
	}
	return nil
}

// Fuse 在时刻 now 融合所有有效来源，返回总分最高的前 k 项。
func (m *Merger) Fuse(now int64, k int) ([]ResultItem, error) {
	if k < 1 || now < 0 {
		return nil, ErrInvalidArgument
	}
	m.lock()
	defer m.unlock()
	if now < m.water {
		return nil, ErrClockRollback
	}

	// 收集有效来源（已提交且 now-submitAt 严格小于 ttl）。
	var active []*sourceState
	for _, name := range m.order {
		src := m.source[name]
		if src.submitted && now-src.submitAt < src.ttl {
			active = append(active, src)
		}
	}
	if len(active) == 0 {
		return nil, ErrNothingToFuse
	}

	// 名次索引：doc -> 上一次完整名次表中的名次（从 0 起）。
	rankIndex := make(map[string]int, len(m.lastRanks))
	for i, doc := range m.lastRanks {
		rankIndex[doc] = i
	}

	totals := make(map[string]*big.Rat)
	weightRat := new(big.Rat)
	loRat := new(big.Rat)
	hiRat := new(big.Rat)
	spanRat := new(big.Rat)
	numRat := new(big.Rat)
	normRat := new(big.Rat)
	for _, src := range active {
		lo, hi := minMax(src.scores)
		weightRat.SetInt64(int64(src.weight))
		loRat.SetInt64(lo)
		hiRat.SetInt64(hi)
		spanRat.Sub(hiRat, loRat)
		for doc, score := range src.scores {
			if spanRat.Sign() == 0 {
				normRat.SetInt64(1)
			} else {
				numRat.SetInt64(score)
				if src.higherBetter {
					numRat.Sub(numRat, loRat)
				} else {
					numRat.Sub(hiRat, numRat)
				}
				normRat.SetFrac(numRat.Num(), spanRat.Num())
			}
			total, ok := totals[doc]
			if !ok {
				total = new(big.Rat)
				totals[doc] = total
			}
			total.Add(total, normRat.Mul(normRat, weightRat))
		}
	}

	docs := make([]string, 0, len(totals))
	for doc := range totals {
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool {
		return docLess(docs[i], docs[j], totals, rankIndex)
	})

	// 本次完整排序整体替换上一次名次表；水位更新。
	m.lastRanks = append(m.lastRanks[:0:0], docs...)
	if now > m.water {
		m.water = now
	}

	limit := k
	if limit > len(docs) {
		limit = len(docs)
	}
	out := make([]ResultItem, limit)
	for i := 0; i < limit; i++ {
		out[i] = ResultItem{Doc: docs[i], Score: formatRat(totals[docs[i]])}
	}
	return out, nil
}

// formatRat 始终输出 "分子/分母" 形式：整数写成 "x/1"，0 写成 "0/1"。
func formatRat(r *big.Rat) string {
	return r.Num().String() + "/" + r.Denom().String()
}

// docLess 实现排序键：总分降序；其次出现在上一次名次表者优先且名次升序；
// 都未出现则按 doc 字节序升序。
func docLess(a, b string, totals map[string]*big.Rat, rankIndex map[string]int) bool {
	if c := totals[b].Cmp(totals[a]); c != 0 {
		return c < 0
	}
	ra, oka := rankIndex[a]
	rb, okb := rankIndex[b]
	if oka != okb {
		return oka
	}
	if oka && ra != rb {
		return ra < rb
	}
	return a < b
}

func minMax(scores map[string]int64) (lo, hi int64) {
	first := true
	for _, s := range scores {
		if first {
			lo, hi = s, s
			first = false
			continue
		}
		if s < lo {
			lo = s
		}
		if s > hi {
			hi = s
		}
	}
	return lo, hi
}

func (m *Merger) lock()   { m.mu <- struct{}{} }
func (m *Merger) unlock() { <-m.mu }
