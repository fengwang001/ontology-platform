// Package evt 定义事件及其序关系：先按 TS 升序，TS 相同按 Key 字典序升序。
// 本包不依赖其他包。
package evt

// Event 是一个事件出现，由 (Key, TS) 标识。TS 可为负。
type Event struct {
	Key string
	TS  int64
}

// Compare 返回 -1/0/1：a 排在 b 前为 -1，相同为 0，否则为 1。
func Compare(a, b Event) int {
	if a.TS != b.TS {
		if a.TS < b.TS {
			return -1
		}
		return 1
	}
	if a.Key != b.Key {
		if a.Key < b.Key {
			return -1
		}
		return 1
	}
	return 0
}

// Less 报告 a 是否应排在 b 前面（TS 升序，同 TS 时 Key 升序）。
func Less(a, b Event) bool { return Compare(a, b) < 0 }

// Equal 报告两个事件的 (Key, TS) 是否完全相同。
func Equal(a, b Event) bool { return Compare(a, b) == 0 }
