package repo

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// 朴素模拟：额度与占用每次都从在库明细/未了结回购全量重算，
// 作为引擎增量维护的参照实现。
type mBond struct {
	rate, price int64
}

type mRepo struct {
	acct       string
	repay, due int64
	use, seq   int64
	status     Status
	badDebt    int64
}

type mAcct struct {
	cash    int64
	avail   map[string]int64
	pledged map[string]int64
}

type model struct {
	day, seq int64
	bonds    map[string]mBond
	accts    map[string]*mAcct
	repos    map[string]*mRepo
}

func newModel() *model {
	return &model{
		bonds: make(map[string]mBond),
		accts: make(map[string]*mAcct),
		repos: make(map[string]*mRepo),
	}
}

func (m *model) capOf(acct string) int64 {
	a := m.accts[acct]
	if a == nil {
		return 0
	}
	var cap int64
	for b, n := range a.pledged {
		cap += n * m.bonds[b].rate / 100
	}
	return cap
}

func (m *model) useOf(acct string) int64 {
	var u int64
	for _, r := range m.repos {
		if r.acct == acct && r.status == Outstanding {
			u += r.use
		}
	}
	return u
}

func (m *model) ensure(acct string) *mAcct {
	a := m.accts[acct]
	if a == nil {
		a = &mAcct{avail: make(map[string]int64), pledged: make(map[string]int64)}
		m.accts[acct] = a
	}
	return a
}

func (m *model) advance(day int64) error {
	if day < m.day {
		return ErrDayRollback
	}
	m.day = day
	var due []*mRepo
	for _, r := range m.repos {
		if r.status == Outstanding && r.due <= day {
			due = append(due, r)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].due != due[j].due {
			return due[i].due < due[j].due
		}
		return due[i].seq < due[j].seq
	})
	for _, r := range due {
		m.settleOne(r)
	}
	return nil
}

func (m *model) settleOne(r *mRepo) {
	a := m.accts[r.acct]
	if a.cash >= r.repay {
		a.cash -= r.repay
		r.status = Redeemed
		return
	}
	out := r.repay
	var bs []string
	for b, n := range a.pledged {
		if n > 0 {
			bs = append(bs, b)
		}
	}
	sort.Strings(bs)
	for _, b := range bs {
		if out == 0 {
			break
		}
		price := m.bonds[b].price
		sell := min(a.pledged[b], ceilDiv(out, price))
		a.pledged[b] -= sell
		out -= sell * price
		if out < 0 {
			a.cash += -out
			out = 0
		}
	}
	if out > 0 {
		r.badDebt = out
	}
	r.status = Defaulted
}

func (m *model) AddBond(day int64, bond string, rate, price int64) error {
	if !validDay(day) || bond == "" || rate < 0 || rate > 150 || price < 1 || price > 1_000_000 {
		return ErrParam
	}
	if err := m.advance(day); err != nil {
		return err
	}
	if _, ok := m.bonds[bond]; ok {
		return ErrDuplicate
	}
	m.bonds[bond] = mBond{rate: rate, price: price}
	return nil
}

func (m *model) SetRate(day int64, bond string, rate int64) error {
	if !validDay(day) || bond == "" || rate < 0 || rate > 150 {
		return ErrParam
	}
	if err := m.advance(day); err != nil {
		return err
	}
	b, ok := m.bonds[bond]
	if !ok {
		return ErrNotExist
	}
	b.rate = rate
	m.bonds[bond] = b
	return nil
}

func (m *model) SetPrice(day int64, bond string, price int64) error {
	if !validDay(day) || bond == "" || price < 1 || price > 1_000_000 {
		return ErrParam
	}
	if err := m.advance(day); err != nil {
		return err
	}
	b, ok := m.bonds[bond]
	if !ok {
		return ErrNotExist
	}
	b.price = price
	m.bonds[bond] = b
	return nil
}

