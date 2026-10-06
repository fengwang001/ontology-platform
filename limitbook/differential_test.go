package limitbook

// 本文件是与引擎增量实现相互独立的朴素参考模型：完整保留操作日志，
// 每次结算都重扫全部活跃理赔来重算各层已用额。随机操作序列同时
// 作用于引擎与朴素模型，逐步比对错误类别与每条明细的赔付额，
// 并打印输入、输出与判定依据（go test -v 可见）。

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type nEndorse struct {
	target string // "m:"+成员 / "i:"+项目 / "f:"
	year   int64
	limit  int64
}

type nClaim struct {
	id      string
	member  string
	details []Detail
	paid    []int64
	years   []int64
}

type nModel struct {
	start, yearLen int64
	familyBase     int64
	memAnnual      map[string]int64
	memLifetime    map[string]int64
	itemBase       map[string]int64
	endorses       []nEndorse
	claims         []*nClaim // 活跃理赔，按受理次序
}

func newNModel(start, yearLen, family int64) *nModel {
	return &nModel{
		start:       start,
		yearLen:     yearLen,
		familyBase:  family,
		memAnnual:   map[string]int64{},
		memLifetime: map[string]int64{},
		itemBase:    map[string]int64{},
	}
}

func (n *nModel) yearOf(day int64) int64 { return (day - n.start) / n.yearLen }

// limitAt 扫描全部批改日志，取目标对象不超过年度 y 的最后一次登记。
func (n *nModel) limitAt(target string, base, y int64) int64 {
	lim := base
	for _, e := range n.endorses {
		if e.target == target && e.year <= y {
			lim = e.limit
		}
	}
	return lim
}

func (n *nModel) addMember(id string, annual, lifetime int64) error {
	if _, ok := n.memAnnual[id]; ok {
		return newErr(ErrMemberDuplicate, "成员 %s", id)
	}
	n.memAnnual[id] = annual
	n.memLifetime[id] = lifetime
	return nil
}

func (n *nModel) addItem(id string, annual int64) error {
	if _, ok := n.itemBase[id]; ok {
		return newErr(ErrItemDuplicate, "项目 %s", id)
	}
	n.itemBase[id] = annual
	return nil
}

func (n *nModel) maxDay() int64 {
	latest := int64(-1)
	for _, c := range n.claims {
		for _, d := range c.details {
			if d.Day > latest {
				latest = d.Day
			}
		}
	}
	return latest
}

func (n *nModel) endorse(target, id string, effDay, limit int64) error {
	if effDay < n.start {
		return newErr(ErrInvalidParam, "生效日 %d 早于承保起始日 %d", effDay, n.start)
	}
	switch target {
	case "m:":
		if _, ok := n.memAnnual[id]; !ok {
			return newErr(ErrMemberNotFound, "成员 %s", id)
		}
	case "i:":
		if _, ok := n.itemBase[id]; !ok {
			return newErr(ErrItemNotFound, "项目 %s", id)
		}
	}
	if md := n.maxDay(); effDay < md {
		return newErr(ErrRetroactive, "生效日 %d 早于最晚发生日 %d", effDay, md)
	}
	n.endorses = append(n.endorses, nEndorse{target: target + id, year: n.yearOf(effDay), limit: limit})
	return nil
}

