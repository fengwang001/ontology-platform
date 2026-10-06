package ontology

import "time"

import "fmt"

// ErrorKind 标识被拒绝操作的错误类别。拒绝次序：
// 参数非法 > 月份已封账 > 顺序错误 > 数据缺失。
type ErrorKind int

const (
	KindIllegalParameter ErrorKind = iota + 1
	KindMonthSealed
	KindOrderError
	KindDataMissing
)

// SettError 是带类别的结算引擎错误。
type SettError struct {
	Kind    ErrorKind
	Message string
	// EarliestMissing 仅在数据缺失时给出最早缺失的间隔起点。
	EarliestMissing time.Time
}

func (e *SettError) Error() string { return e.Message }

func illegal(format string, args ...any) error {
	return &SettError{Kind: KindIllegalParameter, Message: fmt.Sprintf(format, args...)}
}

func sealed(format string, args ...any) error {
	return &SettError{Kind: KindMonthSealed, Message: "月份已封账: " + fmt.Sprintf(format, args...)}
}

func orderErr(format string, args ...any) error {
	return &SettError{Kind: KindOrderError, Message: "顺序错误: " + fmt.Sprintf(format, args...)}
}

func missing(first time.Time) error {
	return &SettError{Kind: KindDataMissing, Message: "数据缺失: earliest missing interval " + first.UTC().Format(time.RFC3339), EarliestMissing: first.UTC()}
}
