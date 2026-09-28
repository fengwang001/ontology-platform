// Package replica 实现分区副本的同步副本集（ISR）维护与高水位（HW）推进。
//
// 所有会改变状态的方法都接受显式时间戳 now，便于确定性测试与时钟回退检测；
// 任何非法输入都会被整体拒绝，失败不留痕（高水位、同步副本集、各副本进度均不变）。
package replica

// ReplicaSet 维护一个分区的同步副本集与高水位。零值不可用，必须用 New 创建。
type ReplicaSet struct {
}

// Config 描述同步副本集的静态配置。
type Config struct {
	// LeaderID 是领导者副本的唯一名，不允许为空。
	LeaderID string
	// LagTolerance 是跟随者"追上时间"允许落后于当前时间的最大容忍量（>0）。
	LagTolerance int64
}

// Snapshot 是某一时刻同步副本集的不可变查询视图。
type Snapshot struct {
	// HighWatermark 是当前已提交高水位（已提交判断：offset <= HW）。
	HighWatermark int64
	// LogEndOffset 是领导者日志结束位点。
	LogEndOffset int64
	// InSync 是当前同步副本集（含领导者），按名字排序。
	InSync []string
	// Followers 是跟随者进度：名字 -> 已拥有位点。
	Followers map[string]int64
}

// New 创建同步副本集。初始仅含领导者，HW 与 LEO 均为 startOffset。
func New(cfg Config, startOffset int64) (*ReplicaSet, error) {
	return nil, ErrInvalidArgument
}

// AddFollower 登记一个跟随者并令其加入同步副本集。
func (r *ReplicaSet) AddFollower(id string, offset int64, now int64) error {
	return ErrInvalidArgument
}

// Append 是领导者写入：推进领导者日志结束位点。返回推进后的 LEO。
func (r *ReplicaSet) Append(records int, now int64) (int64, error) {
	return 0, ErrInvalidArgument
}

// Pull 是跟随者拉取：以其已拥有位点更新进度；追平时刷新追上时间。
// 返回当前高水位，供跟随者判断哪些消息已提交。
func (r *ReplicaSet) Pull(id string, fetchedOffset int64, now int64) (int64, error) {
	return 0, ErrInvalidArgument
}

// Rejoin 供被移出同步副本集的跟随者重新加入；要求其位点已追上当前高水位。
func (r *ReplicaSet) Rejoin(id string, offset int64, now int64) error {
	return ErrInvalidArgument
}

// Tick 周期性检查：追上时间严格早于 now-LagTolerance 的跟随者被移出同步副本集。
// 返回本次被移出的副本名（按名字排序）。
func (r *ReplicaSet) Tick(now int64) ([]string, error) {
	return nil, ErrInvalidArgument
}

// Snapshot 返回当前状态的只读视图。
func (r *ReplicaSet) Snapshot() Snapshot {
	return Snapshot{}
}

// Committed 判断给定位点是否已提交（offset <= HW）。
func (r *ReplicaSet) Committed(offset int64) bool {
	return false
}
