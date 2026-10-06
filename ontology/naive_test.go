package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// 朴素模型：与产品实现完全独立的流式单遍参考实现。
// 它只维护“每个年度已接受理赔的有序序列”，任何时刻的年度累计都通过
// 从头重放得到（O(历史)，刻意不做任何增量优化），从而与 O(1) 的引擎
// 形成结构性对照。

type naiveClaim struct {
	id     string
	day    int64
	lines  []Line
	result Settlement
}

type naiveEngine struct {
	spec   PolicySpec
	years  map[int64][]*naiveClaim // 按受理次序
	exists map[string]bool
}

func newNaive(spec PolicySpec) *naiveEngine {
	return &naiveEngine{spec: spec, years: map[int64][]*naiveClaim{}, exists: map[string]bool{}}
}

// naiveSettle 以“逐分 + 两条独立预算（免赔剩余、自付剩余）”的直白方式结算。
func naiveSettle(in Claim, spec PolicySpec, dedUsed, oopUsed int64) Settlement {
	out := Settlement{ClaimID: in.ID, YearIndex: yearIndex(spec, in.AccDay)}
	// 免赔预算：min(每次免赔, 年度上限-已扣, 基数)。
	var base int64
	var cov []Line
	for _, l := range in.Lines {
		if spec.ExcludedCodes[l.Code] {
			out.ExcludedAmount += l.Amount
			continue
		}
		cov = append(cov, l)
		base += l.Amount
	}
	out.CoveredBase = base
	dedBudget := spec.PerClaimDeductible
	if r := spec.AnnualDeductCap - dedUsed; r < dedBudget {
		dedBudget = r
	}
	if dedBudget > base {
		dedBudget = base
	}
	if dedBudget < 0 {
		dedBudget = 0
	}
	nominalDeduct := dedBudget
	oopRoom := spec.OOPCap - oopUsed
	if oopRoom < 0 {
		oopRoom = 0
	}

	type pre struct {
		lo   LineOut
		self int64 // 封顶前本明细自付（免赔+比例自付）
	}
	pres := make([]pre, 0, len(cov))
	var insTotal, selfTotal int64
	alreadyCapped := oopRoom == 0
	for _, l := range cov {
		lo := LineOut{Code: l.Code, Category: l.Category, Amount: l.Amount}
		if alreadyCapped {
			lo.Insurer = l.Amount
			lo.FullAfter = true
			insTotal += l.Amount
			out.Lines = append(out.Lines, lo)
			continue
		}
		rate := spec.InpatientRate
		if l.Category == CatOutpatient {
			rate = spec.OutpatientRate
		}
		// 阶段一：免赔按名义额逐条消耗（只受免赔预算约束，与封顶无关）。
		d := l.Amount
		if d > dedBudget {
			d = dedBudget
		}
		lo.Deduct = d
		dedBudget -= d
		// 阶段二：剩余按比例赔付、向上取整。
		rest := l.Amount - d
		paid := ceilDiv(rest*int64(rate), 100)
		lo.Insurer = paid
		lo.SelfPay = l.Amount - paid
		pres = append(pres, pre{lo: lo, self: lo.SelfPay})
	}
	if alreadyCapped {
		out.DeductApplied = 0
		out.InsurerPay = insTotal
		out.SelfPay = 0
		return out
	}

	// 阶段三：自付封顶。按明细次序放入自付，定位最后一条被截断明细；
	// 追加赔付归该条，其后明细全额赔付。
	var preSelf int64
	for _, p := range pres {
		preSelf += p.self
	}
	if preSelf <= oopRoom {
		for _, p := range pres {
			insTotal += p.lo.Insurer
			selfTotal += p.lo.SelfPay
			out.Lines = append(out.Lines, p.lo)
		}
		out.DeductApplied = nominalDeduct
		out.InsurerPay = insTotal
		out.SelfPay = selfTotal
		return out
	}

	consumed := int64(0)
	cross := len(pres) // 越界哨兵
	for i, p := range pres {
		if consumed+p.self > oopRoom {
			cross = i
			break
		}
		consumed += p.self
	}
	firstFull := cross + 1
	if cross < len(pres) && oopRoom-consumed == 0 {
		firstFull = cross // 恰好达到封顶：cross 这条本身全额
	}
	for i := 0; i < cross; i++ {
		insTotal += pres[i].lo.Insurer
		selfTotal += pres[i].lo.SelfPay
		out.Lines = append(out.Lines, pres[i].lo)
	}
	if cross < len(pres) && firstFull == cross+1 {
		allowed := oopRoom - consumed
		p := pres[cross].lo
		shift := pres[cross].self - allowed
		p.CapShift = shift
		p.Insurer += shift
		p.SelfPay = allowed
		insTotal += p.Insurer
		selfTotal += p.SelfPay
		out.Lines = append(out.Lines, p)
	}
	for i := firstFull; i < len(pres); i++ {
		p := pres[i].lo
		p.Deduct = 0
		p.Insurer = p.Amount
		p.SelfPay = 0
		p.FullAfter = true
		insTotal += p.Amount
		out.Lines = append(out.Lines, p)
	}
	out.DeductApplied = nominalDeduct
	out.InsurerPay = insTotal
	out.SelfPay = selfTotal
	return out
}

