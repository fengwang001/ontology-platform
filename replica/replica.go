// Package replica 实现对外缓存副本：读、回源（单飞合并）、
// 失效应用与惰性过期。副本之间不直接通信，只通过 bus 总线。
package replica

import (
	"context"
	"errors"
	"sync"
	"time"

	"ontology/entry"
	"ontology/single"
	"ontology/version"
)

// ErrTooManyEntries 表示条目数已达上限且无可淘汰条目（全部在回源中），
// 本次写入被拒绝且不改变任何已有状态。
var ErrTooManyEntries = errors.New("replica: too many entries")

// maxAttempts 是"回源结果被失效判定丢弃"时的最大重试次数。
const maxAttempts = 8

// Loader 是回源函数：返回键的当前值、后端当前版本、是否存在的标记。
// 返回错误时副本不得写入任何缓存。
type Loader func(ctx context.Context, key string) (value string, ver version.Version, found bool, err error)

// Config 是副本配置。
type Config struct {
	TTL         time.Duration // 条目存活时长，左闭右开
	MaxEntries  int           // 条目数上限，<=0 不限
	MaxInflight int           // 单飞并发上限，<=0 不限
	Loader      Loader
	Clock       Clock
}

type fetchResult struct {
	value string
	ver   version.Version
	found bool
}

// Replica 是一个缓存副本，并发安全。
type Replica struct {
	ttl    time.Duration
	maxCap int
	loader Loader
	clock  Clock
	group  *single.Group[fetchResult]

	mu      sync.Mutex
	entries map[string]*entry.Entry
	seq     uint64 // LRU 序号分配器

	hits           uint64
	misses         uint64
	refetches      uint64
	invalidations  uint64
	checkedEntries uint64 // 非导出：每次应用通知时检查的条目数
}

// New 创建副本。Loader 与 Clock 必填。
func New(cfg Config) *Replica {
	if cfg.Clock == nil {
		cfg.Clock = NewManualClock().Clock()
	}
	return &Replica{
		ttl:     cfg.TTL,
		maxCap:  cfg.MaxEntries,
		loader:  cfg.Loader,
		clock:   cfg.Clock,
		group:   single.New[fetchResult](cfg.MaxInflight),
		entries: make(map[string]*entry.Entry),
	}
}

// Get 读取一个键。命中且存活时直接返回；未命中、已失效或已过期时
// 触发回源（同键并发合并为一次）。found=false 表示后端确认键不存在
// （负缓存结果）。回源失败返回错误且不写缓存。
func (r *Replica) Get(ctx context.Context, key string) (value string, found bool, err error) {
	for attempt := 0; attempt < maxAttempts; attempt++ {
		now := r.clock()
		r.mu.Lock()
		e := r.entries[key]
		if e != nil && e.Alive(now) {
			e.IncHits()
			e.Touch(r.nextSeqLocked())
			r.hits++
			v, f := e.Value(), e.Found()
			r.mu.Unlock()
			return v, f, nil
		}
		if e == nil {
			// 单飞并发已满且无法合并：立刻拒绝，不产生任何副作用。
			if r.group.Saturated() && !r.group.InFlight(key) {
				r.mu.Unlock()
				return "", false, single.ErrTooManyInflight
			}
			var aerr error
			e, aerr = r.admitLocked(key)
			if aerr != nil {
				r.mu.Unlock()
				return "", false, aerr
			}
		}
		admitted := e.State() == entry.Hole && e.Floor().IsZero() && e.Stats().Misses == 0
		e.IncMisses()
		r.misses++
		r.mu.Unlock()

		res, ferr := r.group.Do(ctx, key, func(ctx context.Context) (fetchResult, error) {
			return r.load(ctx, key)
		})
		if ferr != nil {
			if errors.Is(ferr, single.ErrTooManyInflight) {
				r.rollbackMiss(key, e, admitted)
			}
			return "", false, ferr
		}

		r.mu.Lock()
		e = r.entries[key]
		if e == nil {
			ne, aerr := r.admitLocked(key)
			if aerr != nil {
				r.mu.Unlock()
				return res.value, res.found, nil // 无法入缓存，直接返回结果
			}
			e = ne
		}
		if !res.ver.Before(e.Floor()) {
			e.Fill(res.value, res.found, res.ver, r.clock()+r.ttl)
			e.Touch(r.nextSeqLocked())
			r.refetches++
			r.mu.Unlock()
			return res.value, res.found, nil
		}
		r.mu.Unlock()
		// 回源期间收到过更高版本的失效通知：结果已过期，丢弃并重试。
	}
	return "", false, errors.New("replica: fetch keeps losing to invalidations")
}

// rollbackMiss 撤销一次被单飞超限拒绝的未命中：回滚计数；
// 若条目是本次接纳且仍为空洞（未被通知写入），一并移除。
func (r *Replica) rollbackMiss(key string, e *entry.Entry, admitted bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.misses--
	e.UndoMiss()
	if admitted && r.entries[key] == e && e.State() == entry.Hole && e.Floor().IsZero() {
		delete(r.entries, key)
	}
}

func (r *Replica) load(ctx context.Context, key string) (fetchResult, error) {
	v, ver, found, err := r.loader(ctx, key)
	if err != nil {
		return fetchResult{}, err
	}
	return fetchResult{value: v, ver: ver, found: found}, nil
}

func (r *Replica) nextSeqLocked() uint64 {
	r.seq++
	return r.seq
}
