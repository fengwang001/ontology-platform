package lab

// TubeState 是标本管的生命周期状态。
type TubeState int

const (
	TubeAwaitingSign TubeState = iota + 1 // 已采集待签收
	TubeSigned                            // 已签收（终结）
	TubeVoided                            // 已作废（终结）
)

// Tube 是一支标本管，采集时登记，签收或作废后终结。
type Tube struct {
	ID        string
	TubeType  string
	PatientID string

	items map[string]*AppItem // 管内仍待签收的项目；key 为项目标识

	Cold     bool // 当前有效的运送方式（true=冷藏）；默认常温
	Shipped  bool // 是否登记过送出
	SignTime int64
	State    TubeState
}

func newTube(id, tubeType, patientID string) *Tube {
	return &Tube{
		ID:        id,
		TubeType:  tubeType,
		PatientID: patientID,
		items:     make(map[string]*AppItem),
		State:     TubeAwaitingSign,
	}
}
