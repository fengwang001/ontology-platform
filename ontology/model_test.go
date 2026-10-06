package ontology

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
)

// ---------- 朴素模型：与引擎优化实现相互独立的参照实现 ----------
// 设计取向刻意相反：不做任何增量记账，所有已用额都通过线性扫描
// 全部有效理赔现算，限额通过线性扫描全部批改现取，结算复杂度
// 随历史理赔数线性增长。随机对照测试用它验证引擎的增量账本。

type nLine struct {
	item string
	day  int64
	year int64
	paid int64
}

type nClaim struct {
	id     string
	member string
	lines  []nLine
}

type nEndorse struct {
	kind   EndorseKind
	object string
	year   int64
	limit  int64
}

type naiveModel struct {
	in       PolicyInput
	claims   []nClaim // 有效理赔，受理次序
	endorses []nEndorse
}

func newNaiveModel(in PolicyInput) *naiveModel { return &naiveModel{in: in} }

func (m *naiveModel) yearOf(day int64) int64 {
	return (day - m.in.StartDay) / m.in.YearLength
}

func (m *naiveModel) memberBase(id string) (MemberInput, bool) {
	for _, mb := range m.in.Members {
		if mb.ID == id {
			return mb, true
		}
	}
	return MemberInput{}, false
}

func (m *naiveModel) itemBase(id string) (ItemInput, bool) {
	for _, it := range m.in.Items {
		if it.ID == id {
			return it, true
		}
	}
	return ItemInput{}, false
}

// limit 线性扫描全部批改，取生效年度 <= year 的最后一条（注册次序覆盖）。
func (m *naiveModel) limit(kind EndorseKind, object string, year, base int64) int64 {
	v := base
	for _, e := range m.endorses {
		if e.kind == kind && e.object == object && e.year <= year {
			v = e.limit
		}
	}
	return v
}

func (m *naiveModel) usedItem(memberID, itemID string, year int64) int64 {
	var s int64
	for _, c := range m.claims {
		if c.member != memberID {
			continue
		}
		for _, ln := range c.lines {
			if ln.item == itemID && ln.year == year {
				s += ln.paid
			}
		}
	}
	return s
}

func (m *naiveModel) usedMember(memberID string, year int64) int64 {
	var s int64
	for _, c := range m.claims {
		if c.member != memberID {
			continue
		}
		for _, ln := range c.lines {
			if ln.year == year {
				s += ln.paid
			}
		}
	}
	return s
}

func (m *naiveModel) usedFamily(year int64) int64 {
	var s int64
	for _, c := range m.claims {
		for _, ln := range c.lines {
			if ln.year == year {
				s += ln.paid
			}
		}
	}
	return s
}

func (m *naiveModel) usedLifetime(memberID string) int64 {
	var s int64
	for _, c := range m.claims {
		if c.member != memberID {
			continue
		}
		for _, ln := range c.lines {
			s += ln.paid
		}
	}
	return s
}

func (m *naiveModel) maxDay() (int64, bool) {
	var max int64
	ok := false
	for _, c := range m.claims {
		for _, ln := range c.lines {
			if !ok || ln.day > max {
				max, ok = ln.day, true
			}
		}
	}
	return max, ok
}

func nRemaining(limit, used int64) int64 {
	if r := limit - used; r > 0 {
		return r
	}
	return 0
}

var layerNames = []string{"明细金额", "项目年度限额", "个人年度限额", "家庭年度限额", "个人终身限额"}

