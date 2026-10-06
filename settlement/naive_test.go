package settlement

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"testing"
	"time"
)

// naiveModel 是独立按规则逐月重算的「朴素模型」：
// 不维护任何增量余额，任何封账都从第 0 个月开始、把全部月的全部读数重扫一遍。
// 它只用于与生产引擎做差分对照；为了独立，结算逻辑在本文件内重新实现一遍。
type naiveModel struct {
	intervalSec int64
	epochUnix   int64
	params      map[string][]scheduleEntry[Params]
	prices      map[string][]scheduleEntry[Prices]
	readings    map[string]map[int64][2]int
	closed      map[string]map[int]bool
}

func newNaive(e *Engine) *naiveModel {
	return &naiveModel{
		intervalSec: e.intervalSec,
		epochUnix:   e.epochUnix,
		params:      map[string][]scheduleEntry[Params]{},
		prices:      map[string][]scheduleEntry[Prices]{},
		readings:    map[string]map[int64][2]int{},
		closed:      map[string]map[int]bool{},
	}
}

func (n *naiveModel) ensure(id string) {
	if _, ok := n.params[id]; !ok {
		n.params[id] = nil
		n.prices[id] = nil
		n.readings[id] = map[int64][2]int{}
		n.closed[id] = map[int]bool{}
	}
}

func (n *naiveModel) register(id string) error {
	if _, ok := n.params[id]; ok {
		return errInvalid("RegisterProsumer", "产消者已存在")
	}
	n.ensure(id)
	return nil
}

func (n *naiveModel) setParams(id, from string, p Params) error {
	m, err := parseMonth(from)
	if err != nil {
		return err
	}
	if p.ContractPowerW < 0 || p.MonthlyCreditableW < 0 || p.CreditValidMonths < 0 {
		return errInvalid("SetParams", "参数为负")
	}
	if _, ok := n.params[id]; !ok {
		return errInvalid("SetParams", "未知产消者")
	}
	if n.lastClosedAfter(id, m) {
		return &SettlementError{Kind: KindClosed, Op: "SetParams", Reason: "生效月已封账"}
	}
	n.params[id] = upsertSchedule(n.params[id], m, p)
	return nil
}

func (n *naiveModel) setPrices(id, from string, pc Prices) error {
	m, err := parseMonth(from)
	if err != nil {
		return err
	}
	if pc.ImportPricePerWh < 0 || pc.SurplusPricePerWh < 0 {
		return errInvalid("SetPrices", "单价为负")
	}
	if _, ok := n.prices[id]; !ok {
		return errInvalid("SetPrices", "未知产消者")
	}
	if n.lastClosedAfter(id, m) {
		return &SettlementError{Kind: KindClosed, Op: "SetPrices", Reason: "生效月已封账"}
	}
	n.prices[id] = upsertSchedule(n.prices[id], m, pc)
	return nil
}

func (n *naiveModel) lastClosedAfter(id string, m Month) bool {
	for idx := range n.closed[id] {
		if idx >= m.index() {
			return true
		}
	}
	return false
}

func (n *naiveModel) reading(id string, rd Reading) error {
	if _, ok := n.readings[id]; !ok {
		return errInvalid("RegisterReading", "未知产消者")
	}
	if !aligned(rd.Start, n.epochUnix, n.intervalSec) {
		return errInvalid("RegisterReading", "间隔起点未对齐")
	}
	if rd.ImportWh < 0 || rd.ExportWh < 0 {
		return errInvalid("RegisterReading", "电量为负")
	}
	m := monthOf(rd.Start)
	key := utcUnix(rd.Start)
	if n.closed[id][m.index()] {
		if old, ok := n.readings[id][key]; ok && old[0] == rd.ImportWh && old[1] == rd.ExportWh {
			return nil
		}
		return &SettlementError{Kind: KindClosed, Op: "RegisterReading", Reason: "月份已封账"}
	}
	n.readings[id][key] = [2]int{rd.ImportWh, rd.ExportWh}
	return nil
}

// naiveLot 朴素模型里的额度笔：列表即可，扫描 O(全部历史)。
type naiveLot struct {
	deposit, expire Month
	remaining       int
}

