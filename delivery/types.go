package delivery

// ErrCode 是可程序化区分的错误类别。拒绝次序由调用点按固定顺序检查保证。
type ErrCode int

const (
	ErrOK                ErrCode = iota
	ErrInvalidParam              // 参数非法（最先检查）
	ErrClockRollback             // 操作时刻早于此前已接受操作的最大时刻
	ErrOrderNotFound             // 订单不存在
	ErrExceptionNotFound         // 异常不存在
	ErrNotPickedUp               // 订单尚未取货
	ErrAlreadyDelivered          // 订单已送达
	ErrExceptionClosed           // 异常已关闭
	ErrOrderTerminal             // 订单已处于终态
	ErrActiveException           // 同一订单已有进行中的异常
	ErrTypeMismatch              // 操作与异常类型不匹配
	ErrWrongState                // 其他状态类错误
	ErrInvalidEvidence           // 拒收证据无效
	ErrContactTooSoon            // 相邻联络间隔不足
	ErrConditionWait             // 无法送达判定：等待时长不足（两条件同缺也报此码）
	ErrConditionContact          // 无法送达判定：联络次数不足
	ErrCorrectionWindow          // 地址纠正超过窗口
	ErrConfirmWindow             // 商家确认超过窗口
	ErrDistanceExceeded          // 新地址距离超限
)

// OpError 携带错误码与可读信息。被拒绝操作不改变任何状态、计时记录与时钟。
type OpError struct {
	Code ErrCode
	Msg  string
}

func (e *OpError) Error() string { return e.Msg }

// CodeOf 从 error 中提取 ErrCode；非本系统错误返回 ErrOK。
func CodeOf(e error) ErrCode {
	if oe, ok := e.(*OpError); ok {
		return oe.Code
	}
	return ErrOK
}

// OrderStatus 订单状态。
type OrderStatus int

const (
	OSPlaced            OrderStatus = iota // 已下单
	OSPicked                               // 已取货，配送中
	OSDelivered                            // 已送达（终态）
	OSException                            // 存在进行中的异常
	OSReturning                            // 退回途中
	OSReturned                             // 已退回（终态）
	OSReturnUnconfirmed                    // 退回未确认（终态）
	OSHandled                              // 就地处理完成（终态）
)

// Terminal 判定订单是否终态。
func (s OrderStatus) Terminal() bool {
	switch s {
	case OSDelivered, OSReturned, OSReturnUnconfirmed, OSHandled:
		return true
	}
	return false
}

// ExceptionType 异常类型。
type ExceptionType int

const (
	ETUnreachable  ExceptionType = iota // 联系不上
	ETWrongAddress                      // 地址有误
	ETRejection                         // 用户拒收
)

// ExceptionStatus 异常状态。
type ExceptionStatus int

const (
	ESActive        ExceptionStatus = iota // 进行中
	ESClosedUser                           // 用户回应/纠正成立，关闭
	ESUndeliverable                        // 已判定无法送达
	ESClosedExpired                        // 纠正窗口到期转为无法送达（历史）
)

// Disposition 商家在下单时固定的无法送达处置预设。
type Disposition int

const (
	DispReturn Disposition = iota // 退回商家
	DispLocal                     // 就地处理
)

// Party 责任归属方。
type Party int

const (
	PartyNone     Party = iota // 未产生损失/未判定
	PartyUser                  // 用户
	PartyMerchant              // 商家
)

// Params 构造参数，所有时长单位均为秒。
type Params struct {
	MinWaitSeconds       int64 // 联系不上：最短等待时长
	MinContacts          int   // 联系不上：最少联络次数
	MinContactInterval   int64 // 相邻联络最小间隔
	CorrectionWindow     int64 // 地址纠正窗口时长
	MaxCorrectionDist    int64 // 纠正允许的最大距离（曼哈顿距离）
	EvidenceValidSeconds int64 // 拒收证据有效时长
	RiderCompensation    int64 // 骑手处置补偿额
	MerchantConfirmWin   int64 // 退回途中的商家确认窗口
}

// ContactRecord 一次联络尝试（仅作审计与复现展示）。
type ContactRecord struct {
	Time int64
}

// ExceptionView 异常快照。
type ExceptionView struct {
	ID            string
	OrderID       string
	Type          ExceptionType
	Status        ExceptionStatus
	StartTime     int64
	ContactCount  int
	LastContactAt int64 // -1 表示尚无联络
	Deadline      int64 // 纠正窗口右端点；无窗口为 -1
	EvidenceID    string
	EvidenceAt    int64
}

// OrderView 订单快照，字段足以完整复现终态与责任归属。
type OrderView struct {
	ID              string
	Status          OrderStatus
	Disposition     Disposition
	AddressX        int64
	AddressY        int64
	AddressChanges  int
	ActiveException string // 进行中异常 ID，无则为空
	ReturnedAt      int64  // 骑手送回时刻；-1 表示尚未送回
	ConfirmDeadline int64  // 商家确认窗口右端点；-1 表示无
	Responsibility  Party
	CompPaid        bool
	CompAmount      int64
}
