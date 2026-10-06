// Package cookie 实现浏览器 Cookie 存储内核：结构化条目写入、
// 请求附带判定、同站规则、过期处理与按站点/全局容量淘汰。
package cookie

import (
	"strings"
	"time"
)

// SameSite 为条目的同站模式。SameSiteUnspecified 表示写入时未显式声明。
type SameSite int

const (
	SameSiteUnspecified SameSite = iota
	SameSiteStrict
	SameSiteLax
	SameSiteNone
)

func (m SameSite) valid() bool {
	return m >= SameSiteUnspecified && m <= SameSiteNone
}

func (m SameSite) String() string {
	switch m {
	case SameSiteStrict:
		return "Strict"
	case SameSiteLax:
		return "Lax"
	case SameSiteNone:
		return "None"
	default:
		return "Unspecified"
	}
}

// hostPrefix 是仅主机前缀约定（__Host-）。
const hostPrefix = "__Host-"

// key 是条目的唯一键：名字 + 所属站点 + 路径 + 分区键。
type key struct {
	name         string
	site         string
	path         string
	partition    string
	hasPartition bool
}

func makeKey(name, site, path string, partition *string) key {
	k := key{name: name, site: site, path: path}
	if partition != nil {
		k.partition = *partition
		k.hasPartition = true
	}
	return k
}

// Entry 是一条 Cookie 存储条目。ExpiresAt 为 nil 表示会话级；
// PartitionKey 为 nil 表示未分区。
type Entry struct {
	Name           string
	Value          string
	Site           string
	Path           string
	Secure         bool
	HTTPOnly       bool
	SameSite       SameSite
	ExpiresAt      *time.Time
	PartitionKey   *string
	CreatedAt      time.Time
	LastAccessedAt time.Time

	seq uint64 // 全局递增序号，完全并列时的确定性强破
	gen uint64 // 代际号，惰性堆失效判定用
}

func (e *Entry) key() key {
	return makeKey(e.Name, e.Site, e.Path, e.PartitionKey)
}

// expired 按时刻比较：过期时刻恰等于当前时刻即视为已过期。
func (e *Entry) expired(now time.Time) bool {
	return e.ExpiresAt != nil && !e.ExpiresAt.After(now)
}

// pathMatch 实现 RFC 6265 路径匹配的整段边界规则：
// 完全一致、cookie 路径以 "/" 结尾的前缀、或目标路径在边界处为 "/"。
func pathMatch(cookiePath, reqPath string) bool {
	if cookiePath == reqPath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}
	if strings.HasSuffix(cookiePath, "/") {
		return true
	}
	return reqPath[len(cookiePath)] == '/'
}

// partitionEqual 判定分区键相等：均为空（nil）也算相等。
func partitionEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
