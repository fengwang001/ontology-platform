// Package delta 定义增量同步的 Change/Delta 类型与纯函数式的单条变更应用。
package delta

// Kind 区分 Change 的类型。
type Kind int

const (
	Set Kind = iota // 写入或覆盖 key
	Del             // 删除 key，键不存在则无操作
)

// Change 是单条变更，按 delta 内给出顺序依次应用。
type Change struct {
	Kind Kind
	Key  string
	Val  int // 仅 Kind==Set 时有意义
}

// Delta 是一个有序增量：基于版本 From，应用后到达版本 To。
type Delta struct {
	From    int
	To      int
	Changes []Change
}

// ApplyChange 把单条 Change 应用到状态 map 上（纯函数，不做任何校验）。
func ApplyChange(state map[string]int, c Change) {
	if c.Kind == Set {
		state[c.Key] = c.Val
	} else {
		delete(state, c.Key)
	}
}
