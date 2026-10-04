package settle

import (
	"errors"
	"fmt"

	"ontology/fund"
)

// Settle 执行一笔结算并返回各方支付与全部中间量。
// 拒绝次序：参数非法 > 时钟回退 > 参保人不存在 > 结算号重复 >
// 目录项不存在（最小下标）> 年度已关闭。
func (e *Engine) Settle(now int64, id, person string, year int, items []Item) (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.touched = 0

	lines, ok := validateItems(items)
	if now < 0 || now > maxNow || id == "" || person == "" ||
		year < 1 || year > 9999 || !ok {
		return nil, ErrInvalidParam
	}
	if now < e.clock {
		return nil, ErrClockRollback
	}
	if !e.led.HasPerson(person) {
		return nil, ErrPersonNotFound
	}
	if _, dup := e.ids[id]; dup {
		return nil, ErrDuplicateSettlement
	}
	// 目录项校验：报下标最小的缺失项。
	missing := -1
	for i, line := range lines {
		if _, found := e.cat.Get(line.Code); !found {
			missing = i
			break
		}
	}
	if missing >= 0 {
		return nil, fmt.Errorf("%w: index %d", ErrItemNotFound, missing)
	}
	before, err := e.led.Use(person, year)
	if err != nil {
		if errors.Is(err, fund.ErrYearClosed) {
			return nil, ErrYearClosed
		}
		return nil, ErrInvalidParam
	}

	var A, E2 int64
	for _, line := range lines {
		am, calcErr := e.cat.CalcLine(line)
		if calcErr != nil {
			return nil, ErrInvalidParam
		}
		A += am.A
		E2 += am.E
	}

	res := e.compute(now, id, person, year, A, E2, before, e.aid[person])

	delta := fund.Acc{Du: res.G, X: res.X, F: res.F1, P: res.P, Q: res.F3}
	if err := e.led.Commit(person, year, delta); err != nil {
		return nil, ErrInvalidParam
	}
	after, _ := e.led.Snapshot(person, year)
	res.After = after

	rec := &record{id: id, person: person, year: year, before: before, result: res}
	// 只做压栈，不遍历任何历史结算记录，故 touched 恒为 0。
	e.ids[id] = rec
	e.open[person] = append(e.open[person], rec)
	e.clock = now
	return res, nil
}

func (e *Engine) compute(now int64, id, person string, year int, A, E2 int64, before fund.Acc, aidOn bool) *Result {
	// 基本医保：起付线 + 三段累计区间。
	g := min64(E2, e.d-before.Du)
	x := E2 - g
	m1, m2, m3 := segments(before.X, before.X+x, e.s1, e.s2)
	f := (m1*int64(e.r1) + m2*int64(e.r2) + m3*int64(e.r3)) / 100
	f1 := min64(f, e.capF-before.F)

	// 大病保险：只结算本次新越线部分。
	p := E2 - f1
	y := max64(0, before.P+p-e.d2) - max64(0, before.P-e.d2)
	f2 := y * int64(e.rd) / 100

	// 医疗救助：仅结算当时身份有效。
	z := E2 - f1 - f2
	var f3 int64
	if aidOn {
		f3 = min64(z*int64(e.ra)/100, e.capA-before.Q)
	}

	self := A - f1 - f2 - f3
	return &Result{
		Now: now, ID: id, Person: person, Year: year,
		A: A, E: E2,
		G: g, X: x, M1: m1, M2: m2, M3: m3,
		F1: f1, P: p, Y: y, F2: f2, Z: z, F3: f3,
		Self: self, AidOn: aidOn, Before: before,
	}
}

// Reverse 冲正指定结算；仅允许冲正该参保人最后一笔尚未冲正的结算。
// 拒绝次序：参数非法 > 时钟回退 > 结算不存在 > 已冲正 > 非最后一笔。
func (e *Engine) Reverse(now int64, id string) (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.touched = 0

	if now < 0 || now > maxNow || id == "" {
		return nil, ErrInvalidParam
	}
	if now < e.clock {
		return nil, ErrClockRollback
	}
	rec, ok := e.ids[id]
	if !ok {
		return nil, ErrSettlementNotFound
	}
	if rec.reversed {
		return nil, ErrAlreadyReversed
	}
	stack := e.open[rec.person]
	e.touched++ // 只读取栈顶这一条历史结算记录
	top := stack[len(stack)-1]
	if top.id != id {
		return nil, ErrNotLast
	}

	after, err := e.led.Restore(rec.person, rec.year, rec.before)
	if err != nil {
		return nil, ErrInvalidParam
	}
	rec.reversed = true
	e.open[rec.person] = stack[:len(stack)-1]
	e.clock = now

	out := *rec.result
	out.After = after
	return &out, nil
}

// Acc 读取某人某年当前五元组（测试与外部核对用）。
func (e *Engine) Acc(person string, year int) (fund.Acc, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.led.Snapshot(person, year)
}

// Aid 读取当前救助身份（测试用）。
func (e *Engine) Aid(person string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.aid[person]
}

// Clock 读取已接受操作的最大 now（测试用）。
func (e *Engine) Clock() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

// touchedForTest 返回当前 touched 计数（包内测试用）。
func (e *Engine) touchedForTest() int {
	return e.touched
}
