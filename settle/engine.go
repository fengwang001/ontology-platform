package settle

import (
	"sync"

	"ontology/catalog"
	"ontology/fund"
)

// Item 一笔结算中的一条明细。
type Item struct {
	Code     string
	Price    int64 // 单价（分）
	Quantity int64 // 数量
}

// Result 一笔结算的全部输入金额、中间量与三层支付结果。
type Result struct {
	Now    int64
	ID     string
	Person string
	Year   int

	A int64 // 明细总额
	E int64 // 合规额合计

	G  int64 // 本笔划入起付线金额
	X  int64 // 本笔进入分段金额
	M1 int64 // 落在 [0,S1) 的长度
	M2 int64 // 落在 [S1,S2) 的长度
	M3 int64 // 落在 [S2,∞) 的长度

	F1 int64 // 基本医保支付
	P  int64 // 本次大病前合规自付
	Y  int64 // 本次新超大病起付金额
	F2 int64 // 大病保险支付
	Z  int64 // 本次大病后剩余合规自付
	F3 int64 // 医疗救助支付

	Self int64 // 个人支付 = A-F1-F2-F3

	AidOn bool // 结算当时是否有救助身份

	Before fund.Acc // 结算前年度五元组
	After  fund.Acc // 结算后年度五元组
}

// record 一笔结算记录；冲正后保留在全局索引中，但从该人的未冲正栈弹出。
type record struct {
	id       string
	person   string
	year     int
	reversed bool
	before   fund.Acc
	result   *Result
}

// Engine 医保结算引擎；单把互斥锁串行化全部操作。
type Engine struct {
	mu sync.Mutex

	cat *catalog.Catalog
	led *fund.Ledger

	d    int64
	s1   int64
	s2   int64
	r1   int
	r2   int
	r3   int
	capF int64
	d2   int64
	rd   int
	ra   int
	capA int64

	aid   map[string]bool
	clock int64

	// ids 全局结算号索引（含已冲正记录，用于拒绝重复结算/重复冲正）。
	ids map[string]*record
	// open 每位参保人的未冲正结算栈（后进先出）。
	open map[string][]*record

	// touched 非导出计数器：本次操作读取的历史结算记录数。
	// 每次 Settle/Reverse 入口清零。
	touched int
}

// New 创建引擎：年度起付线 D、分段界 S1<S2、基本医保封顶 CapF、
// 大病起付 D2、救助封顶 CapA（均为 0..1e13 分），
// r1/r2/r3/rd/ra 为 0..100 的整数百分数。
func New(D, S1, S2 int64, r1, r2, r3 int, CapF, D2 int64, rd, ra int, CapA int64) (*Engine, error) {
	for _, v := range []int64{D, S1, S2, CapF, D2, CapA} {
		if v < 0 || v > maxMoney {
			return nil, ErrInvalidParam
		}
	}
	if S1 >= S2 {
		return nil, ErrInvalidParam
	}
	for _, r := range []int{r1, r2, r3, rd, ra} {
		if r < 0 || r > 100 {
			return nil, ErrInvalidParam
		}
	}
	return &Engine{
		cat: catalog.New(), led: fund.New(),
		d: D, s1: S1, s2: S2, r1: r1, r2: r2, r3: r3,
		capF: CapF, d2: D2, rd: rd, ra: ra, capA: CapA,
		aid:  make(map[string]bool),
		ids:  make(map[string]*record),
		open: make(map[string][]*record),
	}, nil
}

// AddItem 追加目录项（class 为甲、乙、丙）。
// p 为乙类先行自付百分数：乙类要求 1..99，甲/丙必须为 0；limit 为限价，0 表示无限价。
func (e *Engine) AddItem(code, class string, p int, limit int64) error {
	if err := e.cat.AddItem(code, class, p, limit); err != nil {
		return ErrInvalidParam
	}
	return nil
}

// AddPerson 登记参保人；重复登记视为参数非法。
func (e *Engine) AddPerson(person string) error {
	if person == "" {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.led.HasPerson(person) {
		return ErrInvalidParam
	}
	return e.led.AddPerson(person)
}

// SetAid 设置救助身份，只影响其后的结算；同样受单调时钟约束。
func (e *Engine) SetAid(now int64, person string, on bool) error {
	if now < 0 || now > maxNow || person == "" {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.clock {
		return ErrClockRollback
	}
	if !e.led.HasPerson(person) {
		return ErrPersonNotFound
	}
	e.aid[person] = on
	e.clock = now
	return nil
}

func validateItems(items []Item) ([]catalog.Line, bool) {
	if len(items) < 1 || len(items) > 200 {
		return nil, false
	}
	lines := make([]catalog.Line, len(items))
	for i, it := range items {
		if it.Code == "" || it.Price < 1 || it.Price > 1_000_000_000 ||
			it.Quantity < 1 || it.Quantity > 10_000 {
			return nil, false
		}
		lines[i] = catalog.Line{Code: it.Code, Price: it.Price, Quantity: it.Quantity}
	}
	return lines, true
}