// settle 返回每条明细（按输入顺序）的赔付额与判定依据（成为最小值的层）。
func (m *naiveModel) settle(in ClaimInput) ([]int64, []string, *Error) {
	if in.ID == "" || in.MemberID == "" || len(in.Lines) == 0 {
		return nil, nil, errOf(ErrInvalidParam, "", "理赔参数非法")
	}
	for _, ln := range in.Lines {
		if ln.ItemID == "" || ln.Day < 0 || ln.Amount <= 0 {
			return nil, nil, errOf(ErrInvalidParam, "", "明细参数非法")
		}
	}
	mb, ok := m.memberBase(in.MemberID)
	if !ok {
		return nil, nil, errOf(ErrMemberNotFound, "", in.MemberID)
	}
	for _, ln := range in.Lines {
		if _, ok := m.itemBase(ln.ItemID); !ok {
			return nil, nil, errOf(ErrItemNotFound, "", ln.ItemID)
		}
	}
	for _, c := range m.claims {
		if c.id == in.ID {
			return nil, nil, errOf(ErrClaimExists, "", in.ID)
		}
	}
	if nRemaining(mb.LifetimeLimit, m.usedLifetime(in.MemberID)) == 0 {
		return nil, nil, errOf(ErrCapped, "", in.MemberID)
	}
	for _, ln := range in.Lines {
		if ln.Day < m.in.StartDay {
			return nil, nil, errOf(ErrDayNotCovered, "", in.ID)
		}
	}

	order := make([]int, len(in.Lines))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		la, lb := in.Lines[order[a]], in.Lines[order[b]]
		if la.ItemID != lb.ItemID {
			return la.ItemID < lb.ItemID
		}
		return la.Day < lb.Day
	})

	m.claims = append(m.claims, nClaim{id: in.ID, member: in.MemberID})
	cur := &m.claims[len(m.claims)-1]
	payouts := make([]int64, len(in.Lines))
	bindings := make([]string, len(in.Lines))
	for _, idx := range order {
		ln := in.Lines[idx]
		year := m.yearOf(ln.Day)
		ib, _ := m.itemBase(ln.ItemID)
		cands := []int64{
			ln.Amount,
			nRemaining(m.limit(EndorseItemAnnual, ln.ItemID, year, ib.AnnualLimit), m.usedItem(in.MemberID, ln.ItemID, year)),
			nRemaining(m.limit(EndorseMemberAnnual, in.MemberID, year, mb.AnnualLimit), m.usedMember(in.MemberID, year)),
			nRemaining(m.limit(EndorseFamilyAnnual, "", year, m.in.FamilyAnnualLimit), m.usedFamily(year)),
			nRemaining(mb.LifetimeLimit, m.usedLifetime(in.MemberID)),
		}
		pay, binding := cands[0], 0
		for i, c := range cands {
			if c < pay {
				pay, binding = c, i
			}
		}
		payouts[idx] = pay
		bindings[idx] = layerNames[binding]
		cur.lines = append(cur.lines, nLine{item: ln.ItemID, day: ln.Day, year: year, paid: pay})
	}
	return payouts, bindings, nil
}

func (m *naiveModel) reverse(id string) *Error {
	if id == "" {
		return errOf(ErrInvalidParam, "", "理赔号为空")
	}
	idx := -1
	for i, c := range m.claims {
		if c.id == id {
			idx = i
		}
	}
	if idx < 0 {
		return errOf(ErrClaimNotFound, "", id)
	}
	memberID := m.claims[idx].member
	last := -1
	for i, c := range m.claims {
		if c.member == memberID {
			last = i
		}
	}
	if last != idx {
		return errOf(ErrNotLastClaim, "", id)
	}
	m.claims = append(m.claims[:idx], m.claims[idx+1:]...)
	return nil
}

func (m *naiveModel) endorse(kind EndorseKind, object string, day, limit int64) *Error {
	if day < 0 || limit < 0 {
		return errOf(ErrInvalidParam, "", "批改参数非法")
	}
	if kind != EndorseMemberAnnual && kind != EndorseItemAnnual && kind != EndorseFamilyAnnual {
		return errOf(ErrInvalidParam, "", "未知批改类型")
	}
	if kind != EndorseFamilyAnnual && object == "" {
		return errOf(ErrInvalidParam, "", "批改对象为空")
	}
	if day < m.in.StartDay {
		return errOf(ErrInvalidParam, "", "批改参数非法")
	}
	switch kind {
	case EndorseMemberAnnual:
		if _, ok := m.memberBase(object); !ok {
			return errOf(ErrMemberNotFound, "", object)
		}
	case EndorseItemAnnual:
		if _, ok := m.itemBase(object); !ok {
			return errOf(ErrItemNotFound, "", object)
		}
	}
	if maxDay, ok := m.maxDay(); ok && day < maxDay {
		return errOf(ErrRetroactive, "", "")
	}
	m.endorses = append(m.endorses, nEndorse{kind: kind, object: object, year: m.yearOf(day), limit: limit})
	return nil
}

