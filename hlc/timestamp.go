package hlc

import "strconv"

// Timestamp 是混合逻辑时钟为单个事件分配的时间戳，
// 由物理/逻辑时间 Logical 与同一逻辑时间内的计数 Counter 组成，
// 按 (Logical, Counter) 字典序比较。
type Timestamp struct {
	Logical int64
	Counter uint32
}

// Compare 返回 -1、0、1，分别表示 t 早于、等于、晚于 other。
func (t Timestamp) Compare(other Timestamp) int {
	switch {
	case t.Logical < other.Logical:
		return -1
	case t.Logical > other.Logical:
		return 1
	case t.Counter < other.Counter:
		return -1
	case t.Counter > other.Counter:
		return 1
	default:
		return 0
	}
}

func (t Timestamp) String() string {
	return "(" + strconv.FormatInt(t.Logical, 10) + "," + strconv.FormatUint(uint64(t.Counter), 10) + ")"
}
