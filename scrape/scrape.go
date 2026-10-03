// Package scrape 按 target 抓取样本并维护 up 序列与陈旧标记：
// 成功时写入结果样本、为消失的名字写陈旧标记、up=1；
// 失败时为 prev 中每个名字写陈旧标记、up=0。写集预校验后整体原子提交。
package scrape

import (
	"errors"
	"sort"
	"sync"

	"ontology/head"
)

var (
	ErrInvalidParam = errors.New("scrape: invalid argument")
	ErrClock        = errors.New("scrape: now not greater than last accepted")
)

// Reason 是一次被接受抓取的结果原因。
type Reason int

const (
	Ok Reason = iota
	Error
	Panic
	Limit
	Invalid
)

func (r Reason) String() string {
	switch r {
	case Error:
		return "Error"
	case Panic:
		return "Panic"
	case Limit:
		return "Limit"
	case Invalid:
		return "Invalid"
	default:
		return "Ok"
	}
}

// Result 是一次被接受抓取的结果。
type Result struct {
	Up     bool
	Reason Reason
}

type targetState struct {
	lastNow int64
	accept  bool     // 是否已有被接受的抓取
	prev    []string // 上一次成功抓取的名字集合，升序
}

type targetLock struct {
	mu    sync.Mutex
	state targetState
}

// Scraper 管理所有 target 的抓取状态，可并发使用。
type Scraper struct {
	h       *head.Head
	mu      sync.Mutex
	targets map[string]*targetLock
}

func New(h *head.Head) *Scraper {
	return &Scraper{h: h, targets: map[string]*targetLock{}}
}

func (s *Scraper) target(name string) *targetLock {
	s.mu.Lock()
	defer s.mu.Unlock()
	tl, ok := s.targets[name]
	if !ok {
		tl = &targetLock{}
		s.targets[name] = tl
	}
	return tl
}

// Scrape 抓取一次 target。now 必须严格大于该 target 上一次被接受的 now；
// 参数非法先于时钟回退判定，被拒绝时不调用 fetch、状态不变。
// 写集任一项校验失败则整体不写、prev 与 lastNow 不变，并返回该 head 错误。
func (s *Scraper) Scrape(now int64, target string, fetch func() (map[string]int64, error)) (Result, error) {
	if fetch == nil || target == "" || len(target) > 64 || now < 0 || now > 1e12 {
		return Result{}, ErrInvalidParam
	}
	tl := s.target(target)
	tl.mu.Lock()
	defer tl.mu.Unlock()
	if tl.state.accept && now <= tl.state.lastNow {
		return Result{}, ErrClock
	}
	samples, reason := runFetch(fetch, s.h.Lim)
	items := buildItems(target, now, samples, tl.state.prev, reason)
	if err := s.h.ApplyBatch(items); err != nil {
		return Result{}, err
	}
	if reason == Ok {
		tl.state.prev = sortedKeys(samples)
	} else {
		tl.state.prev = nil
	}
	tl.state.lastNow = now
	tl.state.accept = true
	return Result{Up: reason == Ok, Reason: reason}, nil
}

// runFetch 在不持有任何 head 锁的情况下调用 fetch，recover panic；
// 失败原因按 Panic、Error、Limit、Invalid 取第一个成立者。
func runFetch(fetch func() (map[string]int64, error), lim int) (m map[string]int64, r Reason) {
	defer func() {
		if rec := recover(); rec != nil {
			m, r = nil, Panic
		}
	}()
	m, err := fetch()
	if err != nil {
		return nil, Error
	}
	if len(m) > lim {
		return nil, Limit
	}
	for name := range m {
		if name == "" || name == "up" || len(name) > 63 {
			return nil, Invalid
		}
	}
	return m, Ok
}

// buildItems 构造写集：成功时为结果各名字（升序）写值、为 prev 中消失的
// 名字（升序）写陈旧标记、up=1；失败时为 prev 每个名字（升序）写陈旧标记、up=0。
func buildItems(target string, now int64, samples map[string]int64, prev []string, r Reason) []head.Item {
	var items []head.Item
	if r == Ok {
		inResult := make(map[string]bool, len(samples))
		for _, name := range sortedKeys(samples) {
			items = append(items, head.Item{Series: target + "/" + name, Ts: now, V: samples[name]})
			inResult[name] = true
		}
		for _, name := range prev {
			if !inResult[name] {
				items = append(items, head.Item{Series: target + "/" + name, Ts: now, Stale: true})
			}
		}
	} else {
		for _, name := range prev {
			items = append(items, head.Item{Series: target + "/" + name, Ts: now, Stale: true})
		}
	}
	up := int64(0)
	if r == Ok {
		up = 1
	}
	return append(items, head.Item{Series: target + "/up", Ts: now, V: up})
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
