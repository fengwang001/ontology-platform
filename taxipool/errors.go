package taxipool

import "errors"

// 十类可区分错误。每个操作按下列声明次序检查，只报次序最靠前的一类。
var (
	ErrInvalidParam        = errors.New("taxipool: invalid parameter")      // 参数非法
	ErrClockRollback       = errors.New("taxipool: clock rollback")         // 时钟回退
	ErrTerminalNotFound    = errors.New("taxipool: terminal not found")     // 候机楼不存在
	ErrDriverNotFound      = errors.New("taxipool: driver not found")       // 司机不存在
	ErrDriverBanned        = errors.New("taxipool: driver banned")          // 司机禁入中
	ErrDriverInQueue       = errors.New("taxipool: driver already in pool") // 司机已在队列中
	ErrQueueFull           = errors.New("taxipool: queue full")             // 队列已满
	ErrNoCarAvailable      = errors.New("taxipool: no car available")       // 无车可放行
	ErrDriverNotDispatched = errors.New("taxipool: driver not dispatched")  // 司机未处于放行中
	ErrArrivalOverdue      = errors.New("taxipool: arrival overdue")        // 到达已逾期
)
