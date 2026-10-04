package settle

import (
	"sync"

	"ontology/catalog"
	"ontology/fund"
)

// Item 为一条结算明细。
type Item struct {
	Code  string
	Price int64 // 单价（分），1..1e9
	Qty   int64 // 数量，1..1e4
}

// Result 为一笔结算的输出。
type Result struct {
	A    int64 // 总费用 Σa
	E    int64 // 合规额 Σe
	G    int64 // 本笔划入起付金额
	X    int64 // 本笔进入分段金额
	F1   int64 // 基本医保支付
	F2   int64 // 大病保险支付
	F3   int64 // 医疗救助支付
	Self int64 // 个人支付
	M1   int64 // 落在 [0,S1) 的长度
	M2   int64 // 落在 [S1,S2) 的长度
	M3   int64 // 落在 S2 及以上的长度
	P    int64 // 大病支付前的自付
	Y    int64 // 跨过大病起付的合规自付增量
	Z    int64 // 救助前剩余个人负担
	Aid  bool  // 结算时救助身份快照
}

// params 为政策参数。
type params struct {
	d, s1, s2  int64
	r1, r2, r3 int64
	capF       int64
	d2         int64
	rd, ra     int64
	capA       int64
}

// rec 为一笔结算记录（含冲正恢复所需快照）。
type rec struct {
	id        string
	person    string
	year      int
	aid       bool
	snap      fund.Acc
	prevMax   int
	g, x      int64
	f1, p, f3 int64
	reversed  bool
}

// Engine 为医保结算引擎。
type Engine struct {
	mu     sync.Mutex
	cat    *catalog.Catalog
	fund   *fund.Fund
	par    params
	now    int64
	people map[string]bool
	aid    map[string]bool
	byID   map[string]*rec
	stacks map[string][]*rec // 每人活跃（未冲正）记录，LIFO

	// touched 非导出：统计逐份翻阅参保人历史结算记录的份数。
	// Settle 为 0（不翻阅）；Reverse 只触达栈顶一份（≤1）。
	touched int
}

const maxMoney = 10_000_000_000_000 // 1e13

// New 创建引擎；参数非法时 panic（构造函数不返回错误）。
func New(D, S1, S2, r1, r2, r3, CapF, D2, rd, ra, CapA int64) *Engine {
	if !validParams(D, S1, S2, r1, r2, r3, CapF, D2, rd, ra, CapA) {
		panic(ErrInvalid)
	}
	return &Engine{
		cat:    catalog.New(),
		fund:   fund.New(),
		par:    params{d: D, s1: S1, s2: S2, r1: r1, r2: r2, r3: r3, capF: CapF, d2: D2, rd: rd, ra: ra, capA: CapA},
		people: make(map[string]bool),
		aid:    make(map[string]bool),
		byID:   make(map[string]*rec),
		stacks: make(map[string][]*rec),
	}
}

func moneyOK(v int64) bool { return v >= 0 && v <= maxMoney }

func pctOK(v int64) bool { return v >= 0 && v <= 100 }

// validParams 校验构造参数。
func validParams(D, S1, S2, r1, r2, r3, CapF, D2, rd, ra, CapA int64) bool {
	if !(moneyOK(D) && moneyOK(S1) && moneyOK(S2) && moneyOK(CapF) && moneyOK(D2) && moneyOK(CapA)) {
		return false
	}
	if !(S1 < S2) {
		return false
	}
	for _, p := range []int64{r1, r2, r3, rd, ra} {
		if !pctOK(p) {
			return false
		}
	}
	return true
}

// AddItem 增加目录项。
func (e *Engine) AddItem(code string, class catalog.Class, p, limit int64) error {
	return e.cat.AddItem(code, class, p, limit)
}