func (m *model) Credit(day int64, acct, bond string, n int64) error {
	if !validDay(day) || acct == "" || bond == "" || !validQty(n) {
		return ErrParam
	}
	if err := m.advance(day); err != nil {
		return err
	}
	if _, ok := m.bonds[bond]; !ok {
		return ErrNotExist
	}
	a := m.ensure(acct)
	a.avail[bond] += n
	return nil
}

func (m *model) CreditCash(day int64, acct string, amt int64) error {
	if !validDay(day) || acct == "" || !validQty(amt) {
		return ErrParam
	}
	if err := m.advance(day); err != nil {
		return err
	}
	m.ensure(acct).cash += amt
	return nil
}

func (m *model) PledgeIn(day int64, acct, bond string, n int64) error {
	if !validDay(day) || acct == "" || bond == "" || !validQty(n) {
		return ErrParam
	}
	if err := m.advance(day); err != nil {
		return err
	}
	a := m.accts[acct]
	if a == nil {
		return ErrNotExist
	}
	if _, ok := m.bonds[bond]; !ok {
		return ErrNotExist
	}
	if a.avail[bond] < n {
		return ErrInsufficientAvail
	}
	a.avail[bond] -= n
	a.pledged[bond] += n
	return nil
}

func (m *model) Repo(day int64, id, acct string, amount, days, r int64) error {
	if !validDay(day) || id == "" || acct == "" || !validQty(amount) ||
		days < 1 || days > 365 || r < 0 || r > 10_000 {
		return ErrParam
	}
	if err := m.advance(day); err != nil {
		return err
	}
	a := m.accts[acct]
	if a == nil {
		return ErrNotExist
	}
	if _, dup := m.repos[id]; dup {
		return ErrDuplicate
	}
	cap, use := m.capOf(acct), m.useOf(acct)
	if cap < use {
		return ErrDeficit
	}
	u := ceilDiv(amount, 100)
	if use+u > cap {
		return ErrInsufficientCap
	}
	m.seq++
	m.repos[id] = &mRepo{
		acct:  acct,
		repay: amount + ceilDiv(amount*r*days, 3_650_000),
		due:   day + days,
		use:   u,
		seq:   m.seq,
	}
	a.cash += amount
	return nil
}

func (m *model) PledgeOut(day int64, acct, bond string, n int64) error {
	if !validDay(day) || acct == "" || bond == "" || !validQty(n) {
		return ErrParam
	}
	if err := m.advance(day); err != nil {
		return err
	}
	a := m.accts[acct]
	if a == nil {
		return ErrNotExist
	}
	if _, ok := m.bonds[bond]; !ok {
		return ErrNotExist
	}
	if m.capOf(acct) < m.useOf(acct) {
		return ErrDeficit
	}
	if a.pledged[bond] < n {
		return ErrInsufficientStock
	}
	rate := m.bonds[bond].rate
	newCap := m.capOf(acct) - a.pledged[bond]*rate/100 + (a.pledged[bond]-n)*rate/100
	if newCap < m.useOf(acct) {
		return ErrInsufficientCap
	}
	a.pledged[bond] -= n
	a.avail[bond] += n
	return nil
}

func (m *model) deficits() []Deficit {
	var out []Deficit
	for name := range m.accts {
		if gap := m.useOf(name) - m.capOf(name); gap > 0 {
			out = append(out, Deficit{Acct: []byte(name), Gap: gap})
		}
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i].Acct) < string(out[j].Acct) })
	return out
}

// simOp 为一次随机操作。
type simOp struct {
	kind       string
	day        int64
	s1, s2     string
	v1, v2, v3 int64
}

func (o simOp) String() string {
	return fmt.Sprintf("%s(day=%d %q %q %d %d %d)", o.kind, o.day, o.s1, o.s2, o.v1, o.v2, o.v3)
}

func (o simOp) acct() string {
	switch o.kind {
	case "Credit", "PledgeIn", "PledgeOut":
		return o.s1
	case "CreditCash":
		return o.s1
	case "Repo":
		return o.s2
	}
	return ""
}

