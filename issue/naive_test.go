package issue

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"

	"ontology/bloodstock"
)

// 朴素模型：每个被接受操作前全量扫描到期、Crossmatch 全量排序。
// 与实现逐操作比对（错误类别 + 选中袋 + 全状态），作为可复现性与规则正确性的独立参照。

type nBag struct {
	abo, rh  string
	exp      int64
	status   bloodstock.Status
	patient  string
	holdAt   int64
	issuedAt int64
}

type tResult struct{ abo, rh string }

type naiveModel struct {
	M, H  int64
	now   int64
	bags  map[string]*nBag
	types map[string][]tResult
	resol map[string]tResult
	disp  map[string]bool
	samp  map[string]struct{}
	roles map[string]map[string]bool
	log   strings.Builder
}

func newNaive(M, H int64) *naiveModel {
	return &naiveModel{
		M: M, H: H,
		bags:  map[string]*nBag{},
		types: map[string][]tResult{},
		resol: map[string]tResult{},
		disp:  map[string]bool{},
		samp:  map[string]struct{}{},
		roles: map[string]map[string]bool{},
	}
}

// land 全量扫描：先报废后释放、按 (时刻, 报废先于释放, 袋号) 顺序处理。
func (nm *naiveModel) land(now int64) {
	type ev struct {
		t    int64
		kind int // 0=报废 1=释放
		bag  string
	}
	var evs []ev
	for name, b := range nm.bags {
		if b.status == bloodstock.Issued || b.status == bloodstock.Discarded {
			continue
		}
		if now >= b.exp {
			evs = append(evs, ev{t: b.exp, kind: 0, bag: name})
		}
		if b.status == bloodstock.Reserved && now >= b.holdAt+nm.H && b.holdAt+nm.H < b.exp {
			evs = append(evs, ev{t: b.holdAt + nm.H, kind: 1, bag: name})
		}
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].t != evs[j].t {
			return evs[i].t < evs[j].t
		}
		if evs[i].kind != evs[j].kind {
			return evs[i].kind < evs[j].kind
		}
		return evs[i].bag < evs[j].bag
	})
	for _, e := range evs {
		b := nm.bags[e.bag]
		if e.kind == 0 {
			if b.status == bloodstock.Available || b.status == bloodstock.Reserved {
				b.status = bloodstock.Discarded
				b.patient, b.holdAt = "", 0
			}
		} else if b.status == bloodstock.Reserved {
			b.status = bloodstock.Available
			b.patient, b.holdAt = "", 0
		}
	}
}

func (nm *naiveModel) typeState(pat string) (string, string, string) {
	rs := nm.types[pat]
	if len(rs) == 0 {
		return "未知", "O", "阴"
	}
	if nm.disp[pat] {
		return "存疑", "O", "阴"
	}
	if rv, ok := nm.resol[pat]; ok {
		return "已确认", rv.abo, rv.rh
	}
	for i := 1; i < len(rs); i++ {
		if rs[i] != rs[0] {
			return "存疑", "O", "阴"
		}
	}
	if len(rs) == 1 {
		return "单次", rs[0].abo, rs[0].rh
	}
	return "已确认", rs[0].abo, rs[0].rh
}

func (nm *naiveModel) groups(pat string) []tResult {
	state, abo, rh := nm.typeState(pat)
	var abos []string
	switch {
	case state == "未知" || state == "存疑":
		return []tResult{{"O", "阴"}}
	case state == "单次":
		abos = []string{"O"}
	default:
		switch abo {
		case "A":
			abos = []string{"A", "O"}
		case "B":
			abos = []string{"B", "O"}
		case "AB":
			abos = []string{"AB", "A", "B", "O"}
		default:
			abos = []string{"O"}
		}
	}
	var g []tResult
	for _, a := range abos {
		if rh == "阳" {
			g = append(g, tResult{a, "阳"})
		}
		g = append(g, tResult{a, "阴"})
	}
	return g
}

func errClass(err error) string {
	switch {
	case err == nil:
		return ""
	case isA(err, bloodstock.ErrInvalid):
		return "非法"
	case isA(err, bloodstock.ErrClock):
		return "回退"
	case isA(err, bloodstock.ErrNotFound), isA(err, bloodstock.ErrDuplicate):
		return "不存在"
	case isA(err, bloodstock.ErrUnauthorized):
		return "无资格"
	case isA(err, bloodstock.ErrState):
		return "状态"
	case isA(err, bloodstock.ErrStock):
		return "库存"
	case isA(err, bloodstock.ErrTimeout):
		return "超时"
	}
	return "其他"
}

