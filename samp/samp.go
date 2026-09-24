// Package samp 维护当前采样率与按 (bucket, key) 有序的已知键索引，
// 提供 Feed 与 SetRate。本包只依赖 khash，不做参数越界校验
// （入参合法性由上层 api 统一在改动状态前拦截，见不变量 4）。
package samp

import (
	"sort"

	"ontology/khash"
)

// Event 是上游变更事件：键 Key 与值 V。
type Event struct {
	Key string
	V   int64
}

// knownKey 是有序索引中的一项：先按 bucket、再按 key 排列。
type knownKey struct {
	bucket int
	key    string
}

// Sampler 保存采样率与全部已知键。keys 始终有序，
// 因此任意桶区间 [lo,hi) 都可由两次二分定位（SetRate 的依据）。
type Sampler struct {
	rate  int
	keys  []knownKey
	known map[string]struct{}

	// lastChecked 记录最近一次 SetRate 检查过的已知键个数
	// （二分探测次数 + 区间内被取走的键数）。非导出，不经过任何公开方法暴露。
	lastChecked int
}

// New 创建采样率为 rate 的采样器（rate 合法性由 api 保证）。
func New(rate int) *Sampler {
	return &Sampler{rate: rate, known: make(map[string]struct{})}
}

// Rate 返回当前采样率。
func (s *Sampler) Rate() int { return s.rate }

// KnownCount 返回已知键的去重个数。
func (s *Sampler) KnownCount() int { return len(s.keys) }

// Has 报告 key 是否为已知键。
func (s *Sampler) Has(key string) bool {
	_, ok := s.known[key]
	return ok
}

// insertPos 返回 key（桶为 b）在有序索引中的插入位置，
// found 表示它已存在。
func (s *Sampler) insertPos(b int, key string) (pos int, found bool) {
	pos = sort.Search(len(s.keys), func(i int) bool {
		if s.keys[i].bucket != b {
			return s.keys[i].bucket > b
		}
		return s.keys[i].key >= key
	})
	found = pos < len(s.keys) && s.keys[pos].bucket == b && s.keys[pos].key == key
	return pos, found
}

// addKey 把新键登记进有序索引；已知键无操作。
func (s *Sampler) addKey(key string) {
	b := khash.Bucket(key)
	pos, found := s.insertPos(b, key)
	if found {
		return
	}
	s.keys = append(s.keys, knownKey{})
	copy(s.keys[pos+1:], s.keys[pos:])
	s.keys[pos] = knownKey{b, key}
	s.known[key] = struct{}{}
}

// Feed 登记所有出现过的键（含未被采样的），并按当前采样率
// 逐事件直接判定，保持原顺序返回被采样事件。
// 与朴素参照逐事件同公式判定完全一致（不变量 1）。
func (s *Sampler) Feed(evs []Event) []Event {
	out := make([]Event, 0, len(evs))
	for _, e := range evs {
		s.addKey(e.Key)
		if khash.Sampled(e.Key, s.rate) {
			out = append(out, e)
		}
	}
	return out
}

// Sampled 报告 key 在当前采样率下是否被采样。
func (s *Sampler) Sampled(key string) bool {
	return khash.Sampled(key, s.rate)
}

// searchGE 二分返回第一个 bucket >= b 的索引位置，
// 每探测一个键就给 lastChecked 加一。
func (s *Sampler) searchGE(b int) int {
	lo, hi := 0, len(s.keys)
	for lo < hi {
		mid := (lo + hi) / 2
		s.lastChecked++
		if s.keys[mid].bucket < b {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// SetRate 把采样率从 r1 改为 r2，返回新纳入与被移出的已知键。
// 会改变判定的只有桶落在 [min(r1,r2), max(r1,r2)) 内的键；
// 索引有序，两次二分即可圈出区间，区间内键已按 (bucket,key) 有序。
// r2>r1 时区间内键全部是新纳入（移出必空），反之亦然（不变量 3）。
func (s *Sampler) SetRate(r2 int) (added, removed []string) {
	s.lastChecked = 0
	if r2 == s.rate {
		return nil, nil
	}
	loB, hiB := s.rate, r2
	if r2 < s.rate {
		loB, hiB = r2, s.rate
	}
	l := s.searchGE(loB)
	r := s.searchGE(hiB)
	diff := make([]string, 0, r-l)
	for i := l; i < r; i++ {
		s.lastChecked++
		diff = append(diff, s.keys[i].key)
	}
	if r2 > s.rate {
		added = diff
	} else {
		removed = diff
	}
	s.rate = r2
	return added, removed
}
