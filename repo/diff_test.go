package repo_test

// 朴素模拟器：独立于生产代码的增量维护，每次需要时从在库明细重算 Cap；
// 结算逻辑同样独立实现。用于 1500 组随机操作序列的差分对照。

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"

	"ontology/haircut"
	"ontology/repo"
)

type simBond struct {
	rate  int
	price int64
}

type simAcct struct {
	cash    int64
	avail   map[string]int64
	pledged map[string]int64
	use     int64
}

type simRepo struct {
	acct    string
	amount  int64
	due     int
	dueAmt  int64
	status  int
	badDebt int64
	seq     int64
}

type sim struct {
	day   int
	bonds map[string]simBond
	accts map[string]*simAcct
	repos map[string]*simRepo
	seq   int64
	log   []string
}

func newSim() *sim {
	return &sim{
		bonds: map[string]simBond{},
		accts: map[string]*simAcct{},
		repos: map[string]*simRepo{},
	}
}

func (m *sim) capOf(a *simAcct) int64 {
	var cap int64
	for b, n := range a.pledged {
		cap += n * int64(m.bonds[b].rate) / 100 // 逐券向下取整
	}
	return cap
}

func (m *sim) settle(day int) {
	var due []*simRepo
	for _, rp := range m.repos {
		if rp.status == repo.StatusOpen && rp.due <= day {
			due = append(due, rp)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].due != due[j].due {
			return due[i].due < due[j].due
		}
		return due[i].seq < due[j].seq
	})
	for _, rp := range due {
		a := m.accts[rp.acct]
		occ := (rp.amount + 99) / 100
		if a.cash >= rp.dueAmt {
			a.cash -= rp.dueAmt
			a.use -= occ
			rp.status = repo.StatusRepaid
			continue
		}
		rp.status = repo.StatusDefault
		owed := rp.dueAmt
		var bonds []string
		for b, n := range a.pledged {
			if n > 0 {
				bonds = append(bonds, b)
			}
		}
		sort.Strings(bonds)
		for _, b := range bonds {
			if owed <= 0 {
				break
			}
			price := m.bonds[b].price
			need := (owed + price - 1) / price
			sold := a.pledged[b]
			if sold > need {
				sold = need
			}
			proceeds := sold * price
			a.pledged[b] -= sold
			if a.pledged[b] == 0 {
				delete(a.pledged, b)
			}
			if proceeds >= owed {
				a.cash += proceeds - owed
				owed = 0
				break
			}
			owed -= proceeds
		}
		if owed > 0 {
			rp.badDebt = owed
		}
		a.use -= occ
	}
}

func (m *sim) enter(day int) error {
	if day < m.day {
		return haircut.ErrDayBackward
	}
	m.day = day
	m.settle(day)
	return nil
}

func (m *sim) getAcct(k string) *simAcct {
	a := m.accts[k]
	if a == nil {
		a = &simAcct{avail: map[string]int64{}, pledged: map[string]int64{}}
		m.accts[k] = a
	}
	return a
}

// op 描述一步随机操作。
type op struct {
	kind                int
	day                 int
	acct, bond, id      string
	n, price            int64
	rate, days, rateBps int
}

const (
	opAddBond = iota
	opSetRate
	opSetPrice
	opCredit
	opCreditCash
	opPledgeIn
	opPledgeOut
	opRepo
	opIdle
)

