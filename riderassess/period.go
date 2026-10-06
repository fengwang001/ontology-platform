package riderassess

// Config 是系统的全部构造参数。
type Config struct {
	PeriodLen       int64
	BaseTime        int64
	Penalties       map[string]int
	Thresholds      []int
	AppealWindow    int64
	ClusterSpan     int64
	MaxDropPerCycle int
	CompPerLevel    int
}

// gradeForScore 按阈值定级。
func gradeForScore(score int, thresholds []int) int { return 0 }

// QueryResult 是周期查询结果。
type QueryResult struct {
	Period  int64
	Score   int
	Grade   int
	Settled bool
	Benefit int
}
