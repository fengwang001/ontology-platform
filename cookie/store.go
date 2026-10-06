package cookie

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Config 为存储的可配置参数。SiteLimit/GlobalLimit 为 0 表示不限制；
// LaxGrace 是未显式声明同站模式的条目创建后允许附带到
// 非安全方法顶层跨站请求的宽限时长（恰好到达即不再允许）。
type Config struct {
	SiteLimit   int
	GlobalLimit int
	LaxGrace    time.Duration
}

// Stats 为三类删除与累计新增的计数，满足守恒式：
// ExpiredEvictions + CapacityEvictions + ExplicitDeletes + 当前条目数 == TotalAdds。
type Stats struct {
	TotalAdds         uint64 // 累计新增条目数（覆盖不计）
	ExpiredEvictions  uint64 // 过期淘汰
	CapacityEvictions uint64 // 容量淘汰
	ExplicitDeletes   uint64 // 显式删除（含过期写入删除与清除操作）
}

// Store 是 Cookie 存储内核。所有公开方法均可并发调用，
// 内部以单互斥锁串行化，结果等价于某个串行顺序。
type Store struct {
	mu      sync.Mutex
	cfg     Config
	now     time.Time
	seq     uint64
	entries map[key]*Entry
	sites   map[string]*siteIndex
	siteOrd siteHeap // 按站点条目数的惰性大顶堆
	expiry  expiryHeap
	stats   Stats

	// 性能可验证性探针（测试用）
	attachScans int // 附带判定扫描的候选条目数
	evictPops   int // 淘汰选择弹出的堆项数
	sitePops    int // 全局淘汰选择站点时弹出的堆项数
}

type siteIndex struct {
	name   string
	items  map[key]*Entry
	lru    lruHeap
	expiry expiryHeap
	gen    uint64 // 条目数变化时递增，siteOrd 惰性失效用
}

// New 创建空存储，内部时钟从零时刻开始，由 AdvanceClock 推进。
func New(cfg Config) *Store {
	return &Store{cfg: cfg, entries: map[key]*Entry{}, sites: map[string]*siteIndex{}}
}

// Now 返回存储当前时刻。
func (s *Store) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Stats 返回当前统计快照。
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Len 返回当前条目总数。
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// SiteLen 返回某站点当前条目数。
func (s *Store) SiteLen(site string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if si := s.sites[site]; si != nil {
		return len(si.items)
	}
	return 0
}

// AdvanceClock 推进内部时钟；回退（负增量）被拒绝且不改变任何状态。
func (s *Store) AdvanceClock(d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d < 0 {
		return ErrClockRegression
	}
	s.now = s.now.Add(d)
	return nil
}

// SetInput 是一次结构化写入的输入。
type SetInput struct {
	Name         string
	Value        string
	Site         string
	Path         string
	Secure       bool
	HTTPOnly     bool
	SameSite     SameSite
	ExpiresAt    *time.Time // nil 表示会话级
	PartitionKey *string    // nil 表示未分区
	SourceSite   string     // 写入来源站点
	SourceSecure bool       // 写入来源是否安全
	At           *time.Time // 可选的事件时刻，早于当前时刻即时钟回退
}

// Set 写入一条条目。同键（名字+站点+路径+分区键）已存在时为覆盖：
// 保留原创建时刻，更新其余字段与最近访问时刻，且不触发淘汰。
// 过期时刻不晚于当前时刻的写入视为删除已有同键条目（计入显式删除）。
// 被拒绝时不改变任何条目、统计与时钟。
func (s *Store) Set(in SetInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if in.Name == "" || in.Site == "" || !strings.HasPrefix(in.Path, "/") || !in.SameSite.valid() {
		return ErrInvalidArgument
	}
	if in.At != nil && in.At.Before(s.now) {
		return ErrClockRegression
	}
	if strings.HasPrefix(in.Name, hostPrefix) && !(in.Secure && in.Path == "/" && in.Site == in.SourceSite) {
		return ErrHostPrefixViolation
	}
	if in.Secure && !in.SourceSecure {
		return ErrInsecureSource
	}
	if in.SameSite == SameSiteNone && !in.Secure {
		return ErrSameSiteNoneInsecure
	}

	k := makeKey(in.Name, in.Site, in.Path, in.PartitionKey)

	if in.ExpiresAt != nil && !in.ExpiresAt.After(s.now) {
		if _, ok := s.entries[k]; ok {
			s.removeLocked(k, &s.stats.ExplicitDeletes)
		}
		return nil
	}

	if e, ok := s.entries[k]; ok {
		e.Value = in.Value
		e.Secure = in.Secure
		e.HTTPOnly = in.HTTPOnly
		e.SameSite = in.SameSite
		e.ExpiresAt = in.ExpiresAt
		e.LastAccessedAt = s.now
		e.gen++
		si := s.sites[in.Site]
		s.pushLRU(si, e)
		s.pushExpiry(si, e)
		return nil
	}

	s.seq++
	e := &Entry{
		Name:           in.Name,
		Value:          in.Value,
		Site:           in.Site,
		Path:           in.Path,
		Secure:         in.Secure,
		HTTPOnly:       in.HTTPOnly,
		SameSite:       in.SameSite,
		ExpiresAt:      in.ExpiresAt,
		PartitionKey:   in.PartitionKey,
		CreatedAt:      s.now,
		LastAccessedAt: s.now,
		seq:            s.seq,
		gen:            1,
	}
	si := s.siteOf(in.Site)
	s.entries[k] = e
	si.items[k] = e
	s.pushLRU(si, e)
	s.pushExpiry(si, e)
	s.bumpSite(si)
	s.stats.TotalAdds++
	s.enforceCapacity(in.Site)
	return nil
}

// DebugEntries 返回全部存活条目的拷贝（含尚未被观察到的过期条目），
// 按键排序，仅供测试与调试，不参与过期清扫语义。
func (s *Store) DebugEntries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return entryLess(out[i], out[j]) })
	return out
}

func entryLess(a, b Entry) bool {
	if a.Site != b.Site {
		return a.Site < b.Site
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if (a.PartitionKey == nil) != (b.PartitionKey == nil) {
		return a.PartitionKey == nil
	}
	if a.PartitionKey != nil && *a.PartitionKey != *b.PartitionKey {
		return *a.PartitionKey < *b.PartitionKey
	}
	return a.seq < b.seq
}
