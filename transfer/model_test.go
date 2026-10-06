package transfer

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// ---------- 独立编写的朴素模型 ----------
// 与 System 的实现不共享任何代码：用最直接的查表与循环表达同一份规格，
// 用于对拍。模型只在乎"结果对不对"，不在乎性能。

type modelLine struct {
	product  string
	qty      int64
	received int64
	shortage int64
	surplus  int64
}

type modelOrder struct {
	src, dst  string
	lines     []modelLine
	status    OrderStatus
	shippedAt int64
}

type naiveModel struct {
	permille  int64
	closeWait int64
	stock     map[string]map[string][2]int64 // 仓 -> 商品 -> [可用, 冻结]
	orders    map[string]*modelOrder
	last      int64
}

func newNaiveModel(permille, closeWait int64, initial map[string]map[string]int64) *naiveModel {
	m := &naiveModel{
		permille:  permille,
		closeWait: closeWait,
		stock:     map[string]map[string][2]int64{},
		orders:    map[string]*modelOrder{},
		last:      -1,
	}
	for wh, stocks := range initial {
		m.stock[wh] = map[string][2]int64{}
		for p, q := range stocks {
			m.stock[wh][p] = [2]int64{q, 0}
		}
	}
	return m
}

func (m *naiveModel) clockOk(now int64) error {
	if now < 0 {
		return ErrInvalidParam
	}
	if now < m.last {
		return ErrClockRegression
	}
	return nil
}

func (m *naiveModel) get(wh, p string) [2]int64 {
	return m.stock[wh][p]
}

func (m *naiveModel) set(wh, p string, v [2]int64) {
	m.stock[wh][p] = v
}

func (m *naiveModel) create(now int64, id, src, dst string, lines []Line) error {
	if id == "" || src == "" || dst == "" || src == dst || len(lines) == 0 {
		return ErrInvalidParam
	}
	seen := map[string]bool{}
	for _, ln := range lines {
		if ln.Product == "" || ln.Qty <= 0 || seen[ln.Product] {
			return ErrInvalidParam
		}
		seen[ln.Product] = true
	}
	if _, ok := m.stock[src]; !ok {
		return ErrInvalidParam
	}
	if _, ok := m.stock[dst]; !ok {
		return ErrInvalidParam
	}
	if _, ok := m.orders[id]; ok {
		return ErrDuplicateOrder
	}
	if err := m.clockOk(now); err != nil {
		return err
	}
	for i, ln := range lines {
		if m.get(src, ln.Product)[0] < ln.Qty {
			return &InsufficientStockError{LineIndex: i, Product: ln.Product, Want: ln.Qty, Have: m.get(src, ln.Product)[0]}
		}
	}
	o := &modelOrder{src: src, dst: dst, status: StatusCreated}
	for _, ln := range lines {
		v := m.get(src, ln.Product)
		m.set(src, ln.Product, [2]int64{v[0] - ln.Qty, v[1] + ln.Qty})
		o.lines = append(o.lines, modelLine{product: ln.Product, qty: ln.Qty})
	}
	m.orders[id] = o
	if now > m.last {
		m.last = now
	}
	return nil
}

// ---------- 对拍驱动 ----------

// errKey 把错误归一化为可比较的字符串：类别 + 关键字段（判定依据）。
func errKey(err error) string {
	if err == nil {
		return "OK"
	}
	var ise *InsufficientStockError
	if errors.As(err, &ise) {
		return fmt.Sprintf("InsufficientStock(line=%d,want=%d,have=%d)", ise.LineIndex, ise.Want, ise.Have)
	}
	var ore *OverReceiveError
	if errors.As(err, &ore) {
		return fmt.Sprintf("OverReceive(line=%d,limit=%d,attempt=%d)", ore.LineIndex, ore.Limit, ore.Attempt)
	}
	var ncy *NotClosableYetError
	if errors.As(err, &ncy) {
		return fmt.Sprintf("NotClosableYet(now=%d,earliest=%d)", ncy.Now, ncy.Earliest)
	}
	var ree *RecoverExcessError
	if errors.As(err, &ree) {
		return fmt.Sprintf("RecoverExcess(line=%d,max=%d,attempt=%d)", ree.LineIndex, ree.Max, ree.Attempt)
	}
	for _, sentinel := range []error{
		ErrInvalidParam, ErrDuplicateOrder, ErrClockRegression, ErrOrderNotFound,
		ErrOrderNotShipped, ErrOrderAlreadyShipped, ErrOrderClosed, ErrOrderCancelled,
		ErrOrderNotClosed, ErrNoShortage,
	} {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}
	return "UNKNOWN:" + err.Error()
}

