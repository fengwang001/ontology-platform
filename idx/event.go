// Package idx 实现本体平台的增量索引子系统：消费对象属性变更流，
// 以 (Version, ID) 为合法串行顺序维护按属性取值定位对象的倒排索引，
// 并支持索引依据字段的原子切换与失败回滚。
package idx

// EventID 唯一标识源系统发出的一条变更事件，用于去重与全序 tie-break。
type EventID string

// Event 是一条属性取值变更通知。
//
// 合法串行顺序的判定依据：同一 (ObjectID, Property) 上的多条事件按
// (Version, ID) 升序应用，其中 Version 是源系统为该对象属性分配的逻辑
// 序列号（如 CDC 日志位点），ID 在 Version 相同时提供确定性的全序。
// 物理到达顺序不参与排序。
type Event struct {
	ID       EventID
	ObjectID string
	Property string
	Value    string
	Null     bool   // true 表示取值被置空（墓碑）
	Version  uint64 // 源系统逻辑序列号
}

// lessEvent 判定 a 是否应在 b 之前应用。
func lessEvent(a, b Event) bool {
	if a.Version != b.Version {
		return a.Version < b.Version
	}
	return a.ID < b.ID
}
