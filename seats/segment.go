package seats

// NumClasses 是每个航段的舱位等级数，索引 0 最高、2 最低。
const NumClasses = 3

// qentry 是航段上一条尚未到期的预占占用记录。由于时钟单调不减且预占时长
// 固定，新记录的到期时刻单调不减，因此用 FIFO 队列即可，无需堆。
type qentry struct {
	expiry  int64
	class   int
	count   int
	entryID string
}

// Segment 表示一个物理航段。confirmed 与 held 分别累计已出票占用与未到期
// 预占占用；过期预占由惰性清扫从 held 中扣除，每条记录至多被清扫一次。
type Segment struct {
	ID        string
	Physical  int
	Overbook  int
	Auth      [NumClasses]int
	confirmed [NumClasses]int
	held      [NumClasses]int
	queue     []qentry
}

// avail 返回某等级可用数：该等级授权量减去该等级及所有更低等级的已占用数
// 之和。调用前必须先把本航段清扫到当前时刻。
func (s *Segment) avail(class int) int {
	return s.availWith(class, [NumClasses]int{})
}

// availWith 在 avail 的基础上额外减去 expired（尚未提交清扫、但在求值时刻
// 已过期的预占计数），用于在不改变任何状态的前提下求值未来时刻的可用数。
func (s *Segment) availWith(class int, expired [NumClasses]int) int {
	occ := 0
	for c := class; c < NumClasses; c++ {
		occ += s.confirmed[c] + s.held[c] - expired[c]
	}
	return s.Auth[class] - occ
}

// fits 判定在 class 等级上能否售出 pax 个座位（以提交清扫后的状态求值）。
func (s *Segment) fits(class, pax int) bool {
	return s.fitsWith(class, pax, [NumClasses]int{})
}

// fitsWith 判定在 class 等级上能否售出 pax 个座位：该等级及所有更高等级的
// 可用数都必须 >= pax。这既蕴含“可售”（pax>=1 时即可用数 > 0），也保证接
// 受后航段总占用不超过最高等级授权量这一不变式。
func (s *Segment) fitsWith(class, pax int, expired [NumClasses]int) bool {
	for c := 0; c <= class; c++ {
		if s.availWith(c, expired) < pax {
			return false
		}
	}
	return true
}