// settle 朴素结算：校验次序与引擎一致，赔付额通过重扫全部活跃理赔重算。
// 返回结算结果、每条明细的判定依据（供日志打印）与错误。
func (n *nModel) settle(c Claim) (*Settlement, []string, error) {
	if _, ok := n.memAnnual[c.MemberID]; !ok {
		return nil, []string{fmt.Sprintf("成员 %s 未登记", c.MemberID)}, newErr(ErrMemberNotFound, "")
	}
	for _, d := range c.Details {
		if _, ok := n.itemBase[d.ItemID]; !ok {
			return nil, []string{fmt.Sprintf("项目 %s 未登记", d.ItemID)}, newErr(ErrItemNotFound, "")
		}
	}
	for _, oc := range n.claims {
		if oc.id == c.ID {
			return nil, []string{"理赔号已存在"}, newErr(ErrClaimExists, "")
		}
	}
	var lifeUsed int64
	for _, oc := range n.claims {
		if oc.member == c.MemberID {
			for _, pay := range oc.paid {
				lifeUsed += pay
			}
		}
	}
	if lifeUsed == n.memLifetime[c.MemberID] {
		reason := fmt.Sprintf("终身已用 %d == 终身限额 %d → 已封顶", lifeUsed, n.memLifetime[c.MemberID])
		return nil, []string{reason}, newErr(ErrCapped, "")
	}
	for _, d := range c.Details {
		if d.Day < n.start {
			reason := fmt.Sprintf("发生日 %d < 承保起始日 %d", d.Day, n.start)
			return nil, []string{reason}, newErr(ErrDayNotCovered, "")
		}
	}

	order := make([]int, len(c.Details))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		da, db := c.Details[order[a]], c.Details[order[b]]
		if da.ItemID != db.ItemID {
			return da.ItemID < db.ItemID
		}
		return da.Day < db.Day
	})

	paid := make([]int64, len(c.Details))
	years := make([]int64, len(c.Details))
	var total int64
	var reasons []string
	for pos, i := range order {
		d := c.Details[i]
		y := n.yearOf(d.Day)
		var itemUsed, memUsed, famUsed, lifeU int64
		add := func(member, item string, dy int64, pay int64) {
			if dy == y {
				famUsed += pay
			}
			if member == c.MemberID {
				lifeU += pay
				if dy == y {
					memUsed += pay
					if item == d.ItemID {
						itemUsed += pay
					}
				}
			}
		}
		for _, oc := range n.claims {
			for j, od := range oc.details {
				add(oc.member, od.ItemID, oc.years[j], oc.paid[j])
			}
		}
		for _, j := range order[:pos] {
			add(c.MemberID, c.Details[j].ItemID, years[j], paid[j])
		}
		itemRem := max(0, n.limitAt("i:"+d.ItemID, n.itemBase[d.ItemID], y)-itemUsed)
		memRem := max(0, n.limitAt("m:"+c.MemberID, n.memAnnual[c.MemberID], y)-memUsed)
		famRem := max(0, n.limitAt("f:", n.familyBase, y)-famUsed)
		lifeRem := n.memLifetime[c.MemberID] - lifeU
		pay := min(d.Amount, itemRem, memRem, famRem, lifeRem)
		which := "明细金额"
		if pay == itemRem {
			which = "项目年度剩余"
		} else if pay == memRem {
			which = "个人年度剩余"
		} else if pay == famRem {
			which = "家庭年度剩余"
		} else if pay == lifeRem {
			which = "终身剩余"
		}
		reasons = append(reasons, fmt.Sprintf(
			"明细#%d 项目=%s 日=%d 年度=%d: min(金额=%d, 项目剩余=%d, 个人剩余=%d, 家庭剩余=%d, 终身剩余=%d)=%d (%s)",
			i, d.ItemID, d.Day, y, d.Amount, itemRem, memRem, famRem, lifeRem, pay, which))
		paid[i] = pay
		years[i] = y
		total += pay
	}
	n.claims = append(n.claims, &nClaim{
		id:      c.ID,
		member:  c.MemberID,
		details: append([]Detail(nil), c.Details...),
		paid:    paid,
		years:   years,
	})
	return &Settlement{ClaimID: c.ID, Total: total, Paid: paid}, reasons, nil
}

func (n *nModel) reverse(id string) error {
	idx := -1
	for i, c := range n.claims {
		if c.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return newErr(ErrClaimNotFound, "理赔 %s", id)
	}
	last := ""
	for _, c := range n.claims {
		if c.member == n.claims[idx].member {
			last = c.id
		}
	}
	if last != id {
		return newErr(ErrNotLast, "理赔 %s 非末笔(末笔为 %s)", id, last)
	}
	n.claims = append(n.claims[:idx], n.claims[idx+1:]...)
	return nil
}

func errKind(err error) string {
	if err == nil {
		return "<成功>"
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Kind.String()
	}
	return err.Error()
}

func mustSameErr(t *testing.T, got, want error, op string) {
	t.Helper()
	var ge, we *Error
	gok := errors.As(got, &ge)
	wok := errors.As(want, &we)
	if gok != wok || (gok && ge.Kind != we.Kind) {
		t.Fatalf("%s: 引擎错误=%v, 朴素模型错误=%v", op, got, want)
	}
}

func paidOf(s *Settlement) []int64 {
	if s == nil {
		return nil
	}
	return s.Paid
}

