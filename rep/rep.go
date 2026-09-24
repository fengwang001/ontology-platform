// Package rep 定义单副本条目、版本比较与 winner 判定。不依赖其他包。
package rep

// Slot 是一个副本对某 key 持有的条目；Ok=false 表示空副本（无条目）。
type Slot struct {
	Value string
	Ver   int64
	Ok    bool
}

// Beats 报告 s 是否胜过 o（版本更大；版本并列时 value 字典序更大）。仅对 Ok 的条目有意义。
func (s Slot) Beats(o Slot) bool {
	return s.Ver > o.Ver || (s.Ver == o.Ver && s.Value > o.Value)
}

// Winner 朴素参照：扫描全部副本，取最大版本，并列取字典序更大的 value。
// 全部为空时返回 found=false。
func Winner(slots []Slot) (w Slot, found bool) {
	for _, s := range slots {
		if !s.Ok {
			continue
		}
		if !found || s.Beats(w) {
			w, found = s, true
		}
	}
	return w, found
}

// Status 是副本条目相对 winner 的分类。
type Status int

const (
	Current  Status = iota // 与 winner 一致
	Empty                  // 空副本
	Stale                  // 版本落后
	Conflict               // 版本相同但值不同
)

// Classify 将副本条目 s 相对 winner w 分类（调用方保证 w.Ok）。
func Classify(s, w Slot) Status {
	switch {
	case !s.Ok:
		return Empty
	case s.Ver < w.Ver:
		return Stale
	case s.Value != w.Value:
		return Conflict
	default:
		return Current
	}
}
