// Package metering 实现楼宇总表与分户表的抄表记账与公摊分摊服务。
package metering

import "fmt"

// ErrCode 为错误类别，声明顺序即固定报告优先级（靠前者优先报告）。
type ErrCode int

const (
	ErrInvalidParam       ErrCode = iota + 1 // 参数非法
	ErrClockRollback                         // 时钟回退
	ErrMeterNotFound                         // 表不存在
	ErrOutOfOrder                            // 读数乱序
	ErrIllegalReading                        // 读数非法
	ErrEstimateNotAllowed                    // 估抄条件不满足
	ErrPeriodSettled                         // 账期已结算不可重复结算
	ErrNegativeShared                        // 公摊为负
)

var errNames = map[ErrCode]string{
	ErrInvalidParam:       "参数非法",
	ErrClockRollback:      "时钟回退",
	ErrMeterNotFound:      "表不存在",
	ErrOutOfOrder:         "读数乱序",
	ErrIllegalReading:     "读数非法",
	ErrEstimateNotAllowed: "估抄条件不满足",
	ErrPeriodSettled:      "账期已结算不可重复结算",
	ErrNegativeShared:     "公摊为负",
}

func (c ErrCode) String() string { return errNames[c] }

// Error 为服务返回的业务错误，Code 可用于判定类别。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func fail(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Kind 为读数类别。
type Kind int

const (
	Actual   Kind = iota // 实抄
	Estimate             // 估抄
)

// Role 为表计角色。
type Role int

const (
	Master Role = iota // 总表
	Unit               // 分户表
)

// Config 为服务配置。
type Config struct {
	// Gap 为估抄准入门槛：距上一次实抄须严格超过 Gap 个时刻单位才允许估抄。
	Gap int64
}

// UnitBill 为单个分户在一个账期内的账单条目。
type UnitBill struct {
	UnitID  string
	Own     int64 // 自用量（记账）
	Share   int64 // 公摊分摊量
	Payable int64 // 应付金额（= Share，见 DESIGN.md）
}

// Bill 为一个已结算账期的账单，结算后不可更改。
type Bill struct {
	Start       int64
	End         int64
	MasterUsage int64 // 总表用量
	SharedUsage int64 // 公摊用量 = 总表 - 分户自用之和
	Units       []UnitBill
}

// UnitDelta 为单个分户的应付差额。
type UnitDelta struct {
	UnitID string
	Delta  int64
}

// Correction 为对已结算账期的更正，只记录差额，不改动原账单。
// 同一账期各户差额之和恒等于该账期公摊重算前后之差 SharedDelta。
type Correction struct {
	Start       int64
	End         int64
	SharedDelta int64
	Deltas      []UnitDelta // 仅含非零差额，按户号升序
}

// Stats 为可验证的性能计数，用于证明结算开销与历史读数总量无关。
type Stats struct {
	ScanEntries  int64 // 结算扫描触碰的读数条目数（与账期涉及读数成正比）
	SearchProbes int64 // 二分定位的比较次数（O(log n)）
}
