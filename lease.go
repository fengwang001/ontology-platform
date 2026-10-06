package ontology

// Phase 为租约在某一时刻的状态。
type Phase string

const (
	PhaseActive     Phase = "active"     // 固定租期内（含到期日等待未逾期要约答复的短暂情形）
	PhaseHoldover   Phase = "holdover"   // 按月延续，租金冻结
	PhaseEnded      Phase = "ended"      // 固定租期届满，无延续依据
	PhaseTerminated Phase = "terminated" // 延续期经通知终止
)

// OfferStage 描述要约当前所处的答复阶段。
type OfferStage string

const (
	StageTenant    OfferStage = "tenant"    // 等待租户答复
	StageCounter   OfferStage = "counter"   // 租户已反要约，等待房东答复
	StageAccepted  OfferStage = "accepted"  // 已接受（续签成立）
	StageRejected  OfferStage = "rejected"  // 被拒绝（租户拒绝或房东拒绝反要约）
	StageWithdrawn OfferStage = "withdrawn" // 房东撤回
	StageExpired   OfferStage = "expired"   // 逾期未决
)

// Offer 是一份续签要约及其完整答复轨迹。
type Offer struct {
	ID          string
	Rent        int // 要约新租金
	NewEnd      int // 要约新期限（新起始日恒为旧终止日）
	IssueDay    int
	Stage       OfferStage
	CounterRent int
	CounterDay  int
	ResolveDay  int
}

// tenantDeadline 为租户最后可答复日（当天仍可）。
func (o *Offer) tenantDeadline(c int) int { return o.IssueDay + c }

// counterDeadline 为房东对反要约最后可答复日。
func (o *Offer) counterDeadline(c int) int { return o.CounterDay + c }

type lease struct {
	id         string
	start, end int
	rent       int
	lastAdjust int

	offer           *Offer
	offerEverIssued bool // 本租期内是否曾发出要约（保护期判定用）
	protected       bool // 已进入保护期
	phase           Phase
	holdoverStart   int
	noticeDay       int
	terminateAt     int
	generation      int // 每次续签或阶段转换递增，用于堆条目失效判定
}

// OfferView 是要约的只读视图。
type OfferView struct {
	ID          string     `json:"id"`
	Rent        int        `json:"rent"`
	NewEnd      int        `json:"new_end"`
	IssueDay    int        `json:"issue_day"`
	Stage       OfferStage `json:"stage"`
	CounterRent int        `json:"counter_rent,omitempty"`
	CounterDay  int        `json:"counter_day,omitempty"`
	ResolveDay  int        `json:"resolve_day,omitempty"`
}

// LeaseView 是租约在某一 now 下的只读状态视图。
type LeaseView struct {
	ID            string     `json:"id"`
	Start         int        `json:"start"`
	End           int        `json:"end"`
	Rent          int        `json:"rent"`
	LastAdjust    int        `json:"last_adjust"`
	Phase         Phase      `json:"phase"`
	Protected     bool       `json:"protected"`
	HoldoverStart int        `json:"holdover_start,omitempty"`
	NoticeDay     int        `json:"notice_day,omitempty"`
	TerminateAt   int        `json:"terminate_at,omitempty"`
	PendingOffer  *OfferView `json:"pending_offer,omitempty"`
	LastOffer     *OfferView `json:"last_offer,omitempty"`
}

func offerView(o *Offer) *OfferView {
	if o == nil {
		return nil
	}
	return &OfferView{
		ID: o.ID, Rent: o.Rent, NewEnd: o.NewEnd, IssueDay: o.IssueDay,
		Stage: o.Stage, CounterRent: o.CounterRent, CounterDay: o.CounterDay,
		ResolveDay: o.ResolveDay,
	}
}

func (s *Service) viewLocked(l *lease) *LeaseView {
	v := &LeaseView{
		ID: l.id, Start: l.start, End: l.end, Rent: l.rent, LastAdjust: l.lastAdjust,
		Phase: l.phase, Protected: l.protected, HoldoverStart: l.holdoverStart,
		NoticeDay: l.noticeDay, TerminateAt: l.terminateAt,
	}
	if l.offer != nil {
		switch l.offer.Stage {
		case StageTenant, StageCounter:
			v.PendingOffer = offerView(l.offer)
		default:
			v.LastOffer = offerView(l.offer)
		}
	}
	return v
}