func (n *naiveEngine) replay(year int64) (ded, oop int64) {
	var d, o int64
	for _, c := range n.years[year] {
		r := naiveSettle(Claim{ID: c.id, AccDay: c.day, Lines: c.lines}, n.spec, d, o)
		d += r.DeductApplied
		o += r.SelfPay
	}
	return d, o
}

func (n *naiveEngine) submit(in Claim) (Settlement, error) {
	if !validClaim(in) {
		return Settlement{}, ErrInvalid
	}
	if n.exists[in.ID] {
		return Settlement{}, ErrClaimExists
	}
	if in.AccDay < n.spec.InceptDay {
		return Settlement{}, ErrNotCovered
	}
	y := yearIndex(n.spec, in.AccDay)
	d, o := n.replay(y)
	r := naiveSettle(in, n.spec, d, o)
	n.years[y] = append(n.years[y], &naiveClaim{id: in.ID, day: in.AccDay, lines: in.Lines, result: r})
	n.exists[in.ID] = true
	return r, nil
}

func (n *naiveEngine) cancel(id string) error {
	if id == "" {
		return ErrInvalid
	}
	if !n.exists[id] {
		return ErrClaimMissing
	}
	var y int64 = -1
	for yy, list := range n.years {
		if len(list) > 0 && list[len(list)-1].id == id {
			y = yy
		}
	}
	if y < 0 {
		return ErrNotLast
	}
	list := n.years[y]
	n.years[y] = list[:len(list)-1]
	delete(n.exists, id)
	return nil
}

func sameLineResult(a, b []LineOut) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func randSpec(rng *rand.Rand) PolicySpec {
	ex := map[string]bool{}
	if rng.Intn(2) == 0 {
		ex["EX"] = true
	}
	return PolicySpec{
		InceptDay:          int64(rng.Intn(5)),
		YearLen:            int64(1 + rng.Intn(20)),
		PerClaimDeductible: int64(rng.Intn(120)),
		AnnualDeductCap:    int64(rng.Intn(300)),
		InpatientRate:      rng.Intn(101),
		OutpatientRate:     rng.Intn(101),
		OOPCap:             int64(rng.Intn(400)),
		ExcludedCodes:      ex,
	}
}

func randClaim(rng *rand.Rand, id string, spec PolicySpec) Claim {
	n := 1 + rng.Intn(5)
	lines := make([]Line, 0, n)
	codes := []string{"X1", "X2", "X3", "EX"}
	for i := 0; i < n; i++ {
		cat := CatInpatient
		if rng.Intn(2) == 0 {
			cat = CatOutpatient
		}
		lines = append(lines, Line{
			Code:     codes[rng.Intn(len(codes))],
			Category: cat,
			Amount:   int64(1 + rng.Intn(150)),
		})
	}
	day := int64(-1 + rng.Intn(80)) // 偶尔早于承保起始日
	return Claim{ID: id, AccDay: day, Lines: lines}
}