// op 是一条可重放的操作记录。
type op struct {
	kind      string
	now       int64
	id        string
	src, dst  string
	lines     []Line
	lineIndex int
	qty       int64
}

func (o op) String() string {
	switch o.kind {
	case "create":
		return fmt.Sprintf("CreateOrder(now=%d id=%s src=%s dst=%s lines=%v)", o.now, o.id, o.src, o.dst, o.lines)
	case "receive", "recover":
		return fmt.Sprintf("%s(now=%d id=%s line=%d qty=%d)", o.kind, o.now, o.id, o.lineIndex, o.qty)
	default:
		return fmt.Sprintf("%s(now=%d id=%s)", o.kind, o.now, o.id)
	}
}

func runOp(s *System, o op) error {
	switch o.kind {
	case "create":
		return s.CreateOrder(o.now, o.id, o.src, o.dst, o.lines)
	case "cancel":
		return s.CancelOrder(o.now, o.id)
	case "ship":
		return s.ShipOrder(o.now, o.id)
	case "receive":
		return s.Receive(o.now, o.id, o.lineIndex, o.qty)
	case "close":
		return s.CloseOrder(o.now, o.id)
	case "recover":
		return s.Recover(o.now, o.id, o.lineIndex, o.qty)
	}
	panic("unknown op " + o.kind)
}

func runOpModel(m *naiveModel, o op) error {
	switch o.kind {
	case "create":
		return m.create(o.now, o.id, o.src, o.dst, o.lines)
	case "cancel":
		return m.cancel(o.now, o.id)
	case "ship":
		return m.ship(o.now, o.id)
	case "receive":
		return m.receive(o.now, o.id, o.lineIndex, o.qty)
	case "close":
		return m.close(o.now, o.id)
	case "recover":
		return m.recover(o.now, o.id, o.lineIndex, o.qty)
	}
	panic("unknown op " + o.kind)
}

var (
	genWarehouses = []string{"W1", "W2", "W3", "WX"} // WX 不存在
	genProducts   = []string{"A", "B", "C"}
)

func genOps(r *rand.Rand, n int) []op {
	ops := make([]op, 0, n)
	var clock int64
	for i := 0; i < n; i++ {
		// 时钟：多数前进 0~3 秒，小概率回退。
		now := clock + r.Int63n(4)
		if r.Intn(20) == 0 && clock > 2 {
			now = clock - 1 - r.Int63n(2)
		}
		kind := []string{"create", "create", "cancel", "ship", "ship", "receive", "receive", "receive", "close", "recover"}[r.Intn(10)]
		o := op{kind: kind, now: now}
		o.id = fmt.Sprintf("T%d", r.Intn(12))
		if r.Intn(30) == 0 {
			o.id = "NOPE" // 不存在的单
		}
		o.src = genWarehouses[r.Intn(len(genWarehouses))]
		o.dst = genWarehouses[r.Intn(len(genWarehouses))]
		o.lineIndex = r.Intn(4) - 1   // 可能越界
		o.qty = int64(r.Intn(25) - 2) // 可能非正
		if kind == "create" {
			nLines := 1 + r.Intn(3)
			products := append([]string(nil), genProducts...)
			r.Shuffle(len(products), func(a, b int) { products[a], products[b] = products[b], products[a] })
			for j := 0; j < nLines; j++ {
				p := products[j%len(products)]
				if r.Intn(15) == 0 {
					p = products[0] // 可能制造重复商品
				}
				o.lines = append(o.lines, Line{Product: p, Qty: int64(r.Intn(20) + 1)})
			}
			if r.Intn(20) == 0 {
				o.lines = append(o.lines, Line{Product: "A", Qty: 0}) // 非法行
			}
		}
		ops = append(ops, o)
		if now > clock {
			clock = now
		}
	}
	return ops
}

