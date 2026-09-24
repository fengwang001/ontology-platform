// Package precond 实现 HTTP 条件请求的前置判定：给定资源当前的实体标签、
// 最后修改时刻与请求的条件头部，裁决「继续执行 / 未修改 / 前置条件失败」。
//
// 求值顺序（RFC 7232 §6）：If-Match 优先于 If-Unmodified-Since，
// If-None-Match 优先于 If-Modified-Since，且前一组整体先于后一组。
// 解析失败的条件头部按「该头部不存在」处理，判定本身永不报错。
// 时间只来自注入的时钟 now；没有任何条件头部时不会调用 now。
package precond

import (
	"time"

	"ontology/etag"
	"ontology/httpdate"
)

// Result 是判定的三种互斥结果。
type Result int

const (
	Continue    Result = iota // 继续执行
	NotModified               // 返回未修改（304）
	Failed                    // 前置条件失败（412）
)

func (r Result) String() string {
	return [...]string{"Continue", "NotModified", "Failed"}[r]
}

// Resource 是资源的当前状态。Exists 为 false 时其余字段无意义。
type Resource struct {
	Exists  bool
	ETag    string // 原始实体标签，如 `"a"` 或 `W/"a"`
	LastMod time.Time
}

// Request 是请求携带的条件头部（空串表示缺失）与方法。
type Request struct {
	Method            string
	IfMatch           string
	IfNoneMatch       string
	IfModifiedSince   string
	IfUnmodifiedSince string
}

// Evaluate 判定一次请求。now 为注入时钟，仅在真正求值时间条件、且需要
// 排除未来日期时调用。Evaluate 不修改任何传入结构，结果可复现。
func Evaluate(res Resource, req Request, now func() time.Time) Result {
	if req.IfMatch != "" {
		if tags, star, err := etag.ParseList(req.IfMatch); err == nil {
			if !anyMatch(res, tags, star, etag.StrongEqual) {
				return Failed
			}
		} else if !evalUnmodifiedSince(res, req, now) {
			return Failed
		}
	} else if !evalUnmodifiedSince(res, req, now) {
		return Failed
	}
	if req.IfNoneMatch != "" {
		if tags, star, err := etag.ParseList(req.IfNoneMatch); err == nil {
			if anyMatch(res, tags, star, etag.WeakEqual) {
				if req.Method == "GET" || req.Method == "HEAD" {
					return NotModified
				}
				return Failed
			}
			return Continue // If-None-Match 存在且不匹配：忽略 If-Modified-Since
		}
	}
	if req.IfModifiedSince != "" {
		if t, err := httpdate.Parse(req.IfModifiedSince); err == nil && !future(t, now) {
			if !res.Exists || httpdate.Compare(res.LastMod, t) <= 0 {
				return NotModified
			}
		}
	}
	return Continue
}

// evalUnmodifiedSince 返回是否通过；头部缺失或解析失败视为通过。
func evalUnmodifiedSince(res Resource, req Request, now func() time.Time) bool {
	if req.IfUnmodifiedSince == "" {
		return true
	}
	t, err := httpdate.Parse(req.IfUnmodifiedSince)
	if err != nil || future(t, now) {
		return true
	}
	return !res.Exists || httpdate.Compare(res.LastMod, t) <= 0
}

// anyMatch 处理 "*" 与列表任一匹配；资源不存在时只有非星号才可能失败。
func anyMatch(res Resource, tags []etag.Tag, star bool, eq func(etag.Tag, etag.Tag) bool) bool {
	if star {
		return res.Exists
	}
	if !res.Exists {
		return false
	}
	cur, err := etag.Parse(res.ETag)
	if err != nil {
		return false
	}
	for _, t := range tags {
		if eq(t, cur) {
			return true
		}
	}
	return false
}

// future 判定日期是否晚于当前时刻；只有走到这里才调用注入时钟。
func future(t time.Time, now func() time.Time) bool {
	return httpdate.Compare(t, now()) > 0
}
