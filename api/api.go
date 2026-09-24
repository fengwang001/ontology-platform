// Package api 是一致采样的对外入口：New/Feed/SetRate/Sampled/SelfCheck。
// 失败一律是可判定哨兵错误，先整批校验、通过后才改状态（不变量 4）。
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/khash"
	"ontology/samp"
)

// 三类互不相同的哨兵错误；maxKeys<1 按题意并入采样率越界类。
var (
	ErrRateOutOfRange = errors.New("rate out of [0,10000] or maxKeys < 1")
	ErrEmptyKey       = errors.New("event key must not be empty")
	ErrTooManyKeys    = errors.New("known key count would exceed maxKeys")
)

type Event struct {
	Key string
	V   int64
}

type API struct {
	mu      sync.RWMutex
	s       *samp.Sampler
	maxKeys int
}

// New 以采样率 rate 与已知键上限 maxKeys 创建采样器。
func New(rate, maxKeys int) (*API, error) {
	if rate < 0 || rate > khash.Buckets || maxKeys < 1 {
		return nil, ErrRateOutOfRange
	}
	return &API{s: samp.New(rate), maxKeys: maxKeys}, nil
}

// Feed 返回被采样事件（保持原顺序），出现过的键都登记为已知键。
// 任一条非法则整批不生效：无输出、已知键与采样率不变（不变量 4）。
func (a *API) Feed(evs []Event) ([]Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := map[string]struct{}{}
	n := 0
	for _, e := range evs { // 第一遍纯校验，不触碰状态
		if e.Key == "" {
			return nil, ErrEmptyKey
		}
		if _, dup := seen[e.Key]; !dup && !a.s.Has(e.Key) {
			seen[e.Key] = struct{}{}
			n++
		}
	}
	if a.s.KnownCount()+n > a.maxKeys {
		return nil, ErrTooManyKeys
	}
	in := make([]samp.Event, len(evs))
	for i, e := range evs {
		in[i] = samp.Event{Key: e.Key, V: e.V}
	}
	out := a.s.Feed(in) // 不变量 1：逐事件直判，与朴素参照同公式
	res := make([]Event, len(out))
	for i, e := range out {
		res[i] = Event{Key: e.Key, V: e.V}
	}
	return res, nil
}

// SetRate 调整采样率，返回按 (bucket,key) 有序的新纳入/被移出列表。
func (a *API) SetRate(r int) (added, removed []string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r < 0 || r > khash.Buckets {
		return nil, nil, ErrRateOutOfRange
	}
	added, removed = a.s.SetRate(r)
	return added, removed, nil
}

func (a *API) Sampled(key string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.s.Sampled(key)
}

// 第三节内置序列（八个事件）与其去重后的六个已知键。
var (
	eightKeys = []string{"gnj", "dzv", "gnk", "kcm", "dzv", "qjy", "a6m", "dzv"}
	knownKeys = []string{"gnj", "dzv", "gnk", "kcm", "qjy", "a6m"}
)

// SelfCheck 用第三节内置序列核验第二节四条不变量；只用局部实例，可并发调用。
func (a *API) SelfCheck() error {
	var first error
	ok := func(c bool, f string, args ...any) { // 记录首个失败
		if first == nil && !c {
			first = fmt.Errorf(f, args...)
		}
	}
	evs := make([]Event, len(eightKeys))
	for i, k := range eightKeys {
		evs[i] = Event{Key: k, V: int64(i)}
	}
	s, err := New(2500, 100)
	if err != nil {
		return err
	}
	out, err := s.Feed(evs) // 不变量 1：输出须与逐事件朴素判定逐一相符
	if err != nil {
		return err
	}
	j := 0
	for _, e := range evs {
		if khash.Sampled(e.Key, 2500) {
			ok(j < len(out) && out[j] == e, "inv1 feed event %d", j)
			j++
		}
	}
	ok(j == len(out), "inv1 feed len %d want %d", len(out), j)
	ad, rm, e := s.SetRate(2000) // 不变量 1+3：值见 NOTES 十行表，调低无纳入
	ok(e == nil && slices.Equal(ad, []string(nil)) && slices.Equal(rm, []string{"qjy", "gnj"}), "inv1/3 @2000: %v/%v", ad, rm)
	ad, rm, e = s.SetRate(5000) // 不变量 3：调高无移出；gnk 桶恰 2500 此刻才纳入
	ok(e == nil && slices.Equal(ad, []string{"qjy", "gnj", "gnk"}) && slices.Equal(rm, []string(nil)), "inv1/3 @5000: %v/%v", ad, rm)
	for _, k := range knownKeys { // 不变量 2：两独立实例与公式判定一致
		x, _ := New(5000, 10)
		y, _ := New(5000, 10)
		ok(x.Sampled(k) == y.Sampled(k) && x.Sampled(k) == khash.Sampled(k, 5000), "inv2 key %s", k)
	}
	// 不变量 4：三类拒绝互不相同，拒绝前后状态不变、之后仍可正常使用。
	_, er := New(10001, 1)
	ok(errors.Is(er, ErrRateOutOfRange), "inv4 New rate")
	_, er = New(0, 0)
	ok(errors.Is(er, ErrRateOutOfRange), "inv4 New maxKeys")
	_, _, er = s.SetRate(10001)
	ok(errors.Is(er, ErrRateOutOfRange) && s.s.Rate() == 5000, "inv4 setrate trace")
	_, er = s.Feed([]Event{{Key: ""}})
	ok(errors.Is(er, ErrEmptyKey) && s.s.KnownCount() == 6, "inv4 empty trace")
	z, _ := New(2500, 1)
	_, er = z.Feed([]Event{{Key: "x"}, {Key: "y"}})
	ok(errors.Is(er, ErrTooManyKeys) && z.s.KnownCount() == 0, "inv4 overflow trace")
	_, er = z.Feed([]Event{{Key: "x"}})
	ok(er == nil, "inv4 unusable after reject: %v", er)
	return first
}