// compareFinalState 在随机序列结束后，把引擎内部的增量账与朴素模型
// 全量重放的账逐桶比对，证明两者完全等价。
func compareFinalState(t *testing.T, p *Policy, n *nModel) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()

	prune := func(m map[int64]int64) map[int64]int64 {
		out := map[int64]int64{}
		for k, v := range m {
			if v != 0 {
				out[k] = v
			}
		}
		return out
	}

	famUsed := map[int64]int64{}
	memYear := map[string]map[int64]int64{}
	memItem := map[string]map[string]map[int64]int64{}
	memLife := map[string]int64{}
	for _, c := range n.claims {
		for i, d := range c.details {
			y := c.years[i]
			pay := c.paid[i]
			if pay == 0 {
				continue
			}
			famUsed[y] += pay
			if memYear[c.member] == nil {
				memYear[c.member] = map[int64]int64{}
			}
			memYear[c.member][y] += pay
			if memItem[c.member] == nil {
				memItem[c.member] = map[string]map[int64]int64{}
			}
			if memItem[c.member][d.ItemID] == nil {
				memItem[c.member][d.ItemID] = map[int64]int64{}
			}
			memItem[c.member][d.ItemID][y] += pay
			memLife[c.member] += pay
		}
	}

	if !reflect.DeepEqual(prune(p.familyUsed), prune(famUsed)) {
		t.Fatalf("家庭已用不一致: 引擎 %v, 朴素 %v", prune(p.familyUsed), prune(famUsed))
	}
	for id, m := range p.members {
		if !reflect.DeepEqual(prune(m.yearUsed), prune(memYear[id])) {
			t.Fatalf("成员 %s 年度已用不一致: 引擎 %v, 朴素 %v", id, prune(m.yearUsed), prune(memYear[id]))
		}
		if m.lifetimeUsed != memLife[id] {
			t.Fatalf("成员 %s 终身已用不一致: 引擎 %d, 朴素 %d", id, m.lifetimeUsed, memLife[id])
		}
		gotItem := map[string]map[int64]int64{}
		for it, ym := range m.itemUsed {
			if pruned := prune(ym); len(pruned) > 0 {
				gotItem[it] = pruned
			}
		}
		wantItem := map[string]map[int64]int64{}
		for it, ym := range memItem[id] {
			if pruned := prune(ym); len(pruned) > 0 {
				wantItem[it] = pruned
			}
		}
		if !reflect.DeepEqual(gotItem, wantItem) {
			t.Fatalf("成员 %s 项目已用不一致: 引擎 %v, 朴素 %v", id, gotItem, wantItem)
		}
		var wantStack []string
		for _, c := range n.claims {
			if c.member == id {
				wantStack = append(wantStack, c.id)
			}
		}
		if len(m.claims) != 0 || len(wantStack) != 0 {
			if !reflect.DeepEqual(m.claims, wantStack) {
				t.Fatalf("成员 %s 理赔栈不一致: 引擎 %v, 朴素 %v", id, m.claims, wantStack)
			}
		}
	}
	if p.maxDay != n.maxDay() {
		t.Fatalf("最晚发生日不一致: 引擎 %d, 朴素 %d", p.maxDay, n.maxDay())
	}
	if len(p.claims) != len(n.claims) {
		t.Fatalf("活跃理赔数不一致: 引擎 %d, 朴素 %d", len(p.claims), len(n.claims))
	}
	t.Logf("终态一致: 活跃理赔 %d 笔, 最晚发生日 %d", len(n.claims), n.maxDay())
}