// stateKey 抓取 System 全量可观察状态，用于与模型逐位比对。
func stateKey(s *System, warehouses, products, orderIDs []string) string {
	var b []byte
	for _, wh := range warehouses {
		for _, p := range products {
			snap, ok := s.Stock(wh, p)
			b = append(b, fmt.Sprintf("%s/%s:%v:%d,%d|", wh, p, ok, snap.Available, snap.Frozen)...)
		}
	}
	for _, id := range orderIDs {
		lines, status, err := s.OrderLines(id)
		b = append(b, fmt.Sprintf("%s:%v:%v:%v|", id, status, errKey(err), lines)...)
	}
	return string(b)
}

func modelStateKey(m *naiveModel, warehouses, products, orderIDs []string) string {
	var b []byte
	for _, wh := range warehouses {
		_, ok := m.stock[wh]
		for _, p := range products {
			v := m.get(wh, p)
			b = append(b, fmt.Sprintf("%s/%s:%v:%d,%d|", wh, p, ok, v[0], v[1])...)
		}
	}
	for _, id := range orderIDs {
		o, ok := m.orders[id]
		if !ok {
			b = append(b, fmt.Sprintf("%s:%v:%v:%v|", id, StatusCreated, errKey(ErrOrderNotFound), "[]")...)
			continue
		}
		lines := make([]LineStatus, len(o.lines))
		for i, ln := range o.lines {
			lines[i] = LineStatus{Product: ln.product, Shipped: ln.qty, Received: ln.received, Shortage: ln.shortage, Surplus: ln.surplus}
		}
		b = append(b, fmt.Sprintf("%s:%v:%v:%v|", id, o.status, "OK", lines)...)
	}
	return string(b)
}

// TestModelDifferential 与朴素模型对照大量随机操作序列，
// 日志打印输入、输出与判定依据；每步之后校验守恒与全量状态一致。
func TestModelDifferential(t *testing.T) {
	initial := map[string]map[string]int64{
		"W1": {"A": 60, "B": 40, "C": 20},
		"W2": {"A": 30, "B": 10},
		"W3": {"C": 50},
	}
	warehouses := []string{"W1", "W2", "W3", "WX"}
	products := []string{"A", "B", "C"}
	orderIDs := make([]string, 0, 13)
	for i := 0; i < 12; i++ {
		orderIDs = append(orderIDs, fmt.Sprintf("T%d", i))
	}
	orderIDs = append(orderIDs, "NOPE")

	for seed := int64(0); seed < 30; seed++ {
		r := rand.New(rand.NewSource(seed))
		ops := genOps(r, 400)
		s, err := NewSystem(Config{OverReceiptTolerancePermille: 150, CloseWaitSeconds: 8}, initial)
		if err != nil {
			t.Fatal(err)
		}
		replay, err := NewSystem(Config{OverReceiptTolerancePermille: 150, CloseWaitSeconds: 8}, initial)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaiveModel(150, 8, initial)

		for i, o := range ops {
			gotSys := runOp(s, o)
			gotReplay := runOp(replay, o)
			gotModel := runOpModel(m, o)

			ks, kr, km := errKey(gotSys), errKey(gotReplay), errKey(gotModel)
			t.Logf("seed=%d op=%03d in=%s out=%s model=%s", seed, i, o, ks, km)
			if ks != km {
				t.Fatalf("seed=%d op=%03d %s\nsystem: %s\nmodel:  %s", seed, i, o, ks, km)
			}
			if ks != kr {
				t.Fatalf("seed=%d op=%03d %s\nreplay mismatch: %s vs %s", seed, i, o, ks, kr)
			}
			if gotSys == nil {
				ok, entries := s.VerifyConservation()
				if !ok {
					t.Fatalf("seed=%d op=%03d %s\nconservation violated: %+v", seed, i, o, entries)
				}
			}
		}
		ks := stateKey(s, warehouses, products, orderIDs)
		km := modelStateKey(m, warehouses, products, orderIDs)
		if ks != km {
			t.Fatalf("seed=%d final state mismatch\nsystem: %s\nmodel:  %s", seed, ks, km)
		}
		if kr := stateKey(replay, warehouses, products, orderIDs); kr != ks {
			t.Fatalf("seed=%d replay state mismatch", seed)
		}
		t.Logf("seed=%d final state: %s", seed, ks)
	}
}