func TestRandomDifferential(t *testing.T) {
	var log strings.Builder
	const sequences = 120
	const ops = 250
	totalOps := 0
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		spec := randSpec(rng)
		eng := NewEngine()
		if err := eng.RegisterPolicy("p", spec); err != nil {
			t.Fatalf("spec invalid: %+v %v", spec, err)
		}
		nav := newNaive(spec)
		counter := 0
		liveIDs := []string{}
		fmt.Fprintf(&log, "SEQ %d spec=%+v\n", seq, spec)

		for op := 0; op < ops; op++ {
			totalOps++
			action := rng.Intn(100)
			if action < 65 || len(liveIDs) == 0 {
				counter++
				id := fmt.Sprintf("C%d", counter)
				// 偶尔复用一个已存在号，制造“理赔已存在”。
				if len(liveIDs) > 0 && rng.Intn(10) == 0 {
					id = liveIDs[rng.Intn(len(liveIDs))]
				}
				in := randClaim(rng, id, spec)
				er, eerr := eng.Submit("p", in)
				nr, nerr := nav.submit(in)
				fmt.Fprintf(&log, "  submit %s day=%d lines=%v => eng(%v) naive(%v)\n",
					id, in.AccDay, in.Lines, fmtErr(eerr, er), fmtErr(nerr, nr))
				if !sameErr(eerr, nerr) {
					t.Fatalf("SEQ %d submit %+v err mismatch eng=%v naive=%v\n%s", seq, in, eerr, nerr, log.String())
				}
				if eerr == nil {
					if !cmpSettlement(er, nr) {
						t.Fatalf("SEQ %d submit %+v result mismatch\neng=%+v\nnaive=%+v\n%s", seq, in, er, nr, log.String())
					}
					liveIDs = append(liveIDs, id)
				}
			} else {
				// 撤销：多数撤销末笔，偶尔撤销随机笔/不存在号。
				var id string
				pick := rng.Intn(100)
				switch {
				case pick < 60:
					id = liveIDs[len(liveIDs)-1]
				case pick < 85:
					id = liveIDs[rng.Intn(len(liveIDs))]
				default:
					id = "GHOST"
				}
				eerr := eng.Cancel("p", id)
				nerr := nav.cancel(id)
				fmt.Fprintf(&log, "  cancel %s => eng(%v) naive(%v)\n", id, eerr, nerr)
				if !sameErr(eerr, nerr) {
					t.Fatalf("SEQ %d cancel %s err mismatch eng=%v naive=%v\n%s", seq, id, eerr, nerr, log.String())
				}
				if eerr == nil {
					liveIDs = liveIDs[:len(liveIDs)-1]
				}
			}

			// 每 25 步核对全部出现过的年度累计快照与朴素重放一致。
			if op%25 == 0 {
				hi := spec.InceptDay + spec.YearLen*5
				for day := spec.InceptDay; day < hi; day++ {
					y, ed, eo, _ := eng.YearSnapshot("p", day)
					nd, no := nav.replay(y)
					if ed != nd || eo != no {
						t.Fatalf("SEQ %d ledger mismatch year=%d eng(%d,%d) naive(%d,%d)\n%s",
							seq, y, ed, eo, nd, no, log.String())
					}
				}
			}
		}
	}
	t.Logf("random differential ops=%d\n%s", totalOps, log.String())
}

func fmtErr(err error, r Settlement) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("ins=%d self=%d ded=%d excl=%d lines=%+v",
		r.InsurerPay, r.SelfPay, r.DeductApplied, r.ExcludedAmount, r.Lines)
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Error() == b.Error()
}

func cmpSettlement(a, b Settlement) bool {
	return a.CoveredBase == b.CoveredBase &&
		a.DeductApplied == b.DeductApplied &&
		a.InsurerPay == b.InsurerPay &&
		a.SelfPay == b.SelfPay &&
		a.ExcludedAmount == b.ExcludedAmount &&
		a.YearIndex == b.YearIndex &&
		sameLineResult(a.Lines, b.Lines)
}
