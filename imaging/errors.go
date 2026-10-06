package imaging

// Error 是系统可区分的业务错误。
type Error string

func (e Error) Error() string { return string(e) }

const (
	// ErrInvalidArgument 参数非法（标识为空、时长非正、时间越界等）。
	ErrInvalidArgument Error = "参数非法"
	// ErrClockRollback 操作 now 小于上一次被接受操作的 now。
	ErrClockRollback Error = "时钟回退"
	// ErrNotFound 被引用的对象不存在。
	ErrNotFound Error = "对象不存在"
	// ErrInvalidState 对象当前状态不允许该操作。
	ErrInvalidState Error = "状态不符"
	// ErrDeviceClassMismatch 检查类型所需设备类别与设备类别不符。
	ErrDeviceClassMismatch Error = "设备类别不符"
	// ErrImplantIncompatible 磁共振场强超过患者植入物允许的最大场强。
	ErrImplantIncompatible Error = "植入物不兼容"
	// ErrKidneyMissing 增强检查缺少有效的肾功能结果（无结果或超期）。
	ErrKidneyMissing Error = "结果缺失或过期"
	// ErrKidneyInsufficient 肾功能数值低于下限。
	ErrKidneyInsufficient Error = "肾功能不足"
	// ErrDeviceBusy 设备占用区间与既有预约重叠。
	ErrDeviceBusy Error = "设备时段冲突"
	// ErrQCConflict 设备占用区间落在质控时段内。
	ErrQCConflict Error = "质控时段冲突"
	// ErrObservationFull 留观位在某时刻已满。
	ErrObservationFull Error = "留观位不足"
	// ErrCheckinWindow 签到不在允许窗口内（拒绝且不改变状态）。
	ErrCheckinWindow Error = "签到窗口不符"
	// ErrNotHydrated 签到复核：应水化而未按要求水化。
	ErrNotHydrated Error = "未水化"
	// ErrNotPremedicated 签到复核：过敏患者未按要求预处理。
	ErrNotPremedicated Error = "未预处理"
)
