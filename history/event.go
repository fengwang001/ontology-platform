// Package history 提供按实例（非空字节串）隔离的追加式事件日志。
//
// 每条事件占用一个连续序号（从 1 开始）。事件共三种：
//   - TypeUpdate：更新被接受，携带 uid 与 delta；
//   - TypeApplied：队首更新已应用，携带被应用更新的 U 序号；
//   - TypeClosed：实例已关闭，每个实例至多一条。
package history

// EventType 标识事件种类。
type EventType uint8

const (
	TypeUpdate  EventType = iota + 1 // U(uid, delta)
	TypeApplied                      // A(s)：s 为被应用更新的 U 序号
	TypeClosed                       // C
)

// Event 是日志中的一条不可变记录。
type Event struct {
	Index int64 // 该事件自身的序号，从 1 连续
	Type  EventType
	UID   []byte // 仅 TypeUpdate 使用
	Delta int64  // 仅 TypeUpdate 使用
	Seq   int64  // 仅 TypeApplied 使用：被应用的 U 事件序号
}
