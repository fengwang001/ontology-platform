package cookie

import (
	"sort"
	"strings"
)

// Request 是一次请求附带判定的输入。
type Request struct {
	Site          string  // 目标站点
	Path          string  // 目标路径
	Secure        bool    // 是否安全请求
	InitiatorSite string  // 发起者站点
	TopLevelNav   bool    // 是否顶层导航
	SafeMethod    bool    // 是否安全方法
	PartitionKey  *string // 请求分区键
}

// Attach 判定本次请求附带哪些条目。附带成功的条目按
// 路径长者先、同长按创建时刻早者先排列，且其最近访问时刻
// 被更新为当前时刻；被规则拒绝的不更新。扫描范围仅限目标站点，
// 开销与无关站点的条目总数无关。
func (s *Store) Attach(r Request) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Site == "" || !strings.HasPrefix(r.Path, "/") {
		return nil, ErrInvalidArgument
	}
	matched := s.scanSite(r.Site, s.requestFilter(r))
	sortAttachments(matched)
	out := make([]Entry, 0, len(matched))
	for _, e := range matched {
		e.LastAccessedAt = s.now
		e.gen++
		si := s.sites[e.Site]
		s.pushLRU(si, e)
		s.pushExpiry(si, e)
		out = append(out, *e)
	}
	return out, nil
}

// requestFilter 返回针对该请求的条目判定函数。
func (s *Store) requestFilter(r Request) func(*Entry) bool {
	return func(e *Entry) bool {
		if !pathMatch(e.Path, r.Path) {
			return false
		}
		if e.Secure && !r.Secure {
			return false
		}
		if !partitionEqual(e.PartitionKey, r.PartitionKey) {
			return false
		}
		return s.sameSiteOK(e, r)
	}
}

// sameSiteOK 叠加同站规则。未显式声明的条目按宽松处理，
// 并额外允许创建后宽限时长内（严格小于）附带到非安全方法的顶层跨站请求。
func (s *Store) sameSiteOK(e *Entry, r Request) bool {
	sameSite := r.InitiatorSite == r.Site
	switch e.SameSite {
	case SameSiteStrict:
		return sameSite
	case SameSiteNone:
		return true
	case SameSiteLax:
		return sameSite || (r.TopLevelNav && r.SafeMethod)
	default: // SameSiteUnspecified
		if sameSite || (r.TopLevelNav && r.SafeMethod) {
			return true
		}
		return r.TopLevelNav && !r.SafeMethod && s.now.Sub(e.CreatedAt) < s.cfg.LaxGrace
	}
}

// ScriptRead 是脚本读取接口：仅 HTTP 条目不可见，
// 其余按站点、路径、安全、分区与过期过滤；不更新最近访问时刻。
func (s *Store) ScriptRead(site, path string, secure bool, partition *string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if site == "" || !strings.HasPrefix(path, "/") {
		return nil, ErrInvalidArgument
	}
	matched := s.scanSite(site, func(e *Entry) bool {
		if e.HTTPOnly {
			return false
		}
		if !pathMatch(e.Path, path) {
			return false
		}
		if e.Secure && !secure {
			return false
		}
		return partitionEqual(e.PartitionKey, partition)
	})
	sortAttachments(matched)
	out := make([]Entry, 0, len(matched))
	for _, e := range matched {
		out = append(out, *e)
	}
	return out, nil
}

// scanSite 遍历目标站点的条目：观察到的过期条目立即移除并计过期淘汰，
// 其余经 match 判定后收集。只访问目标站点的索引。
func (s *Store) scanSite(site string, match func(*Entry) bool) []*Entry {
	si := s.sites[site]
	if si == nil {
		return nil
	}
	var matched []*Entry
	for k, e := range si.items {
		s.attachScans++
		if e.expired(s.now) {
			s.removeLocked(k, &s.stats.ExpiredEvictions)
			continue
		}
		if match(e) {
			matched = append(matched, e)
		}
	}
	return matched
}

// sortAttachments 按路径长者先、同长创建时刻早者先、再按序号排序。
func sortAttachments(es []*Entry) {
	sort.Slice(es, func(i, j int) bool {
		if len(es[i].Path) != len(es[j].Path) {
			return len(es[i].Path) > len(es[j].Path)
		}
		if !es[i].CreatedAt.Equal(es[j].CreatedAt) {
			return es[i].CreatedAt.Before(es[j].CreatedAt)
		}
		return es[i].seq < es[j].seq
	})
}
