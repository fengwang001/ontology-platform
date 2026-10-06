package policy

// daysPerYear 保单年度长度：自生效日起每 365 天一个区间，左闭右开。
const daysPerYear int64 = 365

// EndorsementType 批改类型，仅三类。
type EndorsementType int

const (
	EndorsementSumInsured  EndorsementType = iota + 1 // 保额变更
	EndorsementPaymentTerm                            // 缴费期变更
	EndorsementBeneficiary                            // 受益人变更
)

// EndorsementState 批改状态。
type EndorsementState int

const (
	StateScheduled    EndorsementState = iota + 1 // 预约
	StatePendingTopUp                             // 待补缴
	StateEffective                                // 已生效
)

// endorsement 批改记录。已撤销的批改用删除表示，不保留痕迹。
type endorsement struct {
	id       string
	typ      EndorsementType
	applyDay int64
	effDay   int64
	seq      int64 // 申请次序，同生效日时作次序依据
	state    EndorsementState

	newSumInsured int64 // 保额变更：新保额
	newPremium    int64 // 保额变更：生效时刻算出的新年缴保费
	topUp         int64 // 保额变更：待补缴金额
	newTerm       int   // 缴费期变更：新缴费期
	beneficiary   string

	heapIndex int
}

// endorsementHeap 按 (生效日, 申请次序) 排序的最小堆，
// 使“找下一条应生效批改”为 O(1)，弹出为 O(log n)，与历史已生效批改数无关。
type endorsementHeap []*endorsement

func (h endorsementHeap) Len() int { return len(h) }

func (h endorsementHeap) Less(i, j int) bool {
	if h[i].effDay != h[j].effDay {
		return h[i].effDay < h[j].effDay
	}
	return h[i].seq < h[j].seq
}

func (h endorsementHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *endorsementHeap) Push(x any) {
	en := x.(*endorsement)
	en.heapIndex = len(*h)
	*h = append(*h, en)
}

func (h *endorsementHeap) Pop() any {
	old := *h
	n := len(old)
	en := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return en
}

// Policy 保单账：全部金额以整数分持有，累计实缴保费滚动维护，
// 现金价值与缴费判定均不扫描历史记录。
type Policy struct {
	id            string
	effDate       int64
	premium       int64 // 当前年缴保费（分）
	sumInsured    int64 // 当前基本保额（分）
	minSumInsured int64
	hesitation    int64
	issueFee      int64
	ratios        []int64 // 现金价值比例表（百分比，可超 100）
	paymentTerm   int
	beneficiary   string

	now        int64
	terminated bool

	totalPaid int64          // 累计实缴保费（分），滚动增减
	paidYears map[int64]bool // 已缴保单年度

	endorsements      map[string]*endorsement // 全部未撤销批改（含已生效）
	queue             endorsementHeap         // 仅预约状态批改
	pendingTopUpCount int
	seqCounter        int64
	maxEffDay         int64 // 已生效批改中最晚生效日，无则 -1
}

// yearOf 计算 day 所在保单年度序号，要求 day >= effDate。
func (p *Policy) yearOf(day int64) int64 {
	return (day - p.effDate) / daysPerYear
}

// ratioAt 取保单年度对应比例，未给出的年度沿用最后一档。O(1)。
func (p *Policy) ratioAt(year int64) int64 {
	if year >= int64(len(p.ratios)) {
		year = int64(len(p.ratios)) - 1
	}
	return p.ratios[year]
}

// cashValueAt 现金价值：累计实缴 × 比例 向下取整，减借款本息，不足为零。O(1)。
func (p *Policy) cashValueAt(day, loan int64) int64 {
	cv := p.totalPaid*p.ratioAt(p.yearOf(day))/100 - loan
	if cv < 0 {
		return 0
	}
	return cv
}

// inHesitation 判断 day 是否仍在犹豫期内（含生效日与末日）。
func (p *Policy) inHesitation(day int64) bool {
	return day < p.effDate+p.hesitation
}

// hasPending 是否存在预约或待补缴状态的批改。
func (p *Policy) hasPending() bool {
	return len(p.queue) > 0 || p.pendingTopUpCount > 0
}

// ceilDiv 正数向上取整除法。
func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}
