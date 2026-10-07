package chain

import "time"

// FrameRecord 是调用链条上一帧（一次内层或顶层调用）的审计记录。
type FrameRecord struct {
	Depth       int       // 该帧在链条中的深度（顶层为 0）
	Action      string    // 动作名
	Input       Params    // 规范化后的输入
	Key         FrameKey  // 用于自我触发检测的帧键
	PreBasis    string    // 前置条件判定依据（函数返回的说明）
	PostBasis   string    // 后置条件判定依据
	EffectBasis string    // 写入计划/输出计算依据
	WritePlan   []WriteOp // 该帧计算出的写入计划（放弃的帧也如实记录）
	Outputs     map[string]any
	Status      Status // 该帧最终结论
	Err         string // 失败原因（成功时为空）
	StartedAt   time.Time
}

// ChainRecord 是一次顶层链条执行的完整审计记录。
type ChainRecord struct {
	Entry       string
	Input       Params
	Frames      []*FrameRecord // 按实际发生顺序（含被拒绝的触发尝试）
	Status      Status         // 顶层帧结论
	Committed   bool           // 整条链条的写入计划是否已提交
	CommitIndex int            // 并发引擎中的全序提交序号（未提交为 -1）
	StartedAt   time.Time
	FinishedAt  time.Time
}

// Path 返回链条路径（动作名序列，含被拒绝的触发）。
func (c *ChainRecord) Path() []string {
	out := make([]string, len(c.Frames))
	for i, f := range c.Frames {
		out[i] = f.Action
	}
	return out
}
