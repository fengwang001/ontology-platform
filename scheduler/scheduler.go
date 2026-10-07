package scheduler

import "sync"

// Role 表示子流角色。
type Role int

const (
	// Normal 普通子流，优先参与选路。
	Normal Role = iota
	// Backup 备用子流，仅当不存在任何活跃的普通子流时才参与选路。
	Backup
)

// ErrorCode 区分事件被拒绝的原因，优先级与声明顺序一致：
// 参数非法 > 子流不存在 > 过期确认 > 确认非整段 > 确认越界。
type ErrorCode int

const (
	ErrInvalidArgument ErrorCode = iota // 参数非法
	ErrSubflowNotFound                  // 子流不存在
	ErrStaleAck                         // 过期确认
	ErrAckMisaligned                    // 确认非整段
	ErrAckOutOfRange                    // 确认越界
)

// Error 是调度器拒绝事件时返回的错误类型。
type Error struct {
	Code   ErrorCode
	Detail string
}

// SentSegment 描述一次发送决策产出的段。
type SentSegment struct {
	Seq     int64  // 连接级序号（段首字节的序号）
	Len     int64  // 段长（字节）
	Subflow string // 所选子流标识
	Resend  bool   // 是否为重新注入段
	Reason  string // 判定依据（供日志）
}

// Decision 是单个事件处理完后的输出。
type Decision struct {
	Sent    []SentSegment // 本次发出的各段
	Blocked string        // 队首段无法发送时的原因；队列已空则为空
}

// Stats 记录基本操作计数，用于验证单事件开销不随队列与历史增长。
type Stats struct {
	Events           int64 // 已接受（未被拒绝）的事件数
	SegmentsWritten  int64 // 写入产生的新段数
	SegmentsSent     int64 // 发送的段次数（含重发）
	SegmentsResent   int64 // 其中重新注入段的发送次数
	SegmentsReleased int64 // 子流确认释放的在途段数
	SegmentsEvicted  int64 // 子流失效清除的在途段数
	SegmentsDropped  int64 // 被连接级确认覆盖而丢弃的待发重新注入段数
	PickCalls        int64 // 选路调用次数
	TreapInserts     int64
	TreapRemovals    int64
	TreapNodeVisits  int64 // treap 内部节点访问次数（体现 log 代价）
	FreshPushes      int64
	FreshPops        int64
	InflightPushes   int64
	InflightPops     int64
}

// Seg 是内部段的对外只读视图，用于快照比对。
type Seg struct {
	Seq      int64
	Len      int64
	Excluded string // 重新注入段被排除的子流（失效来源），新数据段为空
}

// SubflowState 是单个子流的对外只读快照。
type SubflowState struct {
	ID            string
	RTT           int64
	Cwnd          int64
	InitialCwnd   int64
	Role          Role
	Active        bool
	AckedBytes    int64 // 该子流累计已确认字节数
	SentBytes     int64 // 该子流累计发送字节数
	InflightBytes int64
	Inflight      []Seg
}

// State 是调度器完整状态的可比对快照。
type State struct {
	NextSeq         int64
	SentMax         int64
	Acked           int64
	Window          int64
	PendingReinject []Seg          // 待发重新注入段，按连接级序号升序
	PendingFresh    []Seg          // 待发新数据段，按写入顺序
	Subflows        []SubflowState // 按标识排序
}

// Scheduler 是多路径连接的发送侧调度器。所有方法可并发调用，
// 其结果等价于某个串行顺序（内部以单一互斥锁串行化）。
type Scheduler struct {
	mu           sync.Mutex
	m            int64
	nextSeq      int64
	sentMax      int64
	acked        int64
	window       int64
	subs         map[string]*subflow
	activeNormal int
	reinj        treap
	fresh        segQueue
	stats        Stats
}

// subflow 是单个子流的内部状态。
type subflow struct {
	id            string
	rtt           int64
	cwnd          int64
	initialCwnd   int64
	role          Role
	active        bool
	ackedBytes    int64
	sentBytes     int64
	inflightBytes int64
	inflight      segQueue
}
