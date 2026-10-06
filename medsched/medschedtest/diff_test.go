package medschedtest

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"ontology/medsched"
)

func asErr(err error, target **medsched.Error) bool { return errors.As(err, target) }

// 朴素状态码（0-based: pending,onTime,refused,madeUp,missed,void）
// -> 生产状态码（1-based）。
func canonicalNaiveStatus(s int) int {
	switch s {
	case 0:
		return int(medsched.StatusPending)
	case 1:
		return int(medsched.StatusGivenOnTime)
	case 2:
		return int(medsched.StatusRefused)
	case 3:
		return int(medsched.StatusMadeUp)
	case 4:
		return int(medsched.StatusMissed)
	case 5:
		return int(medsched.StatusVoid)
	}
	return 0
}

type rng struct{ state uint64 }

func (r *rng) next() uint64 {
	r.state ^= r.state << 13
	r.state ^= r.state >> 7
	r.state ^= r.state << 17
	return r.state
}

func (r *rng) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

func (r *rng) int63n(n int64) int64 {
	return int64((r.next() & (1<<62 - 1)) % uint64(n))
}

func pick(r *rng, xs []int64) []int64 {
	// 随机挑选一个非空、保持升序的子集。
	var out []int64
	for _, x := range xs {
		if r.intn(2) == 1 {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		out = []int64{xs[r.intn(len(xs))]}
	}
	return out
}

type harness struct {
	t     *testing.T
	sys   *medsched.System
	nv    *Naive
	logf  *os.File
	w     int64
	drugs []string
	pats  []string

	// 真实医嘱ID -> 朴素医嘱ID。
	id2n map[string]string
	// 朴素ID -> 真实ID。
	n2id map[string]string

	// 记录每张朴素医嘱最近的关键计划点，便于构造补给。
	lastPlanned map[string]int64
	drugMins    map[string]int64
}

func (h *harness) log(format string, args ...any) {
	fmt.Fprintf(h.logf, format+"\n", args...)
}

func (h *harness) basis(err error, nr NResult) string {
	if nr.OK {
		return "接受"
	}
	return "拒绝:" + codeName(nr.Code)
}

func codeName(c int) string {
	switch c {
	case cInvalid:
		return "参数非法"
	case cClock:
		return "时钟回退"
	case cNotFound:
		return "对象不存在"
	case cState:
		return "状态不符"
	case cAllergy:
		return "过敏冲突"
	case cNoPoint:
		return "无对应计划点"
	case cInterval:
		return "间隔不足"
	case cLimit:
		return "次数超限"
	case cMakeup:
		return "补给不允许"
	}
	return "?"
}

func sysCode(err error) int {
	if err == nil {
		return 0
	}
	var e *medsched.Error
	if asErr(err, &e) {
		return int(e.Code)
	}
	return -1
}

func (h *harness) compare(tag string, err error, nr NResult) {
	sc := sysCode(err)
	if nr.OK {
		if err != nil {
			h.t.Fatalf("[%s] 朴素接受但生产拒绝 code=%d", tag, sc)
		}
		return
	}
	if sc != nr.Code {
		h.t.Fatalf("[%s] 错误码不一致: 生产=%d(%s) 朴素=%d(%s)",
			tag, sc, codeName(sc), nr.Code, codeName(nr.Code))
	}
}

func freqFor(r *rng, kind int, w int64, minSafe int64, now int64) (medsched.Frequency, OpenSpec) {
	switch kind {
	case 1:
		// H > max(2W, minSafe)，首点不早于 now。
		h := max64(2*w+1, minSafe) + int64(r.intn(50))
		first := now + int64(r.intn(60))
		return medsched.Frequency{Kind: medsched.FreqInterval, First: first, H: h},
			OpenSpec{Kind: 1, First: first, H: h}
	case 2:
		// 保证相邻差 > max(2W,minSafe)，在 [0,86399] 内稀疏取点。
		gap := max64(2*w+1, minSafe)
		candidates := []int64{}
		for t := int64(0); t < daySecs; t += gap + 20 {
			candidates = append(candidates, t)
		}
		times := pick(r, candidates)
		return medsched.Frequency{Kind: medsched.FreqDaily, Times: times},
			OpenSpec{Kind: 2, Times: times}
	default:
		prnMin := int64(1 + r.intn(50))
		lim := int64(1 + r.intn(3))
		return medsched.Frequency{Kind: medsched.FreqPRN, PRNMin: prnMin, PRNLimit: lim},
			OpenSpec{Kind: 3, PRNMin: prnMin, PRNLimit: lim}
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (h *harness) openOrder(r *rng, now int64) {
	pat := h.pats[r.intn(len(h.pats))]
	drug := h.drugs[r.intn(len(h.drugs))]
	kind := 1 + r.intn(3)
	minSafe := h.drugMin(drug)
	sf, nf := freqFor(r, kind, h.w, minSafe, now)
	nf.Now, nf.Patient, nf.Drug = now, pat, drug
	h.log("操作 Open now=%d patient=%s drug=%s kind=%d", now, pat, drug, kind)
	id, err := h.sys.OpenOrder(medsched.OpenOrderInput{Now: now, Patient: pat, Drug: drug, Frequency: sf})
	nr := h.nv.Open(nf)
	h.compare("Open", err, nr)
	if nr.OK {
		h.id2n[id] = nr.ID
		h.n2id[nr.ID] = id
		h.lastPlanned[nr.ID] = -1
		h.log("  -> 接受 真实=%s 朴素=%s 依据=校验通过", id, nr.ID)
	} else {
		h.log("  -> 拒绝 依据=%s", codeName(nr.Code))
	}
}

func (h *harness) drugMin(drug string) int64 {
	// 通过一次无效查询不方便暴露；直接在测试侧记录一份。
	return h.drugMins[drug]
}

func (h *harness) randomNvOrder(r *rng) (string, bool) {
	if len(h.n2id) == 0 {
		return "", false
	}
	ids := make([]string, 0, len(h.n2id))
	for k := range h.n2id {
		ids = append(ids, k)
	}
	return ids[r.intn(len(ids))], true
}

// kindOfNv 返回朴素医嘱频次种类。
func (h *harness) kindOfNv(nid string) int { return h.nv.orders[nid].kind }

func (h *harness) administer(r *rng, now int64) {
	nid, ok := h.randomNvOrder(r)
	if !ok {
		return
	}
	rid := h.n2id[nid]
	h.log("操作 Administer now=%d order=%s", now, rid)
	err := h.sys.Administer(now, rid)
	nr := h.nv.Administer(now, nid)
	h.compare("Administer", err, nr)
	h.log("  -> %s", h.basis(err, nr))
}

func (h *harness) refuse(r *rng, now int64) {
	nid, ok := h.randomNvOrder(r)
	if !ok {
		return
	}
	rid := h.n2id[nid]
	h.log("操作 Refuse now=%d order=%s", now, rid)
	err := h.sys.Refuse(now, rid)
	nr := h.nv.Refuse(now, nid)
	h.compare("Refuse", err, nr)
	h.log("  -> %s", h.basis(err, nr))
}

// chooseMakeupTarget 朴素地找一个当前可能漏给的点；找不到返回 false。
func (h *harness) chooseMakeupTarget(r *rng, now int64, nid string) (int64, bool) {
	o := h.nv.orders[nid]
	var cand []int64
	for _, p := range o.points {
		if (p.status == nMissed || p.status == nPending) && p.t < now {
			cand = append(cand, p.t)
		}
	}
	if len(cand) == 0 {
		return 0, false
	}
	// 有时故意取边界时刻附近；这里随机挑一个。
	return cand[r.intn(len(cand))], true
}

func (h *harness) makeUp(r *rng, now int64) {
	nid, ok := h.randomNvOrder(r)
	if !ok || h.kindOfNv(nid) == 3 {
		return
	}
	planned, ok := h.chooseMakeupTarget(r, now, nid)
	if !ok {
		return
	}
	rid := h.n2id[nid]
	h.log("操作 MakeUp now=%d order=%s planned=%d", now, rid, planned)
	err := h.sys.MakeUp(now, rid, planned)
	nr := h.nv.MakeUp(now, nid, planned)
	h.compare("MakeUp", err, nr)
	h.log("  -> %s", h.basis(err, nr))
}

func (h *harness) stop(r *rng, now int64) {
	nid, ok := h.randomNvOrder(r)
	if !ok {
		return
	}
	rid := h.n2id[nid]
	h.log("操作 Stop now=%d order=%s", now, rid)
	err := h.sys.StopOrder(now, rid)
	nr := h.nv.Stop(now, nid)
	h.compare("Stop", err, nr)
	h.log("  -> %s", h.basis(err, nr))
}

func (h *harness) revise(r *rng, now int64) {
	nid, ok := h.randomNvOrder(r)
	if !ok {
		return
	}
	rid := h.n2id[nid]
	pat := h.nv.orders[nid].patient
	drug := h.drugs[r.intn(len(h.drugs))]
	kind := 1 + r.intn(3)
	sf, nf := freqFor(r, kind, h.w, h.drugMin(drug), now)
	nf.Now, nf.Patient, nf.Drug = now, pat, drug
	h.log("操作 Revise now=%d old=%s drug=%s kind=%d", now, rid, drug, kind)
	newID, err := h.sys.ReviseOrder(now, rid, pat, drug, sf)
	nr := h.nv.Revise(now, nid, nf)
	h.compare("Revise", err, nr)
	if nr.OK {
		h.id2n[newID] = nr.ID
		h.n2id[nr.ID] = newID
		h.lastPlanned[nr.ID] = -1
		h.log("  -> 接受 new=%s/%s", newID, nr.ID)
	} else {
		h.log("  -> %s", h.basis(err, nr))
	}
}

func (h *harness) prn(r *rng, now int64) {
	nid, ok := h.randomNvOrder(r)
	if !ok || h.kindOfNv(nid) != 3 {
		return
	}
	rid := h.n2id[nid]
	h.log("操作 PRN now=%d order=%s", now, rid)
	err := h.sys.AdministerPRN(now, rid)
	nr := h.nv.PRN(now, nid)
	h.compare("PRN", err, nr)
	h.log("  -> %s", h.basis(err, nr))
}

func (h *harness) query(r *rng, now int64) {
	pat := h.pats[r.intn(len(h.pats))]
	lo := int64(0)
	if r.intn(2) == 0 && now > 100 {
		lo = now - int64(r.intn(100))
	}
	hi := now + int64(r.intn(200))
	if hi > horizon {
		hi = horizon
	}
	if lo > hi {
		return
	}
	h.log("操作 Query now=%d patient=%s [%d,%d]", now, pat, lo, hi)
	pts, err := h.sys.Query(now, pat, lo, hi)
	nvpts, nr := h.nv.Query(now, pat, lo, hi)
	h.compare("Query", err, nr)
	if err != nil {
		h.log("  -> %s", h.basis(err, nr))
		return
	}
	if len(pts) != len(nvpts) {
		h.t.Fatalf("[Query] 点数不一致: 生产=%d 朴素=%d", len(pts), len(nvpts))
	}
	for i := range pts {
		ns := canonicalNaiveStatus(nvpts[i].Status)
		wantOrder := h.n2id[nvpts[i].OrderID]
		if pts[i].OrderID != wantOrder || pts[i].Planned != nvpts[i].Planned || int(pts[i].Status) != ns {
			h.t.Fatalf("[Query] 第%d点不一致: 生产=(%d,%d) 朴素=(%d,%d)",
				i, pts[i].Planned, pts[i].Status, nvpts[i].Planned, ns)
		}
		// Actual 仅在已处理时比较。
		if ns != int(medsched.StatusPending) && ns != int(medsched.StatusMissed) &&
			ns != int(medsched.StatusVoid) && pts[i].Actual != nvpts[i].Actual {
			h.t.Fatalf("[Query] 第%d点实际时刻不一致: %d vs %d", i, pts[i].Actual, nvpts[i].Actual)
		}
	}
	h.log("  -> 接受 返回%d点", len(pts))
}

func runOneSequence(t *testing.T, seed uint64, logPath string) {
	const w = int64(10)
	sys, err := medsched.New(w)
	if err != nil {
		t.Fatal(err)
	}
	nv := NewNaive(w)
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()

	h := &harness{
		t: t, sys: sys, nv: nv, logf: lf, w: w,
		drugs:       []string{"D1", "D2", "D3", "D4"},
		pats:        []string{"P1", "P2", "P3"},
		id2n:        map[string]string{},
		n2id:        map[string]string{},
		lastPlanned: map[string]int64{},
		drugMins:    map[string]int64{},
	}
	r := &rng{state: seed | 1}
	h.log("=== 差分序列 seed=%d 日志 ===", seed)

	// 登记药品（最小间隔随机但保持较小，留出冲突空间）。
	cats := []string{"C1", "C2"}
	for _, d := range h.drugs {
		min := int64(5 + r.intn(30))
		cat := cats[r.intn(len(cats))]
		h.log("操作 RegisterDrug now=0 drug=%s cat=%s min=%d", d, cat, min)
		if err := sys.RegisterDrug(0, d, cat, min); err != nil {
			t.Fatalf("sys register: %v", err)
		}
		if nr := nv.RegisterDrug(0, d, cat, min); !nr.OK {
			t.Fatalf("nv register: %d", nr.Code)
		}
		h.drugMins[d] = min
	}
	// 随机登记少量过敏（药品/类别）。
	if r.intn(2) == 0 {
		_ = sys.AddAllergyDrug(0, "P2", "D1")
		_ = nv.AddAllergyDrug(0, "P2", "D1")
		h.log("操作 AddAllergyDrug P2->D1 -> 接受")
	}
	if r.intn(2) == 0 {
		_ = sys.AddAllergyCategory(0, "P3", "C2")
		_ = nv.AddAllergyCat(0, "P3", "C2")
		h.log("操作 AddAllergyCategory P3->C2 -> 接受")
	}

	const steps = 60
	now := int64(0)
	for i := 0; i < steps; i++ {
		// 合法操作流要求时钟单调不减；回退错误由专门单测覆盖。
		now = now + int64(r.intn(40))
		switch r.intn(10) {
		case 0, 1:
			h.openOrder(r, now)
		case 2, 3:
			h.administer(r, now)
		case 4:
			h.refuse(r, now)
		case 5:
			h.makeUp(r, now)
		case 6:
			h.stop(r, now)
		case 7:
			h.revise(r, now)
		case 8:
			h.prn(r, now)
		default:
			h.query(r, now)
		}
	}
	// 终态再做一次全区间查询比对；以双方已接受的时钟（sys.Now）为准，避免回退。
	h.queryAt(sys.Now())
}

func (h *harness) queryAt(now int64) {
	for _, pat := range h.pats {
		pts, err := h.sys.Query(now, pat, 0, horizon)
		if err != nil {
			h.t.Fatalf("final query sys: %v", err)
		}
		nvpts, nr := h.nv.Query(now, pat, 0, horizon)
		if !nr.OK {
			h.t.Fatalf("final query nv: %d", nr.Code)
		}
		if len(pts) != len(nvpts) {
			h.t.Fatalf("[终态查询 %s] 点数不一致: 生产=%d 朴素=%d", pat, len(pts), len(nvpts))
		}
		for i := range pts {
			if pts[i].OrderID != h.n2id[nvpts[i].OrderID] ||
				pts[i].Planned != nvpts[i].Planned ||
				int(pts[i].Status) != canonicalNaiveStatus(nvpts[i].Status) {
				for j := i - 2; j <= i+2 && j < len(pts); j++ {
					if j < 0 {
						continue
					}
					h.t.Logf("j=%d sys=%s/%d st=%d | nv=%s/%d st=%d", j,
						pts[j].OrderID, pts[j].Planned, pts[j].Status,
						nvpts[j].OrderID, nvpts[j].Planned, canonicalNaiveStatus(nvpts[j].Status))
				}
				h.t.Fatalf("[终态查询 %s] 第%d点不一致", pat, i)
			}
		}
	}
}

// TestRandomDifferential 跑不少于1500组随机操作序列，与朴素模型逐步对照。
func TestRandomDifferential(t *testing.T) {
	if testing.Verbose() {
		t.Logf("运行 1500 组差分序列，日志写入临时目录")
	}
	dir := t.TempDir()
	const sequences = 1500
	for s := 0; s < sequences; s++ {
		path := fmt.Sprintf("%s/seq_%04d.log", dir, s)
		runOneSequence(t, uint64(0x9E3779B97F4A7C15)+uint64(s)*2654435761, path)
	}
}