// TestDifferentialRandom 对随机理赔、批改与冲正序列做引擎 vs 朴素模型对照，
// 日志打印每步的输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	for _, seed := range []int64{20261006, 1487, 42, 7} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			start := int64(r.Intn(60))
			yearLen := int64(1 + r.Intn(365))
			family := int64(r.Intn(4000))
			eng := NewEngine()
			mustOK(t, eng.RegisterPolicy("P", start, yearLen, family))
			n := newNModel(start, yearLen, family)
			t.Logf("保单: start=%d yearLen=%d family=%d", start, yearLen, family)

			members := []string{"M0", "M1", "M2", "M3"}
			items := []string{"I0", "I1", "I2"}
			for _, m := range members {
				a, l := int64(r.Intn(3000)), int64(r.Intn(9000))
				mustOK(t, eng.RegisterMember("P", m, a, l))
				mustOK(t, n.addMember(m, a, l))
				t.Logf("登记成员 %s: 年度限额=%d 终身限额=%d", m, a, l)
			}
			for _, it := range items {
				a := int64(r.Intn(1500))
				mustOK(t, eng.RegisterItem("P", it, a))
				mustOK(t, n.addItem(it, a))
				t.Logf("登记项目 %s: 年度项目限额=%d", it, a)
			}

			var active []string
			claimSeq := 0
			const ops = 1200
			for i := 0; i < ops; i++ {
				switch x := r.Intn(100); {
				case x < 50: // 理赔
					c := Claim{ID: fmt.Sprintf("C%d", claimSeq), MemberID: members[r.Intn(len(members))]}
					claimSeq++
					if r.Intn(10) == 0 && len(active) > 0 {
						c.ID = active[r.Intn(len(active))] // 注入重复理赔号
					}
					if r.Intn(15) == 0 {
						c.MemberID = "GHOST"
					}
					nd := 1 + r.Intn(4)
					for j := 0; j < nd; j++ {
						it := items[r.Intn(len(items))]
						if r.Intn(15) == 0 {
							it = "GX"
						}
						day := int64(r.Intn(int(start + 4*yearLen + 1)))
						amt := int64(1 + r.Intn(1200))
						c.Details = append(c.Details, Detail{ItemID: it, Day: day, Amount: amt})
					}
					gotS, gotErr := eng.Settle("P", c)
					wantS, reasons, wantErr := n.settle(c)
					mustSameErr(t, gotErr, wantErr, fmt.Sprintf("op%d 理赔 %s", i, c.ID))
					if gotErr == nil {
						if gotS.Total != wantS.Total || !reflect.DeepEqual(gotS.Paid, wantS.Paid) {
							t.Fatalf("op%d 赔付不一致: 引擎 %v, 朴素 %v", i, gotS, wantS)
						}
						active = append(active, c.ID)
					}
					t.Logf("op%d 理赔 %+v -> 赔付=%v 结果=%s | 依据: %s",
						i, c, paidOf(gotS), errKind(gotErr), strings.Join(reasons, "；"))
				case x < 65: // 冲正
					id := fmt.Sprintf("C%d", r.Intn(claimSeq+3))
					if len(active) > 0 && r.Intn(10) < 8 {
						id = active[r.Intn(len(active))]
					}
					gotErr := eng.Reverse("P", id)
					wantErr := n.reverse(id)
					mustSameErr(t, gotErr, wantErr, fmt.Sprintf("op%d 冲正 %s", i, id))
					if gotErr == nil {
						for j, a := range active {
							if a == id {
								active = append(active[:j], active[j+1:]...)
								break
							}
						}
					}
					t.Logf("op%d 冲正 %s -> 结果=%s", i, id, errKind(gotErr))
				case x < 85: // 批改
					md := n.maxDay()
					var effDay int64
					switch r.Intn(3) {
					case 0:
						effDay = md - 2 + int64(r.Intn(5)) // 围绕最晚发生日，覆盖取等边界
					case 1:
						effDay = start + int64(r.Intn(int(4*yearLen)))
					default:
						effDay = md
					}
					limit := int64(r.Intn(2500))
					var engErr, nErr error
					var desc string
					switch r.Intn(3) {
					case 0:
						id := members[r.Intn(len(members))]
						if r.Intn(15) == 0 {
							id = "GHOST"
						}
						engErr = eng.EndorseMemberAnnual("P", id, effDay, limit)
						nErr = n.endorse("m:", id, effDay, limit)
						desc = "成员" + id
					case 1:
						id := items[r.Intn(len(items))]
						if r.Intn(15) == 0 {
							id = "GX"
						}
						engErr = eng.EndorseItemAnnual("P", id, effDay, limit)
						nErr = n.endorse("i:", id, effDay, limit)
						desc = "项目" + id
					default:
						engErr = eng.EndorseFamilyAnnual("P", effDay, limit)
						nErr = n.endorse("f:", "", effDay, limit)
						desc = "家庭"
					}
					mustSameErr(t, engErr, nErr, fmt.Sprintf("op%d 批改 %s", i, desc))
					t.Logf("op%d 批改 %s 生效日=%d 新限额=%d (最晚发生日=%d) -> 结果=%s",
						i, desc, effDay, limit, md, errKind(engErr))
				default: // 登记（新对象或重复）
					if r.Intn(2) == 0 {
						id := members[r.Intn(len(members))]
						if r.Intn(2) == 0 {
							id = fmt.Sprintf("M%d", len(members))
						}
						a, l := int64(r.Intn(3000)), int64(r.Intn(9000))
						engErr := eng.RegisterMember("P", id, a, l)
						nErr := n.addMember(id, a, l)
						mustSameErr(t, engErr, nErr, fmt.Sprintf("op%d 登记成员 %s", i, id))
						if engErr == nil {
							members = append(members, id)
						}
						t.Logf("op%d 登记成员 %s 年度=%d 终身=%d -> 结果=%s", i, id, a, l, errKind(engErr))
					} else {
						id := items[r.Intn(len(items))]
						if r.Intn(2) == 0 {
							id = fmt.Sprintf("I%d", len(items))
						}
						a := int64(r.Intn(1500))
						engErr := eng.RegisterItem("P", id, a)
						nErr := n.addItem(id, a)
						mustSameErr(t, engErr, nErr, fmt.Sprintf("op%d 登记项目 %s", i, id))
						if engErr == nil {
							items = append(items, id)
						}
						t.Logf("op%d 登记项目 %s 年度项目限额=%d -> 结果=%s", i, id, a, errKind(engErr))
					}
				}
			}
			compareFinalState(t, eng.policies["P"], n)
		})
	}
}
