// Package termination 实现基于令牌环（Dijkstra–Scholten 风格）的
// 分布式终止检测器：基本消息与探测令牌都经由可注入网络异步投递。
package termination

// State 表示进程的活跃/空闲状态。
type State int

const (
	Idle   State = iota // 空闲
	Active              // 活跃
)

// Color 表示进程或令牌的颜色。
type Color int

const (
	White Color = iota // 白
	Black              // 黑
)

// Snapshot 是某一时刻检测器的完整可观测状态。
type Snapshot struct {
	States  []State // 各进程状态
	Colors  []Color // 各进程颜色
	Counts  []int   // 各进程计数器（发送 +1，接收 -1）
	Holder  int     // 令牌持有者
	Token   Color   // 令牌颜色
	Accum   int     // 令牌累计值
	Round   int     // 当前轮次
	Pending int     // 网络中在途的基本消息数
}
