package limitbook

import "sync"

// Member 一名被保成员的限额账。
type Member struct {
	id            string
	annual        limitSeries                // 个人年度限额
	lifetimeLimit int64                      // 个人终身限额（登记后不变）
	lifetimeUsed  int64                      // 终身已用
	yearUsed      map[int64]int64            // 年度 -> 个人年度已用
	itemUsed      map[string]map[int64]int64 // 项目 -> 年度 -> 项目已用
	claims        []string                   // 已受理理赔号，按受理次序（栈）
}

// Item 一个赔付项目。
type Item struct {
	id     string
	annual limitSeries // 个人年度项目限额
}

// Policy 一张保单的完整账簿。所有可变字段仅在 mu 保护下访问，
// 因此同一保单上的并发操作等价于某个串行顺序。
type Policy struct {
	mu         sync.Mutex
	id         string
	start      int64 // 承保起始日（非负整数天）
	yearLen    int64 // 保单年度长度（正整数天）
	family     limitSeries
	familyUsed map[int64]int64 // 年度 -> 家庭共享已用
	members    map[string]*Member
	items      map[string]*Item
	claims     map[string]*claimRecord // 活跃（未冲正）理赔
	dayCounts  map[int64]int           // 活跃理赔明细发生日多重集
	maxDay     int64                   // 活跃理赔中最晚发生日，无则为 -1
	ops        int64                   // 桶访问计数，仅供性能不变量测试读取
}

func newPolicy(id string, start, yearLen, familyAnnualLimit int64) *Policy {
	return &Policy{
		id:         id,
		start:      start,
		yearLen:    yearLen,
		family:     newLimitSeries(familyAnnualLimit),
		familyUsed: make(map[int64]int64),
		members:    make(map[string]*Member),
		items:      make(map[string]*Item),
		claims:     make(map[string]*claimRecord),
		dayCounts:  make(map[int64]int),
		maxDay:     -1,
	}
}

// yearOf 返回发生日所属保单年度。年度为左闭右开区间，逐年顺延。
func (p *Policy) yearOf(day int64) int64 {
	return (day - p.start) / p.yearLen
}

func (p *Policy) addMember(id string, annualLimit, lifetimeLimit int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.members[id]; ok {
		return newErr(ErrMemberDuplicate, "成员 %s", id)
	}
	p.members[id] = &Member{
		id:            id,
		annual:        newLimitSeries(annualLimit),
		lifetimeLimit: lifetimeLimit,
		yearUsed:      make(map[int64]int64),
		itemUsed:      make(map[string]map[int64]int64),
	}
	return nil
}

func (p *Policy) addItem(id string, annualLimit int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.items[id]; ok {
		return newErr(ErrItemDuplicate, "项目 %s", id)
	}
	p.items[id] = &Item{id: id, annual: newLimitSeries(annualLimit)}
	return nil
}

// 以下四个 remaining 帮助函数是结算路径上仅有的账簿读取点，
// 每次调用按固定次数累加 ops，用于证明结算开销只与本笔明细数有关。

func (p *Policy) itemRemaining(m *Member, it *Item, y int64) int64 {
	p.ops += 2
	return max(0, it.annual.at(y)-m.itemUsed[it.id][y])
}

func (p *Policy) memberRemaining(m *Member, y int64) int64 {
	p.ops += 2
	return max(0, m.annual.at(y)-m.yearUsed[y])
}

func (p *Policy) familyRemaining(y int64) int64 {
	p.ops += 2
	return max(0, p.family.at(y)-p.familyUsed[y])
}

func (p *Policy) lifetimeRemaining(m *Member) int64 {
	p.ops++
	return m.lifetimeLimit - m.lifetimeUsed
}

func (p *Policy) addItemUsed(m *Member, itemID string, y, pay int64) {
	p.ops++
	ym := m.itemUsed[itemID]
	if ym == nil {
		ym = make(map[int64]int64)
		m.itemUsed[itemID] = ym
	}
	ym[y] += pay
}
