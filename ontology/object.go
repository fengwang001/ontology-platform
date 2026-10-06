package ontology

import (
	"strings"
)

// ObjType 是对象类型：提交、标签、树、文件之一。
type ObjType int

const (
	TypeAny ObjType = iota // 仅用于查询约束，不是真实对象类型
	TypeCommit
	TypeTag
	TypeTree
	TypeBlob
)

func (t ObjType) String() string {
	switch t {
	case TypeCommit:
		return "commit"
	case TypeTag:
		return "tag"
	case TypeTree:
		return "tree"
	case TypeBlob:
		return "blob"
	default:
		return "any"
	}
}

// idLen 是对象标识的固定长度（小写十六进制字符数）。
const idLen = 40

// Object 是对象库中的对象。字段按类型解释：
// 提交使用 Parents 与 Tree；标签使用 Target；树与文件无额外字段。
type Object struct {
	ID      string
	Type    ObjType
	Parents []string // 仅提交：父提交标识，有序
	Tree    string   // 仅提交：树标识
	Target  string   // 仅标签：指向的对象标识（可再为标签）
}

// isHex 报告 s 是否为非空小写十六进制串。
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// bucketKey 把 4 位十六进制字符映射为桶号 [0, 65536)。
func bucketKey(h4 string) int {
	v := 0
	for i := 0; i < 4; i++ {
		c := h4[i]
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= int(c - '0')
		default:
			v |= int(c-'a') + 10
		}
	}
	return v
}

func lcpLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// lowerBound 返回有序切片中首个 >= target 的下标，以及比较次数（探针数）。
func lowerBound(ids []string, target string) (int, int) {
	lo, hi := 0, len(ids)
	probes := 0
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		probes++
		if ids[mid] < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, probes
}

// objectStore 是只增对象库：全量 map + 按标识前 4 位分桶的有序索引。
// 前缀匹配与最短缩写的代价为 O(log 桶大小 + 命中数)，与对象总数无关。
type objectStore struct {
	objs    map[string]Object
	buckets [1 << 16][]string // 每桶内标识有序
}

func newObjectStore() *objectStore {
	return &objectStore{objs: make(map[string]Object)}
}

func (s *objectStore) get(id string) (Object, bool) {
	o, ok := s.objs[id]
	return o, ok
}

// put 追加对象；调用方保证标识不重复且引用完整。
func (s *objectStore) put(o Object) {
	s.objs[o.ID] = o
	b := bucketKey(o.ID[:4])
	ids := s.buckets[b]
	i, _ := lowerBound(ids, o.ID)
	ids = append(ids, "")
	copy(ids[i+1:], ids[i:])
	ids[i] = o.ID
	s.buckets[b] = ids
}

// prefixCount 统计以 p 为前缀的对象个数，返回命中数与探针数。
func (s *objectStore) prefixCount(p string) (int, int) {
	if len(p) >= 4 {
		ids := s.buckets[bucketKey(p[:4])]
		i, probes := lowerBound(ids, p)
		n := 0
		for i+n < len(ids) && strings.HasPrefix(ids[i+n], p) {
			n++
			probes++
		}
		return n, probes
	}
	lo := bucketKey(p + strings.Repeat("0", 4-len(p)))
	hi := bucketKey(p + strings.Repeat("f", 4-len(p)))
	n, probes := 0, 0
	for b := lo; b <= hi; b++ {
		probes++
		n += len(s.buckets[b])
	}
	return n, probes
}

// prefixFirst 返回按字典序第一个以 p 为前缀的标识。
func (s *objectStore) prefixFirst(p string) (string, bool) {
	if len(p) >= 4 {
		ids := s.buckets[bucketKey(p[:4])]
		i, _ := lowerBound(ids, p)
		if i < len(ids) && strings.HasPrefix(ids[i], p) {
			return ids[i], true
		}
		return "", false
	}
	lo := bucketKey(p + strings.Repeat("0", 4-len(p)))
	hi := bucketKey(p + strings.Repeat("f", 4-len(p)))
	for b := lo; b <= hi; b++ {
		if len(s.buckets[b]) > 0 {
			return s.buckets[b][0], true
		}
	}
	return "", false
}

// shortestAbbrev 返回 id 在当前库中唯一的最短前缀（不短于 minLen）。
// 唯一性只需与有序序列中的前驱、后继比较 LCP。
func (s *objectStore) shortestAbbrev(id string, minLen int) (string, int, bool) {
	probes := 0
	b := bucketKey(id[:4])
	ids := s.buckets[b]
	i, pr := lowerBound(ids, id)
	probes += pr
	if i >= len(ids) || ids[i] != id {
		return "", probes, false
	}
	lcp := 0
	if i > 0 {
		probes++
		lcp = lcpLen(id, ids[i-1])
	} else {
		for bb := b - 1; bb >= 0; bb-- {
			probes++
			if len(s.buckets[bb]) > 0 {
				lcp = lcpLen(id, s.buckets[bb][len(s.buckets[bb])-1])
				break
			}
		}
	}
	if i+1 < len(ids) {
		probes++
		if n := lcpLen(id, ids[i+1]); n > lcp {
			lcp = n
		}
	} else {
		for bb := b + 1; bb < 1<<16; bb++ {
			probes++
			if len(s.buckets[bb]) > 0 {
				if n := lcpLen(id, s.buckets[bb][0]); n > lcp {
					lcp = n
				}
				break
			}
		}
	}
	n := lcp + 1
	if n < minLen {
		n = minLen
	}
	if n > idLen {
		n = idLen
	}
	return id[:n], probes, true
}