func applyEngine(e *Engine, o simOp) error {
	switch o.kind {
	case "AddBond":
		return e.AddBond(o.day, []byte(o.s1), o.v1, o.v2)
	case "SetRate":
		return e.SetRate(o.day, []byte(o.s1), o.v1)
	case "SetPrice":
		return e.SetPrice(o.day, []byte(o.s1), o.v1)
	case "Credit":
		return e.Credit(o.day, []byte(o.s1), []byte(o.s2), o.v1)
	case "CreditCash":
		return e.CreditCash(o.day, []byte(o.s1), o.v1)
	case "PledgeIn":
		return e.PledgeIn(o.day, []byte(o.s1), []byte(o.s2), o.v1)
	case "PledgeOut":
		return e.PledgeOut(o.day, []byte(o.s1), []byte(o.s2), o.v1)
	case "Repo":
		return e.Repo(o.day, []byte(o.s1), []byte(o.s2), o.v1, o.v2, o.v3)
	case "Deficits":
		e.Deficits()
		return nil
	}
	panic("bad kind " + o.kind)
}

func applyModel(m *model, o simOp) error {
	switch o.kind {
	case "AddBond":
		return m.AddBond(o.day, o.s1, o.v1, o.v2)
	case "SetRate":
		return m.SetRate(o.day, o.s1, o.v1)
	case "SetPrice":
		return m.SetPrice(o.day, o.s1, o.v1)
	case "Credit":
		return m.Credit(o.day, o.s1, o.s2, o.v1)
	case "CreditCash":
		return m.CreditCash(o.day, o.s1, o.v1)
	case "PledgeIn":
		return m.PledgeIn(o.day, o.s1, o.s2, o.v1)
	case "PledgeOut":
		return m.PledgeOut(o.day, o.s1, o.s2, o.v1)
	case "Repo":
		return m.Repo(o.day, o.s1, o.s2, o.v1, o.v2, o.v3)
	case "Deficits":
		return nil
	}
	panic("bad kind " + o.kind)
}

func errKey(err error) string {
	switch {
	case err == nil:
		return "OK"
	case errors.Is(err, ErrParam):
		return "参数非法"
	case errors.Is(err, ErrDayRollback):
		return "日期回退"
	case errors.Is(err, ErrNotExist):
		return "不存在"
	case errors.Is(err, ErrDuplicate):
		return "重复"
	case errors.Is(err, ErrDeficit):
		return "欠库"
	case errors.Is(err, ErrInsufficientAvail):
		return "可用不足"
	case errors.Is(err, ErrInsufficientStock):
		return "库存不足"
	case errors.Is(err, ErrInsufficientCap):
		return "标准券不足"
	}
	return "未知:" + err.Error()
}

