// Package bussched 实现公交线路车辆排班与串车调整服务。
package bussched

import "strconv"

// TripNo 为车次号。使用浮点以便备车插入时取前后车次的中点，
// 中点运算在二进制下对二分插入是精确的，重放结果完全确定。
type TripNo float64

func (n TripNo) String() string {
	return strconv.FormatFloat(float64(n), 'g', -1, 64)
}

// Mid 返回介于 a、b 之间的车次号。
func Mid(a, b TripNo) TripNo { return TripNo((float64(a) + float64(b)) / 2) }

// Kind 为干预类别。
type Kind int

const (
	KindNone   Kind = iota // 不干预（只记录到站）
	KindHold               // 控制站扣车
	KindSkip               // 跳站快车
	KindInsert             // 备车插入
)

func (k Kind) String() string {
	switch k {
	case KindHold:
		return "扣车"
	case KindSkip:
		return "跳站"
	case KindInsert:
		return "备车插入"
	default:
		return "不干预"
	}
}

// ErrCode 为可区分的错误类别，判定次序与声明次序一致。
type ErrCode int

const (
	ErrInvalidParam    ErrCode = iota // 参数非法
	ErrClockRollback                  // 时钟回退
	ErrTripNotFound                   // 车次不存在
	ErrStationNotFound                // 站点不存在
	ErrOutOfOrder                     // 上报乱序
	ErrDuplicateReport                // 重复上报
	ErrNotBunched                     // 干预对象不是串车
	ErrDriverNotFound                 // 司机不存在
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrTripNotFound:
		return "车次不存在"
	case ErrStationNotFound:
		return "站点不存在"
	case ErrOutOfOrder:
		return "上报乱序"
	case ErrDuplicateReport:
		return "重复上报"
	case ErrNotBunched:
		return "干预对象不是串车"
	case ErrDriverNotFound:
		return "司机不存在"
	default:
		return "未知错误"
	}
}

// Error 为服务返回的错误，携带类别以便调用方区分。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Code.String() + ": " + e.Msg }

// CodeOf 提取错误的类别；非本服务错误返回 false。
func CodeOf(err error) (ErrCode, bool) {
	if e, ok := err.(*Error); ok {
		return e.Code, true
	}
	return 0, false
}

func newErr(code ErrCode, msg string) *Error { return &Error{Code: code, Msg: msg} }

// Plan 为运行方案，所有时长均为正整数秒。
type Plan struct {
	Headway   int64   // 目标发车间隔
	Dwell     []int64 // 每站计划停站时长
	Travel    []int64 // 相邻站计划行驶时长，长度为站数-1
	HoldCap   int64   // 单次扣车上限
	Tolerance int64   // 容忍量：最晚允许离站 = 计划到站 + Tolerance
	MaxOnDuty int64   // 司机连续在岗时长上限
}

// Event 为判定/降级/插入等事件的日志记录。
type Event struct {
	Time    int64
	Station string
	Trip    TripNo
	Kind    string
	Detail  string
}

// RecordView 为某车次在某站的到离时刻与干预查询结果。
type RecordView struct {
	Arrival      int64
	Departure    int64
	Intervention Kind
	Reason       string // 降级/拒绝原因，可查询
	Actual       bool   // true 为已上报的实际记录，false 为计划外推
}
