package inventory

// Class 舱位等级，数值越小等级越高；三个等级有序嵌套。
type Class int

const (
	ClassHigh Class = iota
	ClassMid
	ClassLow
)

const numClasses = 3

func (c Class) valid() bool { return c >= ClassHigh && c <= ClassLow }

func (c Class) String() string {
	switch c {
	case ClassHigh:
		return "high"
	case ClassMid:
		return "mid"
	case ClassLow:
		return "low"
	}
	return "unknown"
}

// Segment 一个被多条行程共用的航段。
type Segment struct {
	id        string
	seats     int             // 物理座位数，正整数
	overbook  int             // 超售上限，非负整数
	au        [numClasses]int // 授权量：高 >= 中 >= 低，且 au[high] <= seats+overbook
	confirmed [numClasses]int // 已确认出票占用（永不过期）
	held      [numClasses]int // 未到期预占占用（由惰性过期维护）
}

// validAuthorization 校验嵌套次序与超售上限。
func validAuthorization(au [numClasses]int, capacity int) bool {
	for _, v := range au {
		if v < 0 {
			return false
		}
	}
	return au[0] >= au[1] && au[1] >= au[2] && au[0] <= capacity
}

// occupiedFrom 等级 c 及所有更低等级的已占用数之和。
func (s *Segment) occupiedFrom(c Class) int {
	sum := 0
	for i := int(c); i < numClasses; i++ {
		sum += s.confirmed[i] + s.held[i]
	}
	return sum
}

// available 等级 c 的可用数 = 授权量 - 该等级及所有更低等级已占用数之和。
func (s *Segment) available(c Class) int {
	return s.au[int(c)] - s.occupiedFrom(c)
}

// sellable 等级 c 可售：c 及所有更高等级的可用数都大于零。
func (s *Segment) sellable(c Class) bool {
	for i := ClassHigh; i <= c; i++ {
		if s.available(i) <= 0 {
			return false
		}
	}
	return true
}

// canBook 等级 c 对 party 名旅客可订：c 及所有更高等级的可用数都不小于 party。
// 蕴含 sellable，并保证接受后 已确认+未到期预占 <= 最高等级授权量。
func (s *Segment) canBook(c Class, party int) bool {
	for i := ClassHigh; i <= c; i++ {
		if s.available(i) < party {
			return false
		}
	}
	return true
}
