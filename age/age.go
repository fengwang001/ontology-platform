// Package age 计算缓存项的当前年龄与新鲜期。
//
// 时间一律来自注入的 Clock，包内不直接调用 time.Now。
package age

import (
	"errors"
	"time"

	"ontology/directive"
)

// Clock 提供当前时刻，由调用方注入。
type Clock interface {
	Now() time.Time
}

var (
	// ErrInconsistentTime 时刻不自洽：tResp 早于 tReq。
	ErrInconsistentTime = errors.New("inconsistent time: response arrived before request")
	// ErrNoDateAndDirective 指令文本为空且响应无 Date，无法判定新鲜期。
	ErrNoDateAndDirective = errors.New("empty cache directives and missing Date header")
)

// Entry 描述一条缓存响应的时序与新鲜度头信息。
type Entry struct {
	TReq       time.Time     // 请求发出时刻
	TResp      time.Time     // 响应到达时刻
	TDate      time.Time     // 响应 Date 时刻；IsZero 表示缺省
	AgeHdr     int64         // Age 头秒数；缺省传 0 并置 HasAgeHdr=false
	HasAgeHdr  bool          // Age 头是否存在
	Response   directive.Set // 响应缓存指令
	Expires    time.Time     // Expires 绝对时刻；IsZero 表示缺省
	HasExpires bool          // Expires 头是否存在
}

func nonneg(d time.Duration) int64 {
	s := int64(d.Seconds())
	if s < 0 {
		return 0
	}
	return s
}

// CurrentAge 按固定顺序计算年龄，每一步负数都截到 0。
func CurrentAge(e Entry, now time.Time) (int64, error) {
	if e.TResp.Before(e.TReq) {
		return 0, ErrInconsistentTime
	}
	apparent := int64(0)
	if !e.TDate.IsZero() {
		apparent = nonneg(e.TResp.Sub(e.TDate))
	}
	corrected := apparent
	if e.HasAgeHdr && e.AgeHdr > corrected {
		corrected = e.AgeHdr
	}
	network := nonneg(e.TResp.Sub(e.TReq))
	resident := nonneg(now.Sub(e.TResp))
	return corrected + network + resident, nil
}

// Freshness 返回新鲜期秒数及采用的来源名（s-maxage/max-age/expires/none）。
func Freshness(e Entry) (int64, string, error) {
	if d, ok := e.Response.Get("s-maxage"); ok && d.HasDelta {
		return d.Delta, "s-maxage", nil
	}
	if d, ok := e.Response.Get("max-age"); ok && d.HasDelta {
		return d.Delta, "max-age", nil
	}
	if e.HasExpires {
		if e.TDate.IsZero() {
			return 0, "expires", ErrNoDateAndDirective
		}
		return nonneg(e.Expires.Sub(e.TDate)), "expires", nil
	}
	if len(e.Response) == 0 && e.TDate.IsZero() {
		return 0, "none", ErrNoDateAndDirective
	}
	return 0, "none", nil
}
