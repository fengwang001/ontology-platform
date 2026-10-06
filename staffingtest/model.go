// Package staffingtest 包含独立编写的朴素参考模型（naive model）与随机/并发对照测试。
// 朴素模型刻意使用最直白的数据结构：每次判定都线性扫描全部 offer 重算占用，
// 不使用任何增量索引；与 staffing.Service 共享同一套规格，但实现独立，
// 用于大量随机操作序列下的差分对照。
package staffingtest

// MStatus 是朴素模型的通知状态，字符串与 staffing.OfferStatus 一致。
type MStatus string

const (
	MPending   MStatus = "PENDING"
	MAccepted  MStatus = "ACCEPTED"
	MOnboarded MStatus = "ONBOARDED"
	MRejected  MStatus = "REJECTED"
	MExpired   MStatus = "EXPIRED"
	MWithdrawn MStatus = "WITHDRAWN"
	MCanceled  MStatus = "CANCELED"
	MAbandoned MStatus = "ABANDONED"
)

type mPosition struct {
	id        string
	bandLow   int
	bandHigh  int
	headcount int
	frozen    bool
}

type mOffer struct {
	id            int64
	candidate     string
	position      string
	salary        int
	deadline      int
	issuedAt      int
	status        MStatus
	respondedAt   int
	entryDate     int
	onboardedAt   int
	leftAt        int
	canceledAt    int
	usedException bool
}

type mException struct {
	id        string
	position  string
	quarter   int
	total     int
	remaining int
}

// Result 是一步操作的可观察结果，与真实服务逐字段对照。
type Result struct {
	OK    bool
	Code  int // 与 staffing.Code 数值对齐
	Index int // 批量失败下标，非批量为 -1
	IDs   []int64
}

// Model 是朴素参考模型。
type Model struct {
	Cooldown int
	Grace    int

	now      int
	clockSet bool

	positions  map[string]*mPosition
	candidates map[string]bool
	offers     map[int64]*mOffer
	exceptions map[string]*mException
	lastBlock  map[string]map[string]int

	nextID int64
}

// NewModel 创建朴素模型。
func NewModel(cooldown, grace int) *Model {
	return &Model{
		Cooldown:   cooldown,
		Grace:      grace,
		positions:  map[string]*mPosition{},
		candidates: map[string]bool{},
		offers:     map[int64]*mOffer{},
		exceptions: map[string]*mException{},
		lastBlock:  map[string]map[string]int{},
	}
}

func quarterOf(day int) int { return day / 90 }
