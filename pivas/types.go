package pivas

// MaxNow 是 now 与各类时刻的合法上界（含）。
const MaxNow int64 = 1_000_000_000

// Storage 存放方式：室温或冷藏。
type Storage int

const (
	StorageRoom Storage = iota // 室温
	StorageCold                // 冷藏
)

func (s Storage) String() string {
	if s == StorageCold {
		return "cold"
	}
	return "room"
}

// Drug 药品目录条目。稳定秒数与数量、时长一样必须为正整数。
type Drug struct {
	ID             string
	RoomStableSec  int64  // 室温稳定秒数
	ColdStableSec  int64  // 冷藏稳定秒数
	LightSensitive bool   // 是否须避光
	SolventClass   string // 所用溶媒类别
}

// Order 一张配置医嘱。LightProofBag 为是否使用避光外袋；
// 含须避光药品而 LightProofBag 为 false 时报避光冲突。
type Order struct {
	ID            string
	DrugIDs       []string // 1 到 6 种，不得重复
	Solvent       string   // 所选溶媒
	RequiredAt    int64    // 要求送达时刻
	Urgent        bool     // 紧急标志
	LightProofBag bool     // 是否使用避光外袋
}

// BenchConfig 洁净台配置。DurationByCount 下标即医嘱数量，
// 下标 0 闲置，1..Capacity 必须为正且随数量严格递增。
type BenchConfig struct {
	ID              string
	Capacity        int
	DurationByCount []int64
	ClearanceSec    int64 // 相邻批次清场间隔
}

// Admission 受理成功时给出的可行安排。
type Admission struct {
	OrderID  string
	BenchID  string
	BatchID  string
	Storage  Storage
	Start    int64 // 批次开始时刻
	Finish   int64 // 配置完成时刻 = Start + 批次时长
	Delivery int64 // 送达时刻 = Finish + 运送时长
	Reason   string
}
