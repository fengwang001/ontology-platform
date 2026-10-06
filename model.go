package ontology

import "math/big"

// ReadingKind 区分实抄与估算读数。
type ReadingKind int

const (
	// Actual 为实抄读数，一旦登记不可删除。
	Actual ReadingKind = iota
	// Estimated 为估算占位读数，可被同刻实抄替换，也可被删除。
	Estimated
)

func (k ReadingKind) String() string {
	if k == Estimated {
		return "估算"
	}
	return "实抄"
}

// Reading 是一条登记在某只电表上的读数。
type Reading struct {
	Time  int64
	Value uint64
	Kind  ReadingKind
}

// Meter 描述一只电表的静态属性。
// Digits 为显示位数（1..18），显示值范围为 [0, 10^Digits-1]；
// Multiplier 为该表挂接期内不变的倍率（正整数）；
// MaxUsagePerUnitTime 为单位时间最大合理（表显）用电量。
type Meter struct {
	ID                  string
	Digits              int
	Multiplier          int64
	MaxUsagePerUnitTime int64
}

// modulus 返回显示值的一圈大小 10^digits。
func modulus(digits int) uint64 {
	m := uint64(1)
	for i := 0; i < digits; i++ {
		m *= 10
	}
	return m
}

// rawDelta 计算相邻两条读数间的表显用电量。
// 后值不小于前值取差值；后值小于前值视为恰好翻转一圈后补足。
func rawDelta(prevValue, curValue, mod uint64) (delta uint64, rolledOver bool) {
	if curValue >= prevValue {
		return curValue - prevValue, false
	}
	return mod + curValue - prevValue, true
}

// withinRationalLimit 校验：倍率*表显用电量 <= 单位时间上限*区间时长。
// 使用 big.Int 比较，避免 int64 溢出。
func withinRationalLimit(delta uint64, multiplier int64, duration, maxRate int64) bool {
	if duration < 0 {
		return false
	}
	lhs := new(big.Int).Mul(big.NewInt(multiplier), new(big.Int).SetUint64(delta))
	rhs := new(big.Int).Mul(big.NewInt(maxRate), big.NewInt(duration))
	return lhs.Cmp(rhs) <= 0
}

// 挂接段：某供电点在 [InstallTime, RemoveTime) 内挂接某只电表。
// RemoveTime 为 math.MaxInt64 表示仍在挂接中；换表时旧段与新段在同一时刻相接。
type segment struct {
	meterID     string
	installTime int64
	removeTime  int64
}

func (s segment) activeAt(t int64) bool {
	return s.installTime <= t && t < s.removeTime
}

// pointState 是一个供电点的挂接段序列，按安装时刻有序、首尾相接。
type pointState struct {
	id       string
	segments []segment
}

// meterState 是一只电表的动态状态与按时刻有序的读数树。
type meterState struct {
	def *Meter
	mod uint64

	pointID     string // 当前所在供电点，空串表示未挂接
	installTime int64
	removeTime  int64 // 未拆除时为 math.MaxInt64
	tree        *readingTree
}

// QueryResult 是供电点两点间用电量查询结果。
type QueryResult struct {
	// Usage 为区间内供电点用电量（已乘各表倍率）。
	Usage *big.Int
	// ContainsEstimated 表示结果是否包含估算读数参与的区间。
	ContainsEstimated bool
}