func (m *sim) run(o op) error {
	switch o.kind {
	case opAddBond:
		if _, ok := m.bonds[o.bond]; ok {
			return haircut.ErrDupBond
		}
		m.bonds[o.bond] = simBond{rate: o.rate, price: o.price}
		return nil
	case opSetRate:
		if _, ok := m.bonds[o.bond]; !ok {
			return haircut.ErrNoBond
		}
		b := m.bonds[o.bond]
		b.rate = o.rate
		m.bonds[o.bond] = b
		return nil
	case opSetPrice:
		if _, ok := m.bonds[o.bond]; !ok {
			return haircut.ErrNoBond
		}
		b := m.bonds[o.bond]
		b.price = o.price
		m.bonds[o.bond] = b
		return nil
	case opCredit:
		if _, ok := m.bonds[o.bond]; !ok {
			return haircut.ErrNoBond
		}
		a := m.getAcct(o.acct)
		a.avail[o.bond] += o.n
		return nil
	case opCreditCash:
		a := m.getAcct(o.acct)
		a.cash += o.n
		return nil
	case opPledgeIn:
		if _, ok := m.bonds[o.bond]; !ok {
			return haircut.ErrNoBond
		}
		a, ok := m.accts[o.acct]
		if !ok {
			return haircut.ErrNoAccount
		}
		if a.avail[o.bond] < o.n {
			return haircut.ErrAvail
		}
		a.avail[o.bond] -= o.n
		a.pledged[o.bond] += o.n
		return nil
	case opPledgeOut:
		if _, ok := m.bonds[o.bond]; !ok {
			return haircut.ErrNoBond
		}
		a, ok := m.accts[o.acct]
		if !ok {
			return haircut.ErrNoAccount
		}
		if m.capOf(a) < a.use {
			return haircut.ErrDeficit
		}
		if a.pledged[o.bond] < o.n {
			return haircut.ErrStock
		}
		newCap := m.capOf(a) -
			(a.pledged[o.bond]*int64(m.bonds[o.bond].rate)/100 -
				(a.pledged[o.bond]-o.n)*int64(m.bonds[o.bond].rate)/100)
		if newCap < a.use {
			return haircut.ErrCap
		}
		a.pledged[o.bond] -= o.n
		a.avail[o.bond] += o.n
		return nil
	case opRepo:
		if _, ok := m.repos[o.id]; ok {
			return haircut.ErrDupRepo
		}
		a, ok := m.accts[o.acct]
		if !ok {
			return haircut.ErrNoAccount
		}
		if m.capOf(a) < a.use {
			return haircut.ErrDeficit
		}
		occ := (o.n + 99) / 100
		if m.capOf(a) < a.use+occ {
			return haircut.ErrCap
		}
		interest := (o.n*int64(o.rateBps)*int64(o.days) + 3_649_999) / 3_650_000
		a.use += occ
		a.cash += o.n
		m.seq++
		m.repos[o.id] = &simRepo{
			acct: o.acct, amount: o.n, due: o.day + o.days,
			dueAmt: o.n + interest, status: repo.StatusOpen, seq: m.seq,
		}
		return nil
	case opIdle:
		return nil
	}
	return nil
}

func validOp(o op) bool {
	if !haircut.ValidDay(o.day) || o.acct == "" {
		return false
	}
	switch o.kind {
	case opAddBond:
		return o.bond != "" && haircut.ValidRate(o.rate) && haircut.ValidPrice(o.price)
	case opSetRate:
		return o.bond != "" && haircut.ValidRate(o.rate)
	case opSetPrice:
		return o.bond != "" && haircut.ValidPrice(o.price)
	case opCredit, opPledgeIn, opPledgeOut:
		return o.bond != "" && haircut.ValidQty(o.n)
	case opCreditCash:
		return haircut.ValidQty(o.n)
	case opRepo:
		return o.id != "" && haircut.ValidQty(o.n) &&
			o.days >= 1 && o.days <= 365 && haircut.ValidRateBps(o.rateBps)
	case opIdle:
		return true
	}
	return false
}

type snapshot struct {
	day    int
	cash   map[string]int64
	cap    map[string]int64
	use    map[string]int64
	avail  map[string]map[string]int64
	pledge map[string]map[string]int64
	repos  map[string]string
	bad    map[string]int64
}

func takeSimSnapshot(m *sim) snapshot {
	sn := snapshot{
		day: m.day, cash: map[string]int64{}, cap: map[string]int64{}, use: map[string]int64{},
		avail: map[string]map[string]int64{}, pledge: map[string]map[string]int64{},
		repos: map[string]string{}, bad: map[string]int64{},
	}
	for k, a := range m.accts {
		if k == "u_clock" {
			continue
		}
		sn.cash[k] = a.cash
		sn.cap[k] = m.capOf(a)
		sn.use[k] = a.use
		av := map[string]int64{}
		for b, n := range a.avail {
			if n != 0 {
				av[b] = n
			}
		}
		pl := map[string]int64{}
		for b, n := range a.pledged {
			if n != 0 {
				pl[b] = n
			}
		}
		sn.avail[k] = av
		sn.pledge[k] = pl
	}
	for id, rp := range m.repos {
		sn.repos[id] = fmt.Sprintf("%d/%d/%d", rp.status, rp.due, rp.dueAmt)
		sn.bad[id] = rp.badDebt
	}
	return sn
}

