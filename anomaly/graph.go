package anomaly

// EdgeMask 用位掩码表示两个事务之间存在的边类型；
// 同一对事务在不同键上产生的同类或异类边会被合并。
type EdgeMask uint8

const (
	EdgeWW EdgeMask = 1 << iota // 写写边
	EdgeWR                      // 写读边
	EdgeRW                      // 读写边
)

type edge struct {
	from int
	to   int
	mask EdgeMask
	why  string
}

type graph struct {
	nodes []int
	adj   map[int]map[int]EdgeMask
	edges []edge
}
