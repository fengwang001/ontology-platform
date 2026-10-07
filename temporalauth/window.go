package temporalauth

// WindowRule 是以“对象所属地区默认时区”表述的时间窗口规则。
// Start/End 均为该地区默认时区下带日期的民用墙钟时间，自然允许跨越午夜
// （End 的日历日晚于 Start）以及跨越夏令时切换当天。
//
// 归一化后采用半开区间 [start, end)：Start 边界归入窗口内，End 边界归入
// 窗口外。该约定在普通日期与夏令时切换当天完全相同，不设特例。
type WindowRule struct {
	RegionID string
	Start    Civil
	End      Civil
}

// Validate 校验窗口规则：要求 End 严格晚于 Start（窗口长度为正）。
// 校验只依据墙钟字段的日历顺序，不依赖任何具体时区。
func (w WindowRule) Validate() error {
	if civilToSeconds(w.End) <= civilToSeconds(w.Start) {
		return &AuthError{Code: ErrWindowRuleInvalid}
	}
	return nil
}

// WindowVersion 是某“对象类型 + 属性”窗口规则的一个不可变版本。
type WindowVersion struct {
	ValidFrom Instant
	Rule      WindowRule
}

// WindowChain 是窗口规则的版本链（按 ValidFrom 严格升序）。
type WindowChain struct {
	Versions []WindowVersion
}

// RuleAt 返回时刻 t 生效的窗口规则与版本下标（二分，O(log n)）。
func (wc *WindowChain) RuleAt(t Instant) (WindowRule, int, bool) {
	n := len(wc.Versions)
	if n == 0 || t < wc.Versions[0].ValidFrom {
		return WindowRule{}, 0, false
	}
	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if wc.Versions[mid].ValidFrom <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	idx := lo - 1
	return wc.Versions[idx].Rule, idx, true
}

// ResolveBounds 使用给定地区默认时区规则，把窗口边界归一化为绝对时刻。
// gap/overlap 边界与其它时刻走完全相同的 CivilToInstant 约定。
func ResolveBounds(rule WindowRule, zone *ZoneRules) (start, end Instant) {
	return zone.CivilToInstant(rule.Start), zone.CivilToInstant(rule.End)
}

// WithinWindow 判定绝对时刻 t 是否落在半开区间 [start, end) 内。
func WithinWindow(t, start, end Instant) bool {
	return t >= start && t < end
}
