package imaging

import "sync"

// DeviceClass 设备类别。
type DeviceClass int

const (
	ClassCT DeviceClass = 1
	ClassMR DeviceClass = 2
)

// Interval 左闭右开的整数分钟区间 [Start, End)。
type Interval struct {
	Start int
	End   int
}

// Config 全院统一配置。
type Config struct {
	ValidityNormal      int // 普通患者肾功能结果有效期（分钟，含边界）
	ValidityHighRisk    int // 高风险患者肾功能结果有效期（分钟，含边界）
	KidneyLow           int // 肾功能下限
	KidneyHigh          int // 肾功能上限（达到则无需水化）
	HydrationLead       int // 水化提前量（恰取等满足）
	PremedicationLead   int // 对比剂过敏预处理提前量（恰取等满足）
	ObservationMinutes  int // 增强后留观时长
	ObservationCapacity int // 留观位总数
	CleaningMinutes     map[DeviceClass]int
}

// RegisterDeviceRequest 登记设备。
type RegisterDeviceRequest struct {
	ID            string
	Class         DeviceClass
	FieldStrength int // 仅 MR 使用，正整数
}

// AddQCRequest 为设备登记每日重复质控时段（日内分钟，不跨日）。
type AddQCRequest struct {
	DeviceID string
	Interval Interval
}

// RegisterExamTypeRequest 登记检查类型。
type RegisterExamTypeRequest struct {
	ID       string
	Class    DeviceClass
	Duration int  // 占用时长（不含清洁），正整数
	Enhanced bool // 是否使用对比剂
}

// RegisterPatientRequest 登记患者。
type RegisterPatientRequest struct {
	ID               string
	HighRisk         bool
	NoImplant        bool // 无植入物则场强不限
	MaxFieldStrength int  // 有植入物时允许的最大场强，正整数
	Allergic         bool // 是否有对比剂过敏史
}

// RecordKidneyRequest 登记肾功能结果。
type RecordKidneyRequest struct {
	PatientID string
	Value     int
	SampledAt int // 采样时刻，不得晚于 Now
	Now       int
}

// BookRequest 受理预约。
type BookRequest struct {
	ID         string
	PatientID  string
	ExamTypeID string
	DeviceID   string
	Start      int
	Now        int
}

// HydrationRequest 为已受理预约登记水化开始时刻。
type HydrationRequest struct {
	AppointmentID string
	StartAt       int
	Now           int
}

// PremedicationRequest 为已受理预约登记过敏预处理开始时刻。
type PremedicationRequest = HydrationRequest

// CheckInRequest 签到。
type CheckInRequest struct {
	AppointmentID string
	Now           int
}

// RescheduleRequest 改约（NewDeviceID 为空表示不换设备）。
type RescheduleRequest struct {
	AppointmentID string
	NewStart      int
	NewDeviceID   string
	Now           int
}

// CancelRequest 取消预约。
type CancelRequest struct {
	AppointmentID string
	Now           int
}

// Hospital 影像检查预约与对比剂准入系统。
type Hospital struct {
	mu      sync.Mutex
	cfg     Config
	lastNow int

	devices  map[string]*device
	exams    map[string]*examType
	patients map[string]*patient
	appts    map[string]*appointment

	obs      observationCounter
	orderSeq int    // 肾功能结果登记次序
	idSeq    uint64 // 设备区间唯一 id
	accepted int    // 被接受操作计数（朴素模型对照用）
}

// NewHospital 创建系统。配置非法时返回错误。
func NewHospital(cfg Config) (*Hospital, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &Hospital{
		cfg:      cfg,
		devices:  map[string]*device{},
		exams:    map[string]*examType{},
		patients: map[string]*patient{},
		appts:    map[string]*appointment{},
		obs: observationCounter{
			starts: &multiSet{},
			ends:   &multiSet{},
		},
	}, nil
}