func takeRealSnapshot(s *repo.System) snapshot {
	sn := snapshot{
		cash: map[string]int64{}, cap: map[string]int64{}, use: map[string]int64{},
		avail: map[string]map[string]int64{}, pledge: map[string]map[string]int64{},
		repos: map[string]string{}, bad: map[string]int64{},
	}
	sn.day = s.Day()
	// 通过已知账户集合采集：模拟器与真实系统账户键相同，用 Deficits 无法枚举，
	// 这里借助测试侧维护的 acctKeys。
	for _, k := range realAcctKeys(s) {
		a := s.Acct([]byte(k))
		if a == nil {
			continue
		}
		sn.cash[k] = a.Cash
		sn.cap[k] = a.Cap
		sn.use[k] = a.Use
		av := map[string]int64{}
		for b, n := range a.Avail {
			if n != 0 {
				av[b] = n
			}
		}
		pl := map[string]int64{}
		for b, n := range a.Pledged {
			if n != 0 {
				pl[b] = n
			}
		}
		sn.avail[k] = av
		sn.pledge[k] = pl
	}
	for _, id := range realRepoIDs(s) {
		rp := s.GetRepo([]byte(id))
		if rp == nil {
			continue
		}
		sn.repos[id] = fmt.Sprintf("%d/%d/%d", rp.Status, rp.Due, rp.DueAmt)
		sn.bad[id] = rp.BadDebt
	}
	return sn
}

// 真实系统无账户/回购枚举接口；测试通过反射不合适，故在每步用已知键集探测。
// 简化：把键集固定为生成器使用的有限账户/编号空间。
const (
	acctSpace = 4
	bondSpace = 4
	repoSpace = 40
)

func realAcctKeys(_ *repo.System) []string {
	keys := make([]string, 0, acctSpace)
	for i := 0; i < acctSpace; i++ {
		keys = append(keys, fmt.Sprintf("u%d", i))
	}
	return keys
}

func realRepoIDs(_ *repo.System) []string {
	ids := make([]string, 0, repoSpace)
	for i := 0; i < repoSpace; i++ {
		ids = append(ids, fmt.Sprintf("r%02d", i))
	}
	return ids
}

func (a snapshot) diff(b snapshot) string {
	var d []string
	if a.day != b.day {
		d = append(d, fmt.Sprintf("day %d!=%d", a.day, b.day))
	}
	for k := range a.cash {
		if a.cash[k] != b.cash[k] {
			d = append(d, fmt.Sprintf("cash[%s] %d!=%d", k, a.cash[k], b.cash[k]))
		}
		if a.cap[k] != b.cap[k] {
			d = append(d, fmt.Sprintf("cap[%s] %d!=%d", k, a.cap[k], b.cap[k]))
		}
		if a.use[k] != b.use[k] {
			d = append(d, fmt.Sprintf("use[%s] %d!=%d", k, a.use[k], b.use[k]))
		}
		if fmt.Sprint(a.avail[k]) != fmt.Sprint(b.avail[k]) {
			d = append(d, fmt.Sprintf("avail[%s] %v!=%v", k, a.avail[k], b.avail[k]))
		}
		if fmt.Sprint(a.pledge[k]) != fmt.Sprint(b.pledge[k]) {
			d = append(d, fmt.Sprintf("pledged[%s] %v!=%v", k, a.pledge[k], b.pledge[k]))
		}
	}
	for id := range a.repos {
		if a.repos[id] != b.repos[id] {
			d = append(d, fmt.Sprintf("repo[%s] %q!=%q", id, a.repos[id], b.repos[id]))
		}
		if a.bad[id] != b.bad[id] {
			d = append(d, fmt.Sprintf("bad[%s] %d!=%d", id, a.bad[id], b.bad[id]))
		}
	}
	return strings.Join(d, "; ")
}

func errLabel(e error) string {
	switch {
	case e == nil:
		return "nil"
	case errors.Is(e, haircut.ErrInvalid):
		return "Invalid"
	case errors.Is(e, haircut.ErrDayBackward):
		return "DayBackward"
	case errors.Is(e, haircut.ErrDupBond):
		return "DupBond"
	case errors.Is(e, haircut.ErrDupRepo):
		return "DupRepo"
	case errors.Is(e, haircut.ErrNoBond):
		return "NoBond"
	case errors.Is(e, haircut.ErrNoAccount):
		return "NoAccount"
	case errors.Is(e, haircut.ErrDeficit):
		return "Deficit"
	case errors.Is(e, haircut.ErrAvail):
		return "Avail"
	case errors.Is(e, haircut.ErrStock):
		return "Stock"
	case errors.Is(e, haircut.ErrCap):
		return "Cap"
	}
	return e.Error()
}

