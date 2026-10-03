// Package scrape 按目标抓取指标样本，维护 up 序列与陈旧标记。
package scrape

import (
	"errors"
	"sort"
	"sync"

	"ontology/head"
)

// Reason 是一次被接受抓取的类别。
type Reason int

const (
	Ok      Reason = iota // 成功
	Error                 // fetch 返回错误
	Panic                 // fetch panic（已 recover）
	Limit                 // 样本个数超过 Lim
	Invalid               // 名字非法（空、"up" 或超过 63 字节）
)

var (
	ErrInvalidParam    = errors.New("scrape: invalid parameter")
	ErrClockRegression = errors.New("scrape: clock regression")
	maxTargetLen       = 64
	maxNameLen         = 63
	maxNow             = int64(1e12)
)

// Result 描述一次被接受的抓取：Up 为 1（成功）或 0（失败）。
type Result struct {
	Up     int64
	Reason Reason
}

type targetState struct {
	mu      sync.Mutex // 同一 target 的 Scrape 串行
	lastNow int64
	hasLast bool
	prev    map[string]struct{} // 上一次成功抓取的名字集合
}

// Scraper 在 Head 上执行抓取；不同 target 可并行。
type Scraper struct {
	h       *head.Head
	lim     int
	mu      sync.Mutex
	targets map[string]*targetState
}

func New(h *head.Head) *Scraper {
	return &Scraper{h: h, lim: h.Limit(), targets: make(map[string]*targetState)}
}

func (s *Scraper) target(name string) *targetState {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := s.targets[name]
	if ts == nil {
		ts = &targetState{prev: make(map[string]struct{})}
		s.targets[name] = ts
	}
	return ts
}

// Scrape 对 target 执行一次抓取。fetch 在不持有任何 head 锁的情况下调用，
// panic 被 recover 并按失败处理。预校验任一项失败则整次不写，
// prev 与该 target 的上次 now 都不变，并返回该 head 错误。
func (s *Scraper) Scrape(now int64, target string, fetch func() (map[string]int64, error)) (Result, error) {
	if fetch == nil || target == "" || len(target) > maxTargetLen || now < 0 || now > maxNow {
		return Result{}, ErrInvalidParam
	}
	ts := s.target(target)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.hasLast && now <= ts.lastNow {
		return Result{}, ErrClockRegression
	}
	m, reason := doFetch(fetch, s.lim)
	items, up, prev := buildItems(target, now, m, reason, ts.prev)
	if err := s.h.ApplyBatch(items); err != nil {
		return Result{}, err
	}
	ts.lastNow, ts.hasLast, ts.prev = now, true, prev
	return Result{Up: up, Reason: reason}, nil
}

// doFetch 调用 fetch 并按 Panic、Error、Limit、Invalid 的顺序归类失败。
func doFetch(fetch func() (map[string]int64, error), lim int) (m map[string]int64, r Reason) {
	defer func() {
		if recover() != nil {
			m, r = nil, Panic
		}
	}()
	var err error
	if m, err = fetch(); err != nil {
		return nil, Error
	}
	if len(m) > lim {
		return nil, Limit
	}
	for name := range m {
		if name == "" || name == "up" || len(name) > maxNameLen {
			return nil, Invalid
		}
	}
	return m, Ok
}

// buildItems 构造写入集合。成功：按名字升序的结果样本、按名字升序的消失
// 名字陈旧标记、up=1；失败：按名字升序的 prev 陈旧标记、up=0。
func buildItems(target string, now int64, m map[string]int64, r Reason, prev map[string]struct{}) ([]head.Item, int64, map[string]struct{}) {
	key := func(name string) string { return target + "/" + name }
	items := make([]head.Item, 0, len(m)+len(prev)+1)
	next := make(map[string]struct{}, len(m))
	if r == Ok {
		for _, name := range sortedKeys(m) {
			items = append(items, head.Item{Series: key(name), Ts: now, V: m[name]})
			next[name] = struct{}{}
		}
		gone := make([]string, 0, len(prev))
		for name := range prev {
			if _, ok := m[name]; !ok {
				gone = append(gone, name)
			}
		}
		sort.Strings(gone)
		for _, name := range gone {
			items = append(items, head.Item{Series: key(name), Ts: now, Stale: true})
		}
		return append(items, head.Item{Series: key("up"), Ts: now, V: 1}), 1, next
	}
	names := make([]string, 0, len(prev))
	for name := range prev {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		items = append(items, head.Item{Series: key(name), Ts: now, Stale: true})
	}
	return append(items, head.Item{Series: key("up"), Ts: now}), 0, next
}

func sortedKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
