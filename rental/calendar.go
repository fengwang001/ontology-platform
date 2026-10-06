package rental

// occKind 区分一夜被封锁还是被预订占用。
type occKind int

const (
	occBlock occKind = iota
	occBooking
)

type occRef struct {
	kind occKind
	id   int
}

type block struct {
	id    int
	start int
	end   int // 占用 [start, end)
}

// listing 是单个房源的日历。占用按夜存放在哈希表中，
// 因此可订性判定的开销只与候选夜数、间隙与最短入住配置有关，
// 与历史预订总数无关；lookups 计数器用于在测试中验证这一点。
type listing struct {
	gap        int            // 换客间隙天数
	minStay    map[int]int    // 按日覆盖的最短入住夜数，缺省为 1
	maxMinStay int            // 当前所有最短入住设定的最大值
	nights     map[int]occRef // 夜 -> 占用
	blocks     map[int]*block
	bookings   map[int]*Booking
	lookups    int // 可订性判定中的夜表查询次数（测试探针）
}

func newListing() *listing {
	return &listing{
		minStay:    make(map[int]int),
		maxMinStay: 1,
		nights:     make(map[int]occRef),
		blocks:     make(map[int]*block),
		bookings:   make(map[int]*Booking),
	}
}

func (l *listing) minStayAt(day int) int {
	if v, ok := l.minStay[day]; ok {
		return v
	}
	return 1
}

func (l *listing) occAt(day int) (occRef, bool) {
	l.lookups++
	r, ok := l.nights[day]
	return r, ok
}

// activeBooking 判断某预订占用在 now 下是否有效；exclude 用于
// 修改预订时在"原预订已释放"的日历上判定，即排除其自身。
func (l *listing) activeBooking(id, now, exclude int) bool {
	if id == exclude {
		return false
	}
	b := l.bookings[id]
	return b != nil && b.active(now)
}

func (l *listing) occupy(start, end int, r occRef) {
	for d := start; d < end; d++ {
		l.nights[d] = r
	}
}

func (l *listing) release(start, end int, r occRef) {
	for d := start; d < end; d++ {
		if cur, ok := l.nights[d]; ok && cur == r {
			delete(l.nights, d)
		}
	}
}

// check 按固定次序判定 [cin, cout) 在时刻 now 的可订性，
// 只报告第一个错误。开销上界：
// 2*(cout-cin) + 2*gap + 2*(gap+maxMinStay) 次夜表查询。
func (l *listing) check(cin, cout, now, exclude int) *Error {
	for d := cin; d < cout; d++ {
		if r, ok := l.occAt(d); ok && r.kind == occBlock {
			return newErr(CodeBlocked, "日期与封锁区间相交")
		}
	}
	for d := cin; d < cout; d++ {
		if r, ok := l.occAt(d); ok && r.kind == occBooking && l.activeBooking(r.id, now, exclude) {
			return newErr(CodeConflict, "日期与有效预订相交")
		}
	}
	for d := cin - l.gap; d < cin; d++ {
		if r, ok := l.occAt(d); ok && r.kind == occBooking && l.activeBooking(r.id, now, exclude) {
			return newErr(CodeGap, "与前一笔预订的换客间隙不足")
		}
	}
	for d := cout; d < cout+l.gap; d++ {
		if r, ok := l.occAt(d); ok && r.kind == occBooking && l.activeBooking(r.id, now, exclude) {
			return newErr(CodeGap, "与后一笔预订的换客间隙不足")
		}
	}
	if cout-cin < l.minStayAt(cin) {
		return newErr(CodeMinStay, "不足入住日适用的最短入住夜数")
	}
	if err := l.orphanBefore(cin, now, exclude); err != nil {
		return err
	}
	if err := l.orphanAfter(cout, now, exclude); err != nil {
		return err
	}
	return nil
}

// orphanBefore 检查候选预订是否在其前方留下孤夜。
// 空档 = 相邻两笔有效预订之间扣除换客间隙后的空夜数；
// 空档首日为 cin-void（间隙夜归在前一笔预订的退房侧）。
// 与封锁相邻的空档不受约束。扫描至多 gap+maxMinStay 夜。
func (l *listing) orphanBefore(cin, now, exclude int) *Error {
	free := 0
	limit := cin - l.gap - l.maxMinStay
	for d := cin - 1; d >= 0 && d >= limit; d-- {
		r, ok := l.occAt(d)
		if !ok {
			free++
			continue
		}
		if r.kind == occBlock {
			return nil
		}
		if !l.activeBooking(r.id, now, exclude) {
			free++
			continue
		}
		void := free - l.gap
		if void > 0 && void < l.minStayAt(cin-void) {
			return newErr(CodeOrphan, "在前方留下不足最短入住的孤夜")
		}
		return nil
	}
	return nil
}

// orphanAfter 与 orphanBefore 对称，检查候选预订后方的空档；
// 空档首日为 cout（间隙夜归在后一笔预订的入住侧）。
func (l *listing) orphanAfter(cout, now, exclude int) *Error {
	free := 0
	limit := cout + l.gap + l.maxMinStay
	for d := cout; d < limit; d++ {
		r, ok := l.occAt(d)
		if !ok {
			free++
			continue
		}
		if r.kind == occBlock {
			return nil
		}
		if !l.activeBooking(r.id, now, exclude) {
			free++
			continue
		}
		void := free - l.gap
		if void > 0 && void < l.minStayAt(cout) {
			return newErr(CodeOrphan, "在后方留下不足最短入住的孤夜")
		}
		return nil
	}
	return nil
}