func (m *naiveModel) snapshot(memberID, itemID string, year int64) (Snapshot, *Error) {
	mb, ok := m.memberBase(memberID)
	if !ok {
		return Snapshot{}, errOf(ErrMemberNotFound, "", memberID)
	}
	ib, ok := m.itemBase(itemID)
	if !ok {
		return Snapshot{}, errOf(ErrItemNotFound, "", itemID)
	}
	lifeRem := nRemaining(mb.LifetimeLimit, m.usedLifetime(memberID))
	return Snapshot{
		MemberAnnualRemaining: nRemaining(m.limit(EndorseMemberAnnual, memberID, year, mb.AnnualLimit), m.usedMember(memberID, year)),
		ItemAnnualRemaining:   nRemaining(m.limit(EndorseItemAnnual, itemID, year, ib.AnnualLimit), m.usedItem(memberID, itemID, year)),
		FamilyAnnualRemaining: nRemaining(m.limit(EndorseFamilyAnnual, "", year, m.in.FamilyAnnualLimit), m.usedFamily(year)),
		LifetimeRemaining:     lifeRem,
		Capped:                lifeRem == 0,
	}, nil
}

// ---------- 随机对照：引擎 vs 朴素模型 ----------

func TestRandomizedAgainstNaiveModel(t *testing.T) {
	for _, seed := range []uint64{1, 7, 42, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomOps(t, seed)
		})
	}
}

func errCodeOf(t *testing.T, err error) ErrCode {
	t.Helper()
	if err == nil {
		return 0
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("非引擎错误: %v", err)
	}
	return e.Code
}