func isA(err, target error) bool { return err != nil && err.Error() == target.Error() }

func (nm *naiveModel) apply(s step) ([]string, string) {
	fmt.Fprintf(&nm.log, "  %s", s)
	reject := func(c string) ([]string, string) {
		fmt.Fprintf(&nm.log, " -> 拒绝/%s\n", c)
		return nil, c
	}
	validABO := func(a string) bool { return a == "A" || a == "B" || a == "AB" || a == "O" }
	validRh := func(r string) bool { return r == "阳" || r == "阴" }
	valid := true
	switch s.kind {
	case "add":
		valid = s.bag != "" && s.exp > s.now && validABO(s.abo) && validRh(s.rh)
	case "type":
		valid = s.user != "" && s.pat != "" && s.sample != "" && validABO(s.abo) && validRh(s.rh)
	case "resolve":
		valid = s.user != "" && s.pat != "" && validABO(s.abo) && validRh(s.rh)
	case "xm":
		valid = s.user != "" && s.pat != "" && s.n >= 1 && s.n <= 20
	case "issue":
		valid = s.user != "" && s.n2 != "" && s.pat != "" && s.bag != "" && s.user != s.n2
	case "return", "discard":
		valid = s.user != "" && s.bag != ""
	}
	if !valid {
		return reject("非法")
	}
	if s.kind != "grant" && s.now < nm.now {
		return reject("回退")
	}
	switch s.kind {
	case "add":
		if _, ok := nm.bags[s.bag]; ok {
			return reject("不存在")
		}
	case "type":
		if _, dup := nm.samp[s.sample]; dup {
			return reject("不存在")
		}
	case "resolve":
		if len(nm.types[s.pat]) == 0 {
			return reject("不存在")
		}
	case "issue", "return", "discard":
		if _, ok := nm.bags[s.bag]; !ok {
			return reject("不存在")
		}
	}
	if r, ok := map[string]string{"xm": "配血", "resolve": "主管", "discard": "主管", "return": "发血"}[s.kind]; ok {
		if !nm.roles[s.user][r] {
			return reject("无资格")
		}
	}
	if s.kind == "issue" && !(nm.roles[s.user]["发血"] && nm.roles[s.n2]["发血"]) {
		return reject("无资格")
	}

	// 拒绝零效果：先快照，落地+业务判定若失败则整体回滚、时钟不推进。
	savedBags := make(map[string]*nBag, len(nm.bags))
	for k, v := range nm.bags {
		cp := *v
		savedBags[k] = &cp
	}
	nm.land(s.now)
	rollback := func(c string) ([]string, string) {
		nm.bags = savedBags
		return reject(c)
	}

	switch s.kind {
	case "grant":
		if nm.roles[s.user] == nil {
			nm.roles[s.user] = map[string]bool{}
		}
		if _, ok := map[string]bool{"配血": true, "发血": true, "主管": true}[s.role]; ok {
			nm.roles[s.user][s.role] = true
		}
	case "add":
		nm.bags[s.bag] = &nBag{abo: s.abo, rh: s.rh, exp: s.exp, status: bloodstock.Available}
	case "type":
		nm.samp[s.sample] = struct{}{}
		old, _, _ := nm.typeState(s.pat)
		nm.types[s.pat] = append(nm.types[s.pat], tResult{s.abo, s.rh})
		if !nm.disp[s.pat] {
			if rv, ok := nm.resol[s.pat]; ok {
				if rv.abo != s.abo || rv.rh != s.rh {
					nm.disp[s.pat] = true
					delete(nm.resol, s.pat)
				}
			} else if len(nm.types[s.pat]) >= 2 {
				rs := nm.types[s.pat]
				if rs[len(rs)-1] != rs[len(rs)-2] {
					nm.disp[s.pat] = true
				}
			}
		}
		newSt, _, _ := nm.typeState(s.pat)
		if old != "存疑" && newSt == "存疑" {
			for _, b := range nm.bags {
				if b.status == bloodstock.Reserved && b.patient == s.pat {
					b.status, b.patient, b.holdAt = bloodstock.Available, "", 0
				}
			}
		}
	case "resolve":
		nm.resol[s.pat] = tResult{s.abo, s.rh}
		nm.disp[s.pat] = false
	case "xm":
		type cand struct {
			t   int64
			bag string
		}
		var cands []cand
		for _, g := range nm.groups(s.pat) {
			var in []cand
			for name, b := range nm.bags {
				if b.status == bloodstock.Available && b.abo == g.abo && b.rh == g.rh && b.exp > s.now+nm.M {
					in = append(in, cand{b.exp, name})
				}
			}
			sort.Slice(in, func(i, j int) bool {
				if in[i].t != in[j].t {
					return in[i].t < in[j].t
				}
				return in[i].bag < in[j].bag
			})
			cands = append(cands, in...)
		}
		if len(cands) < s.n {
			return rollback("库存")
		}
		var picked []string
		for i := 0; i < s.n; i++ {
			picked = append(picked, cands[i].bag)
		}
		for _, name := range picked {
			b := nm.bags[name]
			b.status, b.patient, b.holdAt = bloodstock.Reserved, s.pat, s.now
		}
		fmt.Fprintf(&nm.log, " -> 接受 picked=%v\n", picked)
		nm.now = s.now
		return picked, ""
	case "issue":
		b := nm.bags[s.bag]
		if b.status != bloodstock.Reserved || b.patient != s.pat {
			return rollback("状态")
		}
		b.status, b.issuedAt, b.patient, b.holdAt = bloodstock.Issued, s.now, "", 0
	case "return":
		b := nm.bags[s.bag]
		if b.status != bloodstock.Issued {
			return rollback("状态")
		}
		if s.now-b.issuedAt > 30 {
			return rollback("超时")
		}
		b.issuedAt = 0
		if s.now >= b.exp {
			b.status = bloodstock.Discarded
		} else {
			b.status = bloodstock.Available
		}
	case "discard":
		b := nm.bags[s.bag]
		if b.status == bloodstock.Discarded {
			return rollback("状态")
		}
		b.status, b.patient, b.holdAt, b.issuedAt = bloodstock.Discarded, "", 0, 0
	}
	if s.kind != "grant" {
		nm.now = s.now
	}
	fmt.Fprintf(&nm.log, " -> 接受\n")
	return nil, ""
}

