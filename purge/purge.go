// Package purge 实现路径校验、刷新批规范化、按租户的刷新规则与全局纪元。
//
// Store 不自持锁：每个操作都在 quota.Meter 的锁临界区内执行。
package purge

import (
	"errors"
	"strings"

	"ontology/quota"
)

// ErrInvalidArgument 表示 Purge 的参数非法（now 越界、批大小越界或路径非法）。
var ErrInvalidArgument = errors.New("purge: invalid argument")

const (
	// MaxPathBytes 是路径总长的上限（字节）。
	MaxPathBytes = 256
	// MaxSegments 是路径段数的上限。
	MaxSegments = 16
	// MaxItems 是单批刷新项数的上限。
	MaxItems = 100
)

// IsDir 报告路径是否为目录（以 / 结尾；单独一个 / 表示根目录）。
func IsDir(p string) bool { return strings.HasSuffix(p, "/") }

// ValidPath 报告路径是否合法：以 / 开头，段之间以 / 分隔，段非空且
// 不为 . 或 ..，总长不超过 256 字节，段数不超过 16。
func ValidPath(p string) bool {
	if len(p) == 0 || len(p) > MaxPathBytes || p[0] != '/' {
		return false
	}
	body := strings.TrimSuffix(p, "/")
	if body == "" {
		return true // 根目录 "/"
	}
	segs := strings.Split(body[1:], "/")
	if len(segs) > MaxSegments {
		return false
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return false
		}
	}
	return true
}

// Normalize 批内规范化：完全相同的项只保留一次；被同批某个目录覆盖
// （该目录是其真前缀）的 URL 与目录被丢弃，不计费也不成为规则。
// 目录只可能被更外层的目录覆盖，URL 不覆盖任何项。
func Normalize(items []string) (urls, dirs []string) {
	seen := make(map[string]struct{}, len(items))
	uniq := make([]string, 0, len(items))
	for _, it := range items {
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		uniq = append(uniq, it)
	}
	var batchDirs []string
	for _, it := range uniq {
		if IsDir(it) {
			batchDirs = append(batchDirs, it)
		}
	}
	for _, it := range uniq {
		covered := false
		for _, d := range batchDirs {
			if d != it && strings.HasPrefix(it, d) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		if IsDir(it) {
			dirs = append(dirs, it)
		} else {
			urls = append(urls, it)
		}
	}
	return urls, dirs
}

type rules struct {
	urls map[string]uint64
	dirs map[string]uint64
}

// Store 保存各租户的刷新规则与全局刷新纪元。
type Store struct {
	meter   *quota.Meter
	epoch   uint64
	tenants map[string]*rules
	visited int // Fresh 一次查询触碰的规则节点数（复杂度不变量证据）
}

// NewStore 返回挂在计量器 m 上的规则存储。
func NewStore(m *quota.Meter) *Store {
	return &Store{meter: m, tenants: make(map[string]*rules)}
}

// Epoch 返回当前全局刷新纪元（尚无被接受的 Purge 时为 0）。
func (s *Store) Epoch() uint64 { return s.epoch }

// LastVisited 返回最近一次 MatchEpoch 触碰的规则节点数。
func (s *Store) LastVisited() int { return s.visited }

// Purge 校验并规范化一批刷新项；计费通过（全有或全无）后全局纪元加 1，
// 规范化后的每一项成为该租户的一条规则，同一路径只保留较大纪元。
func (s *Store) Purge(now int64, tenant string, items []string) (uint64, error) {
	if !quota.ValidNow(now) || len(items) == 0 || len(items) > MaxItems {
		return 0, ErrInvalidArgument
	}
	for _, it := range items {
		if !ValidPath(it) {
			return 0, ErrInvalidArgument
		}
	}
	s.meter.Lock()
	defer s.meter.Unlock()
	urls, dirs := Normalize(items)
	if err := s.meter.ChargePurge(now, tenant, uint64(len(urls)), uint64(len(dirs))); err != nil {
		return 0, err
	}
	s.epoch++
	r := s.tenants[tenant]
	if r == nil {
		r = &rules{urls: make(map[string]uint64), dirs: make(map[string]uint64)}
		s.tenants[tenant] = r
	}
	for _, u := range urls {
		r.urls[u] = s.epoch // 纪元单调递增，覆盖写即保留较大纪元
	}
	for _, d := range dirs {
		r.dirs[d] = s.epoch
	}
	return s.epoch, nil
}

// MatchEpoch 返回匹配 url 的所有规则（路径完全相同的 URL 规则，或任一
// 覆盖它的目录规则）的最大纪元，并把触碰的规则节点数记入 visited。
// 候选键只有：精确 URL、根目录、以及 url 的各段前缀目录，共 段数+1 个，
// 与该租户的规则总数无关。
func (s *Store) MatchEpoch(tenant, url string) uint64 {
	s.visited = 0
	r := s.tenants[tenant]
	if r == nil {
		return 0
	}
	var max uint64
	s.visited++
	if e, ok := r.urls[url]; ok && e > max {
		max = e
	}
	s.visited++
	if e, ok := r.dirs["/"]; ok && e > max {
		max = e
	}
	rest := url[1:] // url 必为合法 URL（以 / 开头、不以 / 结尾）
	for i := 0; i < len(rest); i++ {
		if rest[i] != '/' {
			continue
		}
		s.visited++
		if e, ok := r.dirs["/"+rest[:i]+"/"]; ok && e > max {
			max = e
		}
	}
	return max
}