func genOps(rng *rand.Rand, n int) []simOp {
	accts := []string{"α", "β", "γ", "δ"}
	bonds := []string{"A", "B", "C", "D", "E"}
	pickAcct := func() string {
		if rng.Intn(100) < 3 {
			return "ghost"
		}
		return accts[rng.Intn(len(accts))]
	}
	pickBond := func() string {
		if rng.Intn(100) < 5 {
			return "NOPE"
		}
		return bonds[rng.Intn(len(bonds))]
	}
	qty := func() int64 {
		switch x := rng.Intn(100); {
		case x < 3:
			return 0 // 非法
		case x < 10:
			return 1 + rng.Int63n(1_000_000_000_000)
		default:
			return 1 + rng.Int63n(60)
		}
	}
	rate := func() int64 {
		if rng.Intn(100) < 3 {
			return 151 + rng.Int63n(10) // 非法
		}
		return rng.Int63n(151)
	}
	price := func() int64 {
		switch x := rng.Intn(100); {
		case x < 3:
			return 0 // 非法
		case x < 10:
			return 1 + rng.Int63n(1_000_000)
		default:
			return 1 + rng.Int63n(200)
		}
	}
	var ops []simOp
	var day, idSeq int64
	for i := 0; i < n; i++ {
		d := day
		switch rng.Intn(20) {
		case 0:
			day += 1 + rng.Int63n(30) // 大跳，跨过到期日
			d = day
		case 1:
			if day > 0 {
				d = day - 1 // 日期回退
			}
		case 2:
			d = -1 // 非法日期
		case 3:
			d = 1_000_001 // 非法日期
		default:
			day += rng.Int63n(3)
			d = day
		}
		kind := []string{
			"AddBond", "AddBond", "SetRate", "SetRate", "SetPrice",
			"Credit", "Credit", "CreditCash", "PledgeIn", "PledgeIn",
			"PledgeOut", "PledgeOut", "Repo", "Repo", "Deficits",
		}[rng.Intn(15)]
		op := simOp{kind: kind, day: d}
		switch kind {
		case "AddBond":
			op.s1, op.v1, op.v2 = pickBond(), rate(), price()
		case "SetRate":
			op.s1, op.v1 = pickBond(), rate()
		case "SetPrice":
			op.s1, op.v1 = pickBond(), price()
		case "Credit":
			op.s1, op.s2, op.v1 = pickAcct(), pickBond(), qty()
		case "CreditCash":
			op.s1, op.v1 = pickAcct(), qty()
		case "PledgeIn", "PledgeOut":
			op.s1, op.s2, op.v1 = pickAcct(), pickBond(), qty()
		case "Repo":
			idSeq++
			op.s1 = fmt.Sprintf("r%d", idSeq)
			if rng.Intn(100) < 5 && idSeq > 1 { // 重复编号
				op.s1 = fmt.Sprintf("r%d", 1+rng.Int63n(idSeq))
			}
			op.s2 = pickAcct()
			op.v1 = qty()
			switch rng.Intn(10) {
			case 0:
				op.v2 = 0 // 非法期限
			case 1:
				op.v2 = 366
			default:
				op.v2 = 1 + rng.Int63n(30)
			}
			switch rng.Intn(10) {
			case 0:
				op.v3 = 10001 // 非法利率
			case 1, 2:
				op.v3 = rng.Int63n(10001)
			default:
				op.v3 = rng.Int63n(500)
			}
		}
		ops = append(ops, op)
	}
	return ops
}

// compareState 比较引擎与朴素模型的全量状态；返回首个不一致的描述。
func compareState(e *Engine, m *model) string {
	if e.day != m.day {
		return fmt.Sprintf("day: engine=%d model=%d", e.day, m.day)
	}
	for name, ma := range m.accts {
		a, ok := e.lib.Get(name)
		if !ok {
			return fmt.Sprintf("账户 %s: 引擎缺失", name)
		}
		if a.Cash != ma.cash {
			return fmt.Sprintf("账户 %s 现金: engine=%d model=%d", name, a.Cash, ma.cash)
		}
		if a.Cap != m.capOf(name) {
			return fmt.Sprintf("账户 %s Cap: engine=%d 重算=%d", name, a.Cap, m.capOf(name))
		}
		if a.Use != m.useOf(name) {
			return fmt.Sprintf("账户 %s Use: engine=%d 重算=%d", name, a.Use, m.useOf(name))
		}
		for bond, n := range ma.avail {
			p := a.Pos[bond]
			var got int64
			if p != nil {
				got = p.Avail
			}
			if got != n {
				return fmt.Sprintf("账户 %s 债券 %s 可用: engine=%d model=%d", name, bond, got, n)
			}
		}
		for bond, n := range ma.pledged {
			p := a.Pos[bond]
			var got int64
			if p != nil {
				got = p.Pledged
			}
			if got != n {
				return fmt.Sprintf("账户 %s 债券 %s 在库: engine=%d model=%d", name, bond, got, n)
			}
		}
	}
	for id, mr := range m.repos {
		er, ok := e.repos[id]
		if !ok {
			return fmt.Sprintf("回购 %s: 引擎缺失", id)
		}
		if er.status != mr.status || er.badDebt != mr.badDebt || er.repay != mr.repay || er.due != mr.due {
			return fmt.Sprintf("回购 %s: engine=%v/%d/%d/%d model=%v/%d/%d/%d",
				id, er.status, er.badDebt, er.repay, er.due, mr.status, mr.badDebt, mr.repay, mr.due)
		}
	}
	ed, md := e.Deficits(), m.deficits()
	if len(ed) != len(md) {
		return fmt.Sprintf("Deficits 长度: engine=%d model=%d", len(ed), len(md))
	}
	for i := range ed {
		if !bytes.Equal(ed[i].Acct, md[i].Acct) || ed[i].Gap != md[i].Gap {
			return fmt.Sprintf("Deficits[%d]: engine=%s/%d model=%s/%d",
				i, ed[i].Acct, ed[i].Gap, md[i].Acct, md[i].Gap)
		}
	}
	return ""
}