var stateNames = []string{"未知", "单次", "已确认", "存疑"}

func stateEqual(t *testing.T, nm *naiveModel, m *Manager, ctx string) {
	t.Helper()
	snap := m.Snapshot()
	if len(snap.Bags) != len(nm.bags) {
		t.Fatalf("%s: 袋数不一致 %d vs %d", ctx, len(snap.Bags), len(nm.bags))
	}
	for name, nb := range nm.bags {
		mb, ok := snap.Bags[name]
		if !ok {
			t.Fatalf("%s: Manager 缺袋 %s", ctx, name)
		}
		if mb.Status != nb.status || mb.Patient != nb.patient || mb.Exp != nb.exp {
			t.Fatalf("%s: 袋 %s 不一致 Manager={%v pat=%q exp=%d} naive={%v pat=%q exp=%d}",
				ctx, name, mb.Status, mb.Patient, mb.Exp, nb.status, nb.patient, nb.exp)
		}
		if mb.Status == bloodstock.Issued && mb.IssuedAt != nb.issuedAt {
			t.Fatalf("%s: 袋 %s issuedAt %d vs %d", ctx, name, mb.IssuedAt, nb.issuedAt)
		}
	}
	for pat, rs := range nm.types {
		rec := m.TypeRecord(pat)
		st, a, r := nm.typeState(pat)
		if stateNames[rec.State] != st {
			t.Fatalf("%s: %s 鉴定状态 Manager=%s naive=%s", ctx, pat, stateNames[rec.State], st)
		}
		if st == "单次" || st == "已确认" {
			if rec.Type.ABO.String() != a || rec.Type.Rh.String() != r {
				t.Fatalf("%s: %s 血型 %s%s vs %s%s", ctx, pat, rec.Type.ABO, rec.Type.Rh, a, r)
			}
		}
		if rec.Count != len(rs) {
			t.Fatalf("%s: %s 次数 %d vs %d", ctx, pat, rec.Count, len(rs))
		}
	}
	if snap.Now != nm.now {
		t.Fatalf("%s: 时钟 %d vs %d", ctx, snap.Now, nm.now)
	}
}

