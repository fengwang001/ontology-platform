// Package ontology 在撤回式变更流（retraction stream）上增量维护前 N 名视图。
//
// 变更只有两种：新增一行（Add）与撤回一行（Retract，需携带与新增时一致的分数）。
// 排序规则为分数降序、分数相同时按键的字典序升序；组件始终保留全部存活行，
// 以便榜内行被撤回时由榜外排序最靠前的行补位。
package ontology

import "errors"

// Score 是行的分数。使用整数使“撤回时分数一致”的判定具有确定性。
type Score = int64

// Row 是变更流中的一行。
type Row struct {
	Key   string
	Score Score
}

// ChangeKind 标识变更种类。
type ChangeKind int

const (
	// KindUnknown 是零值，视为非法参数。
	KindUnknown ChangeKind = 0
	// KindAdd 新增一行。
	KindAdd ChangeKind = 1
	// KindRetract 撤回一行。
	KindRetract ChangeKind = 2
)

// Change 是变更流中的一条变更。
type Change struct {
	Kind ChangeKind
	Row  Row
}

// RejectReason 是输入被拒绝的可区分原因。
type RejectReason int

const (
	// RejectNone 表示未被拒绝。
	RejectNone RejectReason = 0
	// RejectInvalidArgument 参数非法（未知变更种类、空键、非法构造参数等）。
	RejectInvalidArgument RejectReason = 1
	// RejectDuplicateKey 新增了一个已经存活的键。
	RejectDuplicateKey RejectReason = 2
	// RejectKeyNotFound 撤回了一个当前不存在的键。
	RejectKeyNotFound RejectReason = 3
	// RejectScoreMismatch 撤回时携带的分数与该行存活分数不一致。
	RejectScoreMismatch RejectReason = 4
	// RejectTooManyLiveRows 新增会使存活行数超过构造时给定的上限。
	RejectTooManyLiveRows RejectReason = 5
)

// Entry 是榜单视图中的一条带名次的行。
type Entry struct {
	Rank  int
	Key   string
	Score Score
}

// ChangeResult 是处理一条变更后的结果。被拒绝时各切片为空、Reason 给出原因；
// 被接受时 Left/Entered 为本条变更引起的离榜/入榜变化。
type ChangeResult struct {
	Input    Change
	Accepted bool
	Reason   RejectReason
	// Left 为离开前 N 名的行，按变更前名次升序；先于 Entered 输出。
	Left []Entry
	// Entered 为进入前 N 名的行，按变更后名次升序。
	Entered []Entry
	// Top 为处理完本条变更后完整的前 N 名快照（仅接受时填充）。
	Top []Entry
}

// View 是某一时刻的一致性只读快照。
type View struct {
	// Top 为前 N 名，名次从 1 开始。
	Top []Entry
	// All 为全部存活行（含榜外），按同一排序规则排好序，名次从 1 开始。
	All []Entry
	// LiveCount 为存活行数，等于 len(All)。
	LiveCount int
}

// 各类拒绝原因对应的哨兵错误，可直接用 errors.Is 区分。
var (
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	ErrDuplicateKey    = errors.New("ontology: row key already exists")
	ErrKeyNotFound     = errors.New("ontology: row key not found")
	ErrScoreMismatch   = errors.New("ontology: retract score does not match live row")
	ErrTooManyLiveRows = errors.New("ontology: live row limit exceeded")
)

// ReasonError 将 RejectReason 映射为对应的哨兵错误；RejectNone 返回 nil。
func ReasonError(r RejectReason) error {
	switch r {
	case RejectInvalidArgument:
		return ErrInvalidArgument
	case RejectDuplicateKey:
		return ErrDuplicateKey
	case RejectKeyNotFound:
		return ErrKeyNotFound
	case RejectScoreMismatch:
		return ErrScoreMismatch
	case RejectTooManyLiveRows:
		return ErrTooManyLiveRows
	default:
		return nil
	}
}
