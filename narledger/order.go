package narledger

import "sort"

type orderStatus int

const (
	orderOpen orderStatus = iota + 1
	orderSettled
	orderDiscrepancy
)

func (s orderStatus) String() string {
	switch s {
	case orderOpen:
		return "OPEN"
	case orderSettled:
		return "SETTLED"
	case orderDiscrepancy:
		return "DISCREPANCY"
	default:
		return "UNKNOWN"
	}
}

// allocation 记录一张领用单据从某批次分出的数量。
type allocation struct {
	drug string
	lot  string
	qty  int64
}

// order 领用单据。
type order struct {
	id        string
	dept      string
	applicant string
	drug      string
	qty       int64
	createdAt int64
	deadline  int64
	status    orderStatus
	shortfall int64
	used      int64
	returned  int64
	residue   int64
	allocs    []allocation
}

// overdueAt 严格晚于 deadline（领用时刻+24h）才算逾期；恰到期仍在期内。
func (o *order) overdueAt(t int64) bool { return o.status == orderOpen && t > o.deadline }

func (o *order) allocTotal() int64 {
	var n int64
	for _, a := range o.allocs {
		n += a.qty
	}
	return n
}

// orderBook 单据账册。
type orderBook struct {
	byID   map[string]*order
	byDept map[string][]*order // 追加式，遍历时按状态过滤；规模判定另由 deptBook 计数器保证
}

func newOrderBook() *orderBook {
	return &orderBook{byID: make(map[string]*order), byDept: make(map[string][]*order)}
}

func (b *orderBook) add(o *order) {
	b.byID[o.id] = o
	b.byDept[o.dept] = append(b.byDept[o.dept], o)
}

func (b *orderBook) get(id string) *order { return b.byID[id] }

// openOfDept 返回科室当前尚未结清（OPEN）的单据，按创建时刻排序。
// 仅供基准测试的小规模对照与快照使用；热路径的上限/锁定判定不调用它。
func (b *orderBook) openOfDept(dept string) []*order {
	var out []*order
	for _, o := range b.byDept[dept] {
		if o.status == orderOpen {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].createdAt != out[j].createdAt {
			return out[i].createdAt < out[j].createdAt
		}
		return out[i].id < out[j].id
	})
	return out
}