func runRandomOps(t *testing.T, seed uint64) {
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b9))
	in := PolicyInput{
		ID:                testPolicyID,
		StartDay:          100,
		YearLength:        10,
		FamilyAnnualLimit: int64(200 + rng.IntN(1800)),
	}
	for i := 0; i < 4; i++ {
		in.Members = append(in.Members, MemberInput{
			ID:            fmt.Sprintf("m%d", i),
			AnnualLimit:   int64(50 + rng.IntN(350)),
			LifetimeLimit: int64(100 + rng.IntN(700)),
		})
	}
	for i := 0; i < 5; i++ {
		in.Items = append(in.Items, ItemInput{
			ID:          fmt.Sprintf("i%d", i),
			AnnualLimit: int64(40 + rng.IntN(260)),
		})
	}
	e := NewEngine()
	if err := e.RegisterPolicy(in); err != nil {
		t.Fatalf("登记保单失败: %v", err)
	}
	model := newNaiveModel(in)
	t.Logf("保单登记: %+v", in)

	memberIDs := []string{"m0", "m1", "m2", "m3", "ghost"}
	itemIDs := []string{"i0", "i1", "i2", "i3", "i4", "ghost"}
	claimIDs := make([]string, 30)
	for i := range claimIDs {
		claimIDs[i] = fmt.Sprintf("c%d", i)
	}
	pick := func(s []string) string { return s[rng.IntN(len(s))] }

	for op := 0; op < 400; op++ {
		switch dice := rng.IntN(100); {
		case dice < 55: // 结算
			ci := ClaimInput{ID: pick(claimIDs), MemberID: pick(memberIDs)}
			for n := 1 + rng.IntN(3); n > 0; n-- {
				ci.Lines = append(ci.Lines, LineInput{
					ItemID: pick(itemIDs),
					Day:    int64(95 + rng.IntN(70)),
					Amount: int64(1 + rng.IntN(300)),
				})
			}
			if rng.IntN(10) == 0 { // 10% 制造非法参数
				switch rng.IntN(3) {
				case 0:
					ci.ID = ""
				case 1:
					ci.Lines[0].Amount = -int64(rng.IntN(5))
				case 2:
					ci.Lines[0].Day = -int64(1 + rng.IntN(5))
				}
			}
			payE, errE := e.SettleClaim(testPolicyID, ci)
			payM, bindM, errM := model.settle(ci)
			codeE, codeM := errCodeOf(t, errE), errCodeOf(t, err2(errM))
			t.Logf("op=%d 结算 输入=%+v 引擎=(%v,%v) 模型=(%v,%v) 判定依据=%v",
				op, ci, payE, codeE, payM, codeM, bindM)
			if codeE != codeM {
				t.Fatalf("op=%d 错误码不一致: 引擎=%v 模型=%v 输入=%+v", op, codeE, codeM, ci)
			}
			if codeE == 0 {
				if len(payE) != len(payM) {
					t.Fatalf("op=%d 赔付明细数不一致: 引擎=%v 模型=%v", op, payE, payM)
				}
				for i := range payE {
					if payE[i] != payM[i] {
						t.Fatalf("op=%d 赔付额不一致: 引擎=%v 模型=%v 输入=%+v", op, payE, payM, ci)
					}
				}
			}
		case dice < 75: // 批改
			kind := EndorseKind(1 + rng.IntN(3))
			object := ""
			switch kind {
			case EndorseMemberAnnual:
				object = pick(memberIDs)
			case EndorseItemAnnual:
				object = pick(itemIDs)
			}
			day := int64(95 + rng.IntN(70))
			limit := int64(rng.IntN(400))
			errE := e.Endorse(testPolicyID, kind, object, day, limit)
			errM := model.endorse(kind, object, day, limit)
			codeE, codeM := errCodeOf(t, errE), errCodeOf(t, err2(errM))
			t.Logf("op=%d 批改 类型=%d 对象=%s 生效日=%d 限额=%d 引擎=%v 模型=%v",
				op, kind, object, day, limit, codeE, codeM)
			if codeE != codeM {
				t.Fatalf("op=%d 批改错误码不一致: 引擎=%v 模型=%v", op, codeE, codeM)
			}
		case dice < 90: // 冲正
			id := pick(claimIDs)
			if rng.IntN(10) == 0 {
				id = "ghost"
			}
			errE := e.ReverseClaim(testPolicyID, id)
			errM := model.reverse(id)
			codeE, codeM := errCodeOf(t, errE), errCodeOf(t, err2(errM))
			t.Logf("op=%d 冲正 理赔号=%s 引擎=%v 模型=%v", op, id, codeE, codeM)
			if codeE != codeM {
				t.Fatalf("op=%d 冲正错误码不一致: 引擎=%v 模型=%v", op, codeE, codeM)
			}
		default: // 快照对拍
			memberID := memberIDs[rng.IntN(4)]
			itemID := itemIDs[rng.IntN(5)]
			year := int64(rng.IntN(6))
			snapE, errE := e.Snapshot(testPolicyID, memberID, itemID, year)
			snapM, errM := model.snapshot(memberID, itemID, year)
			codeE, codeM := errCodeOf(t, errE), errCodeOf(t, err2(errM))
			if codeE != codeM {
				t.Fatalf("op=%d 快照错误码不一致: 引擎=%v 模型=%v", op, codeE, codeM)
			}
			if codeE == 0 && snapE != snapM {
				t.Fatalf("op=%d 快照不一致: 引擎=%+v 模型=%+v (member=%s item=%s year=%d)",
					op, snapE, snapM, memberID, itemID, year)
			}
			t.Logf("op=%d 快照 member=%s item=%s year=%d 引擎=%+v 模型=%+v",
				op, memberID, itemID, year, snapE, snapM)
		}
	}
}

func err2(e *Error) error {
	if e == nil {
		return nil
	}
	return e
}
