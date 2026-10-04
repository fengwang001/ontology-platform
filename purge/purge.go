// Package purge 实现刷新路径语法、规则 trie 与批规范化。
package purge

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

const (
	maxPathBytes = 256
	maxSegments  = 16
)

var (
	// ErrInvalidPath 路径非法（语法/长度/段数）。
	ErrInvalidPath = errors.New("purge: invalid path")
	// ErrEmptyBatch items 为空或超过 100。
	ErrEmptyBatch = errors.New("purge: empty batch")
)

// Kind 区分 URL 与目录。
type Kind int

const (
	KindURL Kind = iota
	KindDir
)

// Path 为解析后的路径。
type Path struct {
	Raw  string
	Kind Kind
	Segs []string // 目录为 "/" 时为空
}

// Parse 校验并解析路径。
func Parse(raw string) (Path, error) {
	if len(raw) == 0 || raw[0] != '/' || len(raw) > maxPathBytes {
		return Path{}, ErrInvalidPath
	}
	if raw == "/" {
		return Path{Raw: raw, Kind: KindDir}, nil
	}
	isDir := raw[len(raw)-1] == '/'
	body := raw
	if isDir {
		body = raw[:len(raw)-1]
	}
	segStrs := strings.Split(body[1:], "/")
	if len(segStrs) > maxSegments {
		return Path{}, ErrInvalidPath
	}
	segs := make([]string, len(segStrs))
	for i, seg := range segStrs {
		if seg == "" || seg == "." || seg == ".." {
			return Path{}, ErrInvalidPath
		}
		segs[i] = seg
	}
	kind := KindURL
	if isDir {
		kind = KindDir
	}
	return Path{Raw: raw, Kind: kind, Segs: segs}, nil
}

// Normalize 对一批原始路径去重、去目录覆盖，返回计费 URL/目录数与保留项。
func Normalize(items []string) (kept []Path, u, d int, err error) {
	if len(items) < 1 || len(items) > 100 {
		return nil, 0, 0, ErrEmptyBatch
	}
	parsed := make([]Path, 0, len(items))
	seen := map[string]struct{}{}
	for _, it := range items {
		p, perr := Parse(it)
		if perr != nil {
			return nil, 0, 0, perr
		}
		if _, dup := seen[p.Raw]; dup {
			continue
		}
		seen[p.Raw] = struct{}{}
		parsed = append(parsed, p)
	}
	sort.SliceStable(parsed, func(i, j int) bool {
		if len(parsed[i].Segs) != len(parsed[j].Segs) {
			return len(parsed[i].Segs) < len(parsed[j].Segs)
		}
		if parsed[i].Kind != parsed[j].Kind {
			return parsed[i].Kind == KindDir // 目录先于 URL
		}
		return parsed[i].Raw < parsed[j].Raw
	})
	dirs := make([]Path, 0)
	for _, p := range parsed {
		covered := false
		for _, dir := range dirs {
			if covers(dir, p) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		kept = append(kept, p)
		if p.Kind == KindDir {
			dirs = append(dirs, p)
			d++
		} else {
			u++
		}
	}
	return kept, u, d, nil
}

// covers 判断目录 dir 是否覆盖路径 p（目录不覆盖同名 URL，根目录覆盖除自身外全部）。
func covers(dir, p Path) bool {
	if len(dir.Segs) == 0 {
		return len(p.Segs) > 0 // 根目录 "/" 不覆盖自身
	}
	if len(p.Segs) <= len(dir.Segs) {
		return false
	}
	for i, seg := range dir.Segs {
		if p.Segs[i] != seg {
			return false
		}
	}
	return true
}

// node 为规则 trie 节点。目录规则纪元存在节点上。
type node struct {
	children map[string]*node
	dirEpoch int64 // 以该节点结尾的目录规则纪元；0 表示无规则
	urlEpoch int64 // 与该节点路径完全相同的 URL 规则纪元
}

func newNode() *node {
	return &node{children: map[string]*node{}}
}

// Store 为每租户一棵规则 trie，并发安全。
type Store struct {
	mu    sync.RWMutex
	roots map[string]*node
}

// NewStore 创建空规则存储。
func NewStore() *Store {
	return &Store{roots: map[string]*node{}}
}

// Put 为租户写入一批规则纪元（同路径保留较大纪元）。
func (s *Store) Put(tenant string, kept []Path, epoch int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, ok := s.roots[tenant]
	if !ok {
		root = newNode()
		s.roots[tenant] = root
	}
	for _, p := range kept {
		n := root
		for _, seg := range p.Segs {
			child := n.children[seg]
			if child == nil {
				child = newNode()
				n.children[seg] = child
			}
			n = child
		}
		if p.Kind == KindDir {
			if epoch > n.dirEpoch {
				n.dirEpoch = epoch
			}
		} else if epoch > n.urlEpoch {
			n.urlEpoch = epoch
		}
	}
}

// MaxEpoch 返回覆盖某 URL 的全部规则中的最大纪元：
// 完全相同 URL 规则，或任一为其前缀目录的目录规则。
func (s *Store) MaxEpoch(tenant, rawURL string) (int64, error) {
	epoch, _, err := s.Lookup(tenant, rawURL)
	return epoch, err
}

// Lookup 同 MaxEpoch，额外返回本次查询实际访问的 trie 节点数（供复杂度断言）。
// 访问节点数 = 1（根）+ 实际下行的边数，不超过该 URL 段数 + 1。
func (s *Store) Lookup(tenant, rawURL string) (maxEpoch int64, visited int, err error) {
	p, err := Parse(rawURL)
	if err != nil {
		return 0, 0, err
	}
	if p.Kind != KindURL {
		return 0, 0, ErrInvalidPath
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.roots[tenant]
	if !ok {
		return 0, 0, nil
	}
	visited = 1
	// 根目录规则 "/" 覆盖所有非根 URL。
	if n.dirEpoch > maxEpoch {
		maxEpoch = n.dirEpoch
	}
	for i := 0; i < len(p.Segs); i++ {
		child := n.children[p.Segs[i]]
		if child == nil {
			break
		}
		visited++
		n = child
		// 中继节点：以该位置结尾的目录规则覆盖当前 URL。
		// 末节点是 URL 自身，其 dirEpoch（目录 /x/ 的规则）不覆盖同名 URL /x，
		// 故只在非末节点取目录纪元；末节点只取完全相同的 URL 纪元。
		if i < len(p.Segs)-1 {
			if n.dirEpoch > maxEpoch {
				maxEpoch = n.dirEpoch
			}
		} else if n.urlEpoch > maxEpoch {
			maxEpoch = n.urlEpoch
		}
	}
	return maxEpoch, visited, nil
}
