// Package topn 在撤回式变更流（行的新增与撤回）上增量维护排名前 N 的视图。
//
// 排序规则：分数降序；分数相同时按键的字典序升序。
// 每次成功处理一条变更后，比较处理前后的前 N 名集合，
// 先输出离开（leave）的行，再输出进入（enter）的行。
package topn

// Row 是一行存活数据，由键与分数唯一标识。
type Row struct {
	Key   string
	Score int64
}

// Op 表示一条变更的操作类型。
type Op int

const (
	// OpUnknown 零值操作，属于非法输入。
	OpUnknown Op = 0
	// OpAdd 新增一行。
	OpAdd Op = 1
	// OpRetract 撤回一行，必须同时匹配键与分数。
	OpRetract Op = 2
)

// Change 是变更流中的一条输入：对某个键执行新增或撤回。
type Change struct {
	Op    Op
	Key   string
	Score int64
}

// Kind 表示日志条目的种类。
type Kind int

const (
	// KindLeave 表示一行离开前 N 名。
	KindLeave Kind = 1
	// KindEnter 表示一行进入前 N 名。
	KindEnter Kind = 2
)

// Entry 是前 N 名集合变化产生的一条日志。
// Rank 为该行在变化后榜单中的名次（从 1 开始）；离开条目取其离开前的名次。
type Entry struct {
	Kind  Kind
	Key   string
	Score int64
	Rank  int
}

// Reason 是输入被拒绝的可区分原因。
type Reason int

const (
	// ReasonUnknown 未分类（不会出现在正常返回中）。
	ReasonUnknown Reason = 0
	// ReasonInvalidArgument 构造参数或变更本身非法（N、容量、空键、未知操作等）。
	ReasonInvalidArgument Reason = 1
	// ReasonDuplicateKey 新增时键已经存活。
	ReasonDuplicateKey Reason = 2
	// ReasonRetractMissing 撤回的键当前不存在。
	ReasonRetractMissing Reason = 3
	// ReasonScoreMismatch 撤回时给定分数与存活行分数不符。
	ReasonScoreMismatch Reason = 4
	// ReasonLiveLimitExceeded 新增会使存活行数超过容量上限。
	ReasonLiveLimitExceeded Reason = 5
)

// RejectError 在一条变更（或构造参数）被拒绝时返回，携带可区分的原因。
type RejectError struct {
	Reason Reason
	// Msg 为面向日志的人类可读判定依据。
	Msg string
}

func (e *RejectError) Error() string {
	return e.Msg
}