// 1500 组随机操作序列与朴素模拟逐步对照，日志打印输入、输出与判定依据；
// 每组再重放一次验证相同操作序列得到相同结果。
func TestRandomSimAgainstNaive(t *testing.T) {
	const sequences = 1500
	const opsPerSeq = 80
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genOps(rng, opsPerSeq)
		e := New()
		m := newModel()
		for i, op := range ops {
			errE := applyEngine(e, op)
			errM := applyModel(m, op)
			basis := ""
			if ac := op.acct(); ac != "" {
				var cash int64
				if ma := m.accts[ac]; ma != nil {
					cash = ma.cash
				}
				basis = fmt.Sprintf("model[%s]: cap=%d use=%d cash=%d",
					ac, m.capOf(ac), m.useOf(ac), cash)
			}
			t.Logf("seed=%d op=%d 输入=%s 输出: engine=%s model=%s 依据: %s",
				seed, i, op, errKey(errE), errKey(errM), basis)
			if errKey(errE) != errKey(errM) {
				t.Fatalf("seed=%d op=%d %s: engine=%s model=%s", seed, i, op, errKey(errE), errKey(errM))
			}
			if diff := compareState(e, m); diff != "" {
				t.Fatalf("seed=%d op=%d %s: 状态不一致: %s", seed, i, op, diff)
			}
			// 每次被接受的 Repo 与 PledgeOut 之后该账户 Cap>=Use。
			if (op.kind == "Repo" || op.kind == "PledgeOut") && errE == nil {
				a, _ := e.lib.Get(op.acct())
				if a.Cap < a.Use {
					t.Fatalf("seed=%d op=%d %s: 接受后 Cap=%d < Use=%d", seed, i, op, a.Cap, a.Use)
				}
			}
		}
		// 重放：相同操作序列得到相同结果。
		e2 := New()
		for _, op := range ops {
			_ = applyEngine(e2, op)
		}
		if diff := compareEngines(e, e2, m); diff != "" {
			t.Fatalf("seed=%d 重放不一致: %s", seed, diff)
		}
	}
}

func compareEngines(e1, e2 *Engine, m *model) string {
	if e1.day != e2.day || e1.seq != e2.seq {
		return fmt.Sprintf("day/seq: %d/%d vs %d/%d", e1.day, e1.seq, e2.day, e2.seq)
	}
	for name, ma := range m.accts {
		a1, _ := e1.lib.Get(name)
		a2, _ := e2.lib.Get(name)
		if a1.Cash != a2.Cash || a1.Cap != a2.Cap || a1.Use != a2.Use {
			return fmt.Sprintf("账户 %s: %v vs %v", name, a1, a2)
		}
		for bond, p1 := range a1.Pos {
			p2 := a2.Pos[bond]
			if p2 == nil || *p1 != *p2 {
				return fmt.Sprintf("账户 %s 债券 %s 持仓不一致", name, bond)
			}
		}
		_ = ma
	}
	for id, r1 := range e1.repos {
		r2 := e2.repos[id]
		if r2 == nil || *r1 != *r2 {
			return fmt.Sprintf("回购 %s 不一致", id)
		}
	}
	return ""
}