func (m *naiveModel) cancel(now int64, id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := m.clockOk(now); err != nil {
		return err
	}
	o, ok := m.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	switch o.status {
	case StatusShipped:
		return ErrOrderAlreadyShipped
	case StatusClosed:
		return ErrOrderClosed
	case StatusCancelled:
		return ErrOrderCancelled
	}
	for _, ln := range o.lines {
		v := m.get(o.src, ln.product)
		m.set(o.src, ln.product, [2]int64{v[0] + ln.qty, v[1] - ln.qty})
	}
	o.status = StatusCancelled
	if now > m.last {
		m.last = now
	}
	return nil
}

func (m *naiveModel) ship(now int64, id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := m.clockOk(now); err != nil {
		return err
	}
	o, ok := m.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	switch o.status {
	case StatusShipped:
		return ErrOrderAlreadyShipped
	case StatusClosed:
		return ErrOrderClosed
	case StatusCancelled:
		return ErrOrderCancelled
	}
	for _, ln := range o.lines {
		v := m.get(o.src, ln.product)
		m.set(o.src, ln.product, [2]int64{v[0], v[1] - ln.qty})
	}
	o.status = StatusShipped
	o.shippedAt = now
	if now > m.last {
		m.last = now
	}
	return nil
}

func (m *naiveModel) receive(now int64, id string, lineIndex int, qty int64) error {
	if id == "" || qty <= 0 {
		return ErrInvalidParam
	}
	if err := m.clockOk(now); err != nil {
		return err
	}
	o, ok := m.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	if lineIndex < 0 || lineIndex >= len(o.lines) {
		return ErrInvalidParam
	}
	switch o.status {
	case StatusCreated:
		return ErrOrderNotShipped
	case StatusClosed:
		return ErrOrderClosed
	case StatusCancelled:
		return ErrOrderCancelled
	}
	ln := &o.lines[lineIndex]
	limit := ln.qty + ln.qty*m.permille/1000
	if ln.received+qty > limit {
		return &OverReceiveError{LineIndex: lineIndex, Limit: limit, Attempt: ln.received + qty}
	}
	before := ln.received - ln.qty
	if before < 0 {
		before = 0
	}
	after := ln.received + qty - ln.qty
	if after < 0 {
		after = 0
	}
	ln.surplus += after - before
	ln.received += qty
	v := m.get(o.dst, ln.product)
	m.set(o.dst, ln.product, [2]int64{v[0] + qty, v[1]})
	if now > m.last {
		m.last = now
	}
	return nil
}

func (m *naiveModel) close(now int64, id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := m.clockOk(now); err != nil {
		return err
	}
	o, ok := m.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	switch o.status {
	case StatusCreated:
		return ErrOrderNotShipped
	case StatusClosed:
		return ErrOrderClosed
	case StatusCancelled:
		return ErrOrderCancelled
	}
	all := true
	for _, ln := range o.lines {
		if ln.received < ln.qty {
			all = false
		}
	}
	if !all && now-o.shippedAt < m.closeWait {
		return &NotClosableYetError{Now: now, Earliest: o.shippedAt + m.closeWait}
	}
	for i := range o.lines {
		if o.lines[i].qty > o.lines[i].received {
			o.lines[i].shortage = o.lines[i].qty - o.lines[i].received
		}
	}
	o.status = StatusClosed
	if now > m.last {
		m.last = now
	}
	return nil
}

func (m *naiveModel) recover(now int64, id string, lineIndex int, qty int64) error {
	if id == "" || qty <= 0 {
		return ErrInvalidParam
	}
	if err := m.clockOk(now); err != nil {
		return err
	}
	o, ok := m.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	if lineIndex < 0 || lineIndex >= len(o.lines) {
		return ErrInvalidParam
	}
	switch o.status {
	case StatusCancelled:
		return ErrOrderCancelled
	case StatusCreated, StatusShipped:
		return ErrOrderNotClosed
	}
	ln := &o.lines[lineIndex]
	if ln.shortage == 0 {
		return ErrNoShortage
	}
	if qty > ln.shortage {
		return &RecoverExcessError{LineIndex: lineIndex, Max: ln.shortage, Attempt: qty}
	}
	ln.shortage -= qty
	v := m.get(o.dst, ln.product)
	m.set(o.dst, ln.product, [2]int64{v[0] + qty, v[1]})
	if now > m.last {
		m.last = now
	}
	return nil
}