// recompute 从首个有参数的月起到 m 月逐月全量重算（朴素 O(历史)）。
func (n *naiveModel) recompute(id string, m Month) (MonthResult, error) {
	ps, pcs := n.params[id], n.prices[id]
	if len(ps) == 0 || len(pcs) == 0 {
		return MonthResult{}, errInvalid("recompute", "缺少参数或单价")
	}
	cursor := ps[0].from
	if first := pcs[0].from; first.after(cursor) {
		cursor = first
	}
	if cursor.after(m) {
		return MonthResult{}, errInvalid("recompute", "目标早于生效起点")
	}
	lots := []naiveLot{}
	var res MonthResult
	for !cursor.after(m) {
		p, _ := valueAt(ps, cursor)
		pc, _ := valueAt(pcs, cursor)
		capWh := p.ContractPowerW * int(n.intervalSec) / 3600

		// 收集本月读数（朴素做法：扫全部读数键）。
		keys := make([]int64, 0)
		start := monthStartUTC(cursor).Unix()
		next := monthStartUTC(cursor.add(1)).Unix()
		for k := range n.readings[id] {
			if int64(k) >= start && int64(k) < next {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

		r := MonthResult{Month: cursor}
		for _, k := range keys {
			v := n.readings[id][int64(k)]
			r.ImportWh += v[0]
			r.ExportRawWh += v[1]
			if over := v[1] - capWh; over > 0 {
				r.CurtailedWh += over
			}
		}
		capped := r.ExportRawWh - r.CurtailedWh
		if capped > p.MonthlyCreditableW {
			r.CreditableWh = p.MonthlyCreditableW
			r.AboveCapWh = capped - p.MonthlyCreditableW
		} else {
			r.CreditableWh = capped
		}
		net := r.ImportWh - r.CreditableWh
		if net <= 0 {
			r.SelfOffsetWh = r.ImportWh
			r.DepositedWh = -net
			if r.DepositedWh > 0 {
				lots = append(lots, naiveLot{cursor, cursor.add(p.CreditValidMonths), r.DepositedWh})
				r.NewCreditRemaining = r.DepositedWh
			}
		} else {
			r.SelfOffsetWh = r.CreditableWh
			need := net
			// 朴素线性扫描：按（到期、存入）排序后逐笔使用。
			sort.SliceStable(lots, func(i, j int) bool {
				a, b := lots[i], lots[j]
				if !a.expire.equal(b.expire) {
					return a.expire.index() < b.expire.index()
				}
				return a.deposit.index() < b.deposit.index()
			})
			for i := range lots {
				lot := &lots[i]
				if lot.remaining <= 0 || lot.deposit.index() >= cursor.index() ||
					lot.expire.index() < cursor.index() {
					continue
				}
				take := lot.remaining
				if take > need {
					take = need
				}
				lot.remaining -= take
				need -= take
				r.HistoryUsedWh += take
				if need == 0 {
					break
				}
			}
			r.BilledWh = need
		}
		// 到期月 <= 本月（恰本月者在抵扣之后）的额度按本月余电单价付款。
		kept := lots[:0]
		for _, lot := range lots {
			if lot.remaining > 0 && lot.expire.index() <= cursor.index() {
				r.ExpiredPaidWh += lot.remaining
				continue
			}
			if lot.remaining > 0 {
				kept = append(kept, lot)
			}
		}
		lots = kept
		r.ImportBill = r.BilledWh * pc.ImportPricePerWh
		r.SurplusPayment = (r.AboveCapWh + r.ExpiredPaidWh) * pc.SurplusPricePerWh
		res = r
		cursor = cursor.add(1)
	}
	return res, nil
}

func (n *naiveModel) close(id, month string) (*MonthResult, error) {
	m, err := parseMonth(month)
	if err != nil {
		return nil, err
	}
	if _, ok := n.closed[id]; !ok {
		return nil, errInvalid("CloseMonth", "未知产消者")
	}
	if n.closed[id][m.index()] {
		return nil, &SettlementError{Kind: KindClosed, Op: "CloseMonth", Reason: "月份已封账"}
	}
	expect := -1
	for idx := range n.closed[id] {
		if expect < idx {
			expect = idx
		}
	}
	if expect >= 0 && m.index() != expect+1 {
		return nil, &SettlementError{Kind: KindOrder, Op: "CloseMonth", Reason: "顺序错误"}
	}
	start := monthStartUTC(m).Unix()
	next := monthStartUTC(m.add(1)).Unix()
	for t := start; t < next; t += n.intervalSec {
		if _, ok := n.readings[id][t]; !ok {
			return nil, &SettlementError{
				Kind: KindMissing, Op: "CloseMonth", Reason: "数据缺失",
				Missing: time.Unix(t, 0).UTC(),
			}
		}
	}
	r, err := n.recompute(id, m)
	if err != nil {
		return nil, err
	}
	n.closed[id][m.index()] = true
	return &r, nil
}

func (n *naiveModel) query(id, month string) (*MonthResult, error) {
	m, err := parseMonth(month)
	if err != nil {
		return nil, err
	}
	if _, ok := n.closed[id]; !ok {
		return nil, errInvalid("Query", "未知产消者")
	}
	if n.closed[id][m.index()] {
		r, err := n.recompute(id, m)
		if err != nil {
			return nil, err
		}
		return &r, nil
	}
	r, err := n.recompute(id, m)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func errKey(err error) string {
	if err == nil {
		return "ok"
	}
	var se *SettlementError
	if errors.As(err, &se) {
		key := fmt.Sprintf("kind=%d", se.Kind)
		if se.Kind == KindMissing {
			key += "@" + se.Missing.UTC().Format(time.RFC3339)
		}
		return key
	}
	return err.Error()
}

// ---- 随机差分驱动器 ----

var settleLog = os.Getenv("SETTLE_LOG") == "1"

type opKind int

const (
	opSetParams opKind = iota
	opSetPrices
	opReading
	opClose
	opQuery
)

type op struct {
	kind   opKind
	pid    string
	month  Month
	start  time.Time
	imp    int
	exp    int
	power  int
	capM   int
	valid  int
	iprice int
	sprice int
	desc   string
}

// runRandomDifferential 用固定随机种子生成大量操作，同时喂给生产引擎与朴素模型，
// 比对错误类别（含数据缺失的最早间隔）与月结果；任何分歧立即打印完整日志后失败。
func runRandomDifferential(t *testing.T, seed, horizonMonths int, opsN int) {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(seed)))
	eng := newTestEngine()
	nv := newNaive(eng)

	// 3 个产消者，预先以 2026-01 生效参数/单价注册。
	const pn = 3
	for i := 0; i < pn; i++ {
		id := fmt.Sprintf("p%d", i)
		if err := eng.RegisterProsumer(id); err != nil {
			t.Fatal(err)
		}
		if err := nv.register(id); err != nil {
			t.Fatal(err)
		}
		p0 := Params{ContractPowerW: 3000, MonthlyCreditableW: 20000, CreditValidMonths: 2}
		c0 := Prices{ImportPricePerWh: 8, SurplusPricePerWh: 3}
		if err := eng.SetParams(id, "2026-01", p0); err != nil {
			t.Fatal(err)
		}
		if err := nv.setParams(id, "2026-01", p0); err != nil {
			t.Fatal(err)
		}
		if err := eng.SetPrices(id, "2026-01", c0); err != nil {
			t.Fatal(err)
		}
		if err := nv.setPrices(id, "2026-01", c0); err != nil {
			t.Fatal(err)
		}
	}

	firstMonth := MustMonth("2026-01")
	logs := []string{fmt.Sprintf("=== differential seed=%d ops=%d ===", seed, opsN)}

	randomTime := func(m Month) time.Time {
		day := 1 + rng.Intn(daysInMonth(m))
		hour := rng.Intn(24)
		return atMonth(m, day, hour)
	}

	failf := func(format string, a ...any) {
		for _, l := range logs {
			t.Log(l)
		}
		t.Fatalf(format, a...)
	}

	for i := 0; i < opsN; i++ {
		pid := fmt.Sprintf("p%d", rng.Intn(pn))
		kind := opKind(rng.Intn(5))
		o := op{kind: kind, pid: pid}
		var engR, nvR *MonthResult
		var engErr, nvErr error

		switch kind {
		case opSetParams:
			o.month = firstMonth.add(rng.Intn(horizonMonths))
			o.power = rng.Intn(6001)
			o.capM = rng.Intn(40001)
			o.valid = rng.Intn(4)
			o.desc = fmt.Sprintf("SetParams(%s,%s,power=%d,cap=%d,valid=%d)", pid, o.month, o.power, o.capM, o.valid)
			p := Params{o.power, o.capM, o.valid}
			engErr = eng.SetParams(pid, o.month.String(), p)
			nvErr = nv.setParams(pid, o.month.String(), p)
		case opSetPrices:
			o.month = firstMonth.add(rng.Intn(horizonMonths))
			o.iprice = rng.Intn(20)
			o.sprice = rng.Intn(10)
			o.desc = fmt.Sprintf("SetPrices(%s,%s,in=%d,sur=%d)", pid, o.month, o.iprice, o.sprice)
			pc := Prices{o.iprice, o.sprice}
			engErr = eng.SetPrices(pid, o.month.String(), pc)
			nvErr = nv.setPrices(pid, o.month.String(), pc)
		case opReading:
			o.month = firstMonth.add(rng.Intn(horizonMonths))
			o.start = randomTime(o.month)
			// 小概率非法输入（负电量）。
			if rng.Intn(15) == 0 {
				o.imp = -1
			} else {
				o.imp = rng.Intn(9001)
			}
			if rng.Intn(15) == 0 {
				o.exp = -1
			} else {
				o.exp = rng.Intn(9001)
			}
			o.desc = fmt.Sprintf("Reading(%s,%s,imp=%d,exp=%d)", pid, o.start.Format(time.RFC3339), o.imp, o.exp)
			rd := Reading{Start: o.start, ImportWh: o.imp, ExportWh: o.exp}
			engErr = eng.RegisterReading(pid, rd)
			nvErr = nv.reading(pid, rd)
		case opClose:
			o.month = firstMonth.add(rng.Intn(horizonMonths))
			o.desc = fmt.Sprintf("Close(%s,%s)", pid, o.month)
			engR, engErr = eng.CloseMonth(pid, o.month.String())
			nvR, nvErr = nv.close(pid, o.month.String())
		case opQuery:
			o.month = firstMonth.add(rng.Intn(horizonMonths))
			o.desc = fmt.Sprintf("Query(%s,%s)", pid, o.month)
			engR, engErr = eng.Query(pid, o.month.String())
			nvR, nvErr = nv.query(pid, o.month.String())
		}

		ek, nk := errKey(engErr), errKey(nvErr)
		line := fmt.Sprintf("#%d %s => eng[%s] naive[%s]", i, o.desc, ek, nk)
		if settleLog {
			t.Log(line)
		}
		logs = append(logs, line)
		if ek != nk {
			failf("错误类别分歧 @ %s", line)
		}
		if engErr == nil && (kind == opClose || kind == opQuery) {
			if engR == nil || nvR == nil || *engR != *nvR {
				logs = append(logs,
					fmt.Sprintf("  eng  = %+v", engR),
					fmt.Sprintf("  naive= %+v", nvR))
				failf("月结果分歧 @ %s", o.desc)
			}
			why := fmt.Sprintf("imp=%d exp=%d cur=%d above=%d self=%d dep=%d hist=%d bill=%d exp=%d inBill=%d sur=%d",
				nvR.ImportWh, nvR.ExportRawWh, nvR.CurtailedWh, nvR.AboveCapWh,
				nvR.SelfOffsetWh, nvR.DepositedWh, nvR.HistoryUsedWh, nvR.BilledWh,
				nvR.ExpiredPaidWh, nvR.ImportBill, nvR.SurplusPayment)
			logs = append(logs, "    -> "+why)
		}
	}

	// 收尾：尽量顺序封账全部月，两边结果必须一致，且全部恒等式成立。
	for p := 0; p < pn; p++ {
		pid := fmt.Sprintf("p%d", p)
		for k := 0; k < horizonMonths; k++ {
			m := firstMonth.add(k)
			er, ee := eng.CloseMonth(pid, m.String())
			nr, ne := nv.close(pid, m.String())
			if errKey(ee) != errKey(ne) {
				failf("收尾封账错误分歧 %s %s: %v vs %v", pid, m, ee, ne)
			}
			if ee == nil && *er != *nr {
				failf("收尾封账结果分歧 %s %s: %+v vs %+v", pid, m, *er, *nr)
			}
		}
		assertInvariants(t, eng, pid, "2026-01")
	}
}

func daysInMonth(m Month) int {
	return time.Date(m.Year, time.Month(m.Month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func TestRandomDifferential(t *testing.T) {
	seeds := []int{1, 2, 3, 7, 42, 100}
	for _, s := range seeds {
		t.Run(fmt.Sprintf("seed=%d", s), func(t *testing.T) {
			runRandomDifferential(t, s, 6, 1200)
		})
	}
}