func genOps(rng *rand.Rand, steps int) []op {
	ops := make([]op, 0, steps)
	day := 0
	for i := 0; i < steps; i++ {
		// 日期：以较大概率停留在当天，偶尔前进，几乎不回退（回退分支单独注入）
		switch rng.Intn(10) {
		case 0:
			day += rng.Intn(4)
		case 1:
			if day > 0 && rng.Intn(3) == 0 {
				day--
			}
		}
		if day > 60 {
			day = 60
		}
		o := op{
			day:  day,
			acct: fmt.Sprintf("u%d", rng.Intn(acctSpace)),
			bond: fmt.Sprintf("b%d", rng.Intn(bondSpace)),
			id:   fmt.Sprintf("r%02d", rng.Intn(repoSpace)),
			n:    int64(1 + rng.Intn(2000)),
		}
		switch k := rng.Intn(10); k {
		case 0:
			o.kind = opAddBond
			o.rate = rng.Intn(151)
			o.price = int64(1 + rng.Intn(200))
		case 1:
			o.kind = opSetRate
			o.rate = rng.Intn(151)
		case 2:
			o.kind = opSetPrice
			o.price = int64(1 + rng.Intn(200))
		case 3:
			o.kind = opCredit
		case 4:
			o.kind = opCreditCash
		case 5, 6:
			o.kind = opPledgeIn
		case 7:
			o.kind = opPledgeOut
		case 8:
			o.kind = opRepo
			o.days = 1 + rng.Intn(12)
			o.rateBps = rng.Intn(3000)
		default:
			o.kind = opIdle
		}
		// 注入少量非法参数
		if rng.Intn(20) == 0 {
			o.n = 0
		}
		if !validOp(o) {
			o.kind = opIdle
			if !validOp(o) {
				continue
			}
		}
		ops = append(ops, o)
	}
	return ops
}

func runReal(s *repo.System, o op) error {
	switch o.kind {
	case opAddBond:
		return s.AddBond(o.day, []byte(o.bond), o.rate, o.price)
	case opSetRate:
		return s.SetRate(o.day, []byte(o.bond), o.rate)
	case opSetPrice:
		return s.SetPrice(o.day, []byte(o.bond), o.price)
	case opCredit:
		return s.Credit(o.day, []byte(o.acct), []byte(o.bond), o.n)
	case opCreditCash:
		return s.CreditCash(o.day, []byte(o.acct), o.n)
	case opPledgeIn:
		return s.PledgeIn(o.day, []byte(o.acct), []byte(o.bond), o.n)
	case opPledgeOut:
		return s.PledgeOut(o.day, []byte(o.acct), []byte(o.bond), o.n)
	case opRepo:
		return s.Repo(o.day, []byte(o.id), []byte(o.acct), o.n, o.days, o.rateBps)
	case opIdle:
		// 用旁观者账户的 CreditCash 推进日期并触发结算
		return s.CreditCash(o.day, []byte("u_clock"), 1)
	}
	return nil
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	verbose := os.Getenv("DIFF_VERBOSE") == "1"
	const sequences, steps = 1500, 30
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genOps(rng, steps)
		m := newSim()
		s := repo.New()
		for i, o := range ops {
			// 模拟器：参数校验在生成阶段已保证合法（Idle 除外）；先推进日期。
			var simErr error
			if err := m.enter(o.day); err != nil {
				simErr = err
			} else {
				if o.kind == opIdle {
					a := m.getAcct("u_clock")
					a.cash++
				} else {
					simErr = m.run(o)
				}
			}
			realErr := runReal(s, o)
			if verbose {
				t.Logf("seed=%d step=%d INPUT %+v -> OUTPUT sim=%s real=%s",
					seed, i, o, errLabel(simErr), errLabel(realErr))
			}
			if errLabel(simErr) != errLabel(realErr) {
				dumpFailure(t, seed, i, o, ops, errLabel(simErr), errLabel(realErr))
				t.Fatalf("seed=%d step=%d error divergence: sim=%s real=%s",
					seed, i, errLabel(simErr), errLabel(realErr))
			}
			sn1, sn2 := takeSimSnapshot(m), takeRealSnapshot(s)
			if d := sn1.diff(sn2); d != "" {
				dumpFailure(t, seed, i, o, ops, errLabel(simErr), errLabel(realErr))
				t.Fatalf("seed=%d step=%d state divergence (judge: Cap/Use/cash/holdings/repo vs naive): %s", seed, i, d)
			}
			if verbose {
				t.Logf("seed=%d step=%d JUDGE match at day=%d", seed, i, sn1.day)
			}
		}
	}
	t.Logf("differential: %d random sequences x %d ops all match naive recomputation", sequences, steps)
}

func dumpFailure(t *testing.T, seed int64, step int, o op, ops []op, simErr, realErr string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "DIFF seed=%d step=%d op=%+v\n", seed, step, o)
	fmt.Fprintf(&b, "err sim=%s real=%s\n", simErr, realErr)
	start := step - 8
	if start < 0 {
		start = 0
	}
	for j := start; j <= step && j < len(ops); j++ {
		mark := "  "
		if j == step {
			mark = ">>"
		}
		fmt.Fprintf(&b, "%s [%d] %+v\n", mark, j, ops[j])
	}
	t.Log(b.String())
}