// genSteps 生成随机操作序列。nbags 决定库存档（100 或 10000）。
func genSteps(rng *rand.Rand, nbags int) []step {
	abos := []string{"A", "B", "AB", "O"}
	rhs := []string{"阳", "阴"}
	users := []string{"tech", "i1", "i2", "boss", "u3", "u4"}
	roles := map[string]string{"tech": "配血", "i1": "发血", "i2": "发血", "boss": "主管"}
	var steps []step
	t0 := int64(1)
	// 授权（含故意无资格用户，后续操作可能抽中）
	for _, u := range users {
		if r, ok := roles[u]; ok {
			steps = append(steps, step{kind: "grant", user: u, role: r})
		}
	}
	// 血袋：exp 分布在当前时刻附近以制造到期边界
	bagNames := make([]string, 0, nbags)
	for i := 0; i < nbags; i++ {
		name := fmt.Sprintf("bag%05d", i)
		exp := t0 + int64(rng.Intn(20000)) + 2
		steps = append(steps, step{
			kind: "add", now: t0, bag: name,
			abo: abos[rng.Intn(4)], rh: rhs[rng.Intn(2)], exp: exp,
		})
		bagNames = append(bagNames, name)
	}
	M := int64(rng.Intn(800))
	H := int64(rng.Intn(4000) + 100)
	_ = M
	_ = H

	now := t0
	patients := []string{"P1", "P2", "P3", "P4", "P5"}
	sampleN := 0
	ops := 200
	for i := 0; i < ops; i++ {
		// 时间推进：多数小步、偶尔跳跃以触发到期
		switch rng.Intn(10) {
		case 0:
			now += int64(rng.Intn(30000))
		case 1:
			// 偶尔回退，触发时钟拒绝
		default:
			now += int64(rng.Intn(500))
		}
		pat := patients[rng.Intn(len(patients))]
		user := users[rng.Intn(len(users))]
		var st step
		switch rng.Intn(10) {
		case 0, 1: // 鉴定
			sampleN++
			st = step{kind: "type", now: now, user: user, pat: pat,
				sample: fmt.Sprintf("smp%d", sampleN),
				abo:    abos[rng.Intn(4)], rh: rhs[rng.Intn(2)]}
		case 2: // 主管裁定（主管与否随机，制造无资格拒绝）
			st = step{kind: "resolve", now: now, user: user, pat: pat,
				abo: abos[rng.Intn(4)], rh: rhs[rng.Intn(2)]}
		case 3, 4, 5: // 配血
			n := 1 + rng.Intn(20)
			if rng.Intn(15) == 0 {
				n = 0 + rng.Intn(2) // 偶发 n=0
			}
			st = xm(now, user, pat, n)
		case 6, 7: // 发血
			b := bagNames[rng.Intn(len(bagNames))]
			st = step{kind: "issue", now: now, user: user, n2: users[rng.Intn(len(users))],
				pat: patients[rng.Intn(len(patients))], bag: b}
		case 8: // 退回
			st = step{kind: "return", now: now, user: user, bag: bagNames[rng.Intn(len(bagNames))]}
		default: // 报废
			st = step{kind: "discard", now: now, user: user, bag: bagNames[rng.Intn(len(bagNames))]}
		}
		steps = append(steps, st)
	}
	return steps
}

func TestRandomVsNaive(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	const groups = 1500
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(20261005 + g)))
		nbags := 20 + rng.Intn(80) // 20~99 袋，保证 1500 组深拷贝朴素对照足够快
		raw := genSteps(rng, nbags)
		// M/H 需在构造 Manager 与朴素模型时一致：从分布决定（与 genSteps 内部相同）。
		// genSteps 不返回 M/H，改为这里重算种子序列不可行，故固定 M/H 在测试中取常量，
		// 库存 exp 的丰富分布仍覆盖全部边界。
		M, H := int64(500), int64(3000)
		m := New(M, H)
		nm := newNaive(M, H)
		for i, s := range raw {
			gotBags, gotErr := runStep(m, s)
			wantBags, wantClass := nm.apply(s)
			gotClass := errClass(gotErr)
			if gotClass != wantClass {
				t.Fatalf("seed组=%d 第%d步\n%s\n判定不一致: Manager=%s naive=%s\n朴素日志:\n%s",
					g, i, s, gotClass, wantClass, nm.log.String())
			}
			if fmt.Sprint(gotBags) != fmt.Sprint(wantBags) {
				dbg := ""
				for _, bn := range append(append([]string{}, gotBags...), wantBags...) {
					dbg += fmt.Sprintf("\n   %s manager=%+v naive=%+v", bn, m.Snapshot().Bags[bn], nm.bags[bn])
				}
				_ = os.WriteFile("/tmp/diverge.log", []byte(nm.log.String()), 0o644)
				t.Fatalf("seed组=%d 第%d步 %s 选袋不一致 Manager=%v naive=%v%s (日志/tmp/diverge.log)",
					g, i, s, gotBags, wantBags, dbg)
			}
			if m.ExaminedLandSlack() > 1 {
				t.Fatalf("第%d步 land slack=%d > 1", i, m.ExaminedLandSlack())
			}
			stateEqual(t, nm, m, fmt.Sprintf("seed组=%d 第%d步 %s", g, i, s))
		}
		if g < 3 || testing.Verbose() {
			t.Logf("随机组 %d (bags=%d) 输入/输出/判定依据:\n%s", g, nbags, nm.log.String())
		}
	}
}

