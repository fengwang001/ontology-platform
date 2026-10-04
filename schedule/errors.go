package schedule

import "errors"

// 失败原因以错误码形式给出，便于精确复现与测试对照。
var (
	ErrInvalid   = errors.New("schedule: invalid argument")      // 参数非法
	ErrClockBack = errors.New("schedule: clock moved backwards") // 时钟回退
	ErrNotFound  = errors.New("schedule: room or equipment not found")
	ErrDuplicate = errors.New("schedule: surgery id already exists")
	ErrNoRoom    = errors.New("schedule: no room registered")
	ErrRoomBusy  = errors.New("schedule: room conflict")      // 手术间冲突
	ErrSurgeon   = errors.New("schedule: surgeon conflict")   // 医生冲突
	ErrEquip     = errors.New("schedule: equipment shortage") // 设备不足
	ErrState     = errors.New("schedule: surgery not cancellable")
)

// EquipShortError 携带设备不足时字节序最小的类型名。
type EquipShortError struct {
	Type string
}

func (e *EquipShortError) Error() string { return "schedule: equipment shortage: " + e.Type }

func (e *EquipShortError) Is(target error) bool { return target == ErrEquip }