// AddPerson 增加参保人。
func (e *Engine) AddPerson(person string) error {
	if person == "" {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.people[person] {
		return ErrInvalid
	}
	e.people[person] = true
	return nil
}

// SetAid 设置救助身份，只影响其后的结算。
func (e *Engine) SetAid(now int64, person string, on bool) error {
	if now < 0 || now > 1_000_000_000 || person == "" {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.now {
		return ErrClockBack
	}
	if !e.people[person] {
		return ErrNoPerson
	}
	e.now = now
	e.aid[person] = on
	return nil
}

// Settle 执行一笔三层分摊结算。
func (e *Engine) Settle(now int64, id, person string, year int, items []Item) (Result, error) {
	var res Result
	// 1. 参数非法
	if now < 0 || now > 1_000_000_000 || id == "" || person == "" ||
		year < 1 || year > 9999 || len(items) < 1 || len(items) > 200 {
		return res, ErrInvalid
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 2. 时钟回退（拒绝不改时钟）
	if now < e.now {
		return res, ErrClockBack
	}
	// 3. 参保人不存在
	if !e.people[person] {
		return res, ErrNoPerson
	}
	// 4. 结算号重复（map 查找，不翻阅历史）
	if _, dup := e.byID[id]; dup {
		return res, ErrDupID
	}
	// 5. 目录项不存在（报下标最小项）；同时校验明细参数。
	entries := make([]catalog.Item, len(items))
	for i, it := range items {
		if it.Code == "" || it.Price < 1 || it.Price > 1_000_000_000 ||
			it.Qty < 1 || it.Qty > 10_000 {
			return res, ErrInvalid
		}
		ent, ok := e.cat.Get(it.Code)
		if !ok {
			return res, ErrNoItem
		}
		entries[i] = ent
	}
	// 6. 年度已关闭
	if e.fund.Closed(person, year) {
		return res, ErrYearClosed
	}

	// 通过全部校验后才推进时钟。
	e.now = now

	// 逐条计算 a / b / c / e
	var A, E int64
	for i, it := range items {
		ent := entries[i]
		a := it.Price * it.Qty
		unit := it.Price
		if ent.Limit > 0 && unit > ent.Limit {
			unit = ent.Limit
		}
		b := unit * it.Qty
		var c int64
		switch ent.Class {
		case catalog.Yi:
			c = ceilDiv(b*ent.P, 100)
		case catalog.Bing:
			c = b
		default: // 甲类
			c = 0
		}
		A += a
		E += b - c
	}

	snap, prevMax := e.fund.Before(person, year)

	// 基本医保：起付 → 分段 → 合计取整一次 → 封顶截断
	g := int64(0)
	if e.par.d > snap.DU {
		g = min64(E, e.par.d-snap.DU)
	}
	x := E - g
	m1, m2, m3 := segments(snap.X, x, e.par.s1, e.par.s2)
	f := (m1*e.par.r1 + m2*e.par.r2 + m3*e.par.r3) / 100
	f1 := int64(0)
	if e.par.capF > snap.F {
		f1 = min64(f, e.par.capF-snap.F)
	}

	// 大病保险
	p := E - f1
	y := max64(0, snap.P+p-e.par.d2) - max64(0, snap.P-e.par.d2)
	f2 := y * e.par.rd / 100

	// 医疗救助（仅当前有救助身份）
	aid := e.aid[person]
	z := E - f1 - f2
	f3 := int64(0)
	if aid && e.par.capA > snap.Q {
		f3 = min64(z*e.par.ra/100, e.par.capA-snap.Q)
	}

	self := A - f1 - f2 - f3

	// 写入累计
	e.fund.Apply(person, year, g, x, f1, p, f3)
	r := &rec{
		id: id, person: person, year: year, aid: aid,
		snap: snap, prevMax: prevMax,
		g: g, x: x, f1: f1, p: p, f3: f3,
	}
	e.byID[id] = r
	// 入栈只追加引用，不翻阅历史，故 touched 为 0。
	e.stacks[person] = append(e.stacks[person], r)
	e.touched = 0

	return Result{
		A: A, E: E, G: g, X: x,
		F1: f1, F2: f2, F3: f3, Self: self,
		M1: m1, M2: m2, M3: m3, P: p, Y: y, Z: z, Aid: aid,
	}, nil
}

// Reverse 冲正一笔结算（仅允许该人最后一笔未冲正）。
func (e *Engine) Reverse(now int64, id string) error {
	// 1. 参数非法
	if now < 0 || now > 1_000_000_000 || id == "" {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	// 2. 时钟回退
	if now < e.now {
		return ErrClockBack
	}
	// 3. 结算不存在
	r, ok := e.byID[id]
	if !ok {
		return ErrNoSettlement
	}
	// 4. 已冲正（墓碑仍可区分）
	if r.reversed {
		return ErrReversed
	}
	// 5. 非最后一笔：只触达栈顶一份历史记录。
	st := e.stacks[r.person]
	if len(st) == 0 || st[len(st)-1] != r {
		return ErrNotLast
	}

	// 全部通过后才推进时钟并回滚。
	e.now = now
	r.reversed = true
	e.stacks[r.person] = st[:len(st)-1]
	e.fund.Restore(r.person, r.year, r.snap, r.prevMax)
	e.touched = 1
	return nil
}

// Acc 读取某（参保人，年度）五累计量快照（观测用）。
func (e *Engine) Acc(person string, year int) fund.Acc {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fund.Snapshot(person, year)
}

// Touched 观测非导出计数器：最近一次成功操作触达的历史记录份数。
func (e *Engine) Touched() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.touched
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// segments 求增量 x 落在累计区间 [x0,x0+x) 上的三段长度，界为 s1<s2。
func segments(x0, x, s1, s2 int64) (m1, m2, m3 int64) {
	lo, hi := x0, x0+x
	if hi <= s1 {
		return x, 0, 0
	}
	if lo < s1 {
		m1 = s1 - lo
	}
	upper := hi
	if upper > s2 {
		upper = s2
	}
	lower := lo
	if lower < s1 {
		lower = s1
	}
	if upper > lower {
		m2 = upper - lower
	}
	if hi > s2 {
		low3 := lo
		if low3 < s2 {
			low3 = s2
		}
		m3 = hi - low3
	}
	return m1, m2, m3
}