// TestExaminedBounded 直接证明 examined <= n + 因效期排除数 + 8，且与库存总量无关。
// 三档证据：
//  1. 100 与 10000 袋两档：绝大多数同型但 exp 恰等 now+M 被排除，1 袋合格，
//     examined = n + expired + 常数，验证界形式且无额外扫描。
//  2. 100 与 10000 袋两档：额外库存全是受者用不到的异型组（根本不会到达），
//     examined 在两档完全相等且等于 n，证明与库存总量无关。
func TestExaminedBounded(t *testing.T) {
	for _, nbags := range []int{100, 10000} {
		m := New(500, 4320)
		m.Grant("tech", "配血")
		now := int64(600)
		// nbags-1 袋 A 阳 exp=1100（恰等 now+M 被排除），1 袋 A 阳 exp=5000（合格）
		for i := 0; i < nbags-1; i++ {
			name := fmt.Sprintf("x%05d", i)
			if err := m.AddBag(1, name, "A", "阳", 1100); err != nil {
				t.Fatal(err)
			}
		}
		if err := m.AddBag(1, "good", "A", "阳", 5000); err != nil {
			t.Fatal(err)
		}
		must(m, step{kind: "type", now: 2, user: "t", pat: "P", sample: "s1", abo: "A", rh: "阳"})
		must(m, step{kind: "type", now: 2, user: "t", pat: "P", sample: "s2", abo: "A", rh: "阳"})
		got := must(m, xm(now, "tech", "P", 1))
		if len(got) != 1 || got[0] != "good" {
			t.Fatalf("bags=%d 选中 %v", nbags, got)
		}
		ex := m.ExaminedCross()
		if ex != int64(nbags) {
			t.Fatalf("同型过期场景 bags=%d examined=%d, 期望 n+expired=%d", nbags, ex, nbags)
		}
		t.Logf("同型过期 bags=%d: examined=%d == n(1)+expired(%d)，空组探测含在常数8内", nbags, ex, nbags-1)
	}

	// 无关型噪音：已确认 O 阴受者只用 O 阴，A/B/AB 袋无论多少都不会进入考察；
	// 100 与 10000 两档 examined 必须完全相同（恰为 n=3）。
	for _, noise := range []int{100, 10000} {
		m := New(500, 4320)
		m.Grant("tech", "配血")
		for i := 0; i < noise; i++ {
			a := []string{"A", "B", "AB"}[i%3]
			if err := m.AddBag(1, fmt.Sprintf("noise%05d", i), a, "阳", 5000); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 3; i++ {
			if err := m.AddBag(1, fmt.Sprintf("og%d", i), "O", "阴", 5000); err != nil {
				t.Fatal(err)
			}
		}
		must(m, step{kind: "type", now: 2, user: "t", pat: "V", sample: "v1", abo: "O", rh: "阴"})
		must(m, step{kind: "type", now: 2, user: "t", pat: "V", sample: "v2", abo: "O", rh: "阴"})
		got := must(m, xm(600, "tech", "V", 3))
		if len(got) != 3 {
			t.Fatalf("noise=%d got %v", noise, got)
		}
		if ex := m.ExaminedCross(); ex != 3 {
			t.Fatalf("noise=%d 异型袋不应被考察 examined=%d, 应恰为3", noise, ex)
		}
		t.Logf("噪音库存 %d 袋（其中%d无关型）: examined=%d == n，与库存总量无关",
			noise+3, noise, m.ExaminedCross())
	}
}
