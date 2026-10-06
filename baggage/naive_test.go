package baggage

// 本文件是按题目规则独立编写的朴素对照模型（递归拆段、顺序分配合并额度），
// 用于与正式实现做随机比对；每个用例打印输入、输出与判定依据。

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

type naiveInput struct {
	cfg      Config
	airports map[string]Airport
	carriers map[string]Carrier
	tiers    map[string]Tier
	booking  map[string]bool
	pnr      string
	pax      []PassengerBags
	itin     []Segment
	now      int64
}

type naiveResult struct {
	err         *Error
	sections    []Section
	extractions []string
	tags        []BagTag
	total       int64
}

// naiveSplit 递归地寻找第一个必须提取的中转站。
func naiveSplit(segs []Segment, airports map[string]Airport, cfg Config) [][2]int {
	for i := 1; i < len(segs); i++ {
		a := airports[segs[i].From]
		conn := segs[i].Depart - segs[i-1].Arrive
		if a.Customs || segs[i-1].PNR != segs[i].PNR || conn < cfg.MinConn || conn > cfg.MaxConn {
			out := [][2]int{{0, i}}
			for _, r := range naiveSplit(segs[i:], airports, cfg) {
				out = append(out, [2]int{r[0] + i, r[1] + i})
			}
			return out
		}
	}
	return [][2]int{{0, len(segs)}}
}

// naiveAllowance 从左向右扫描，记录第一个跨区域航段的承运人。
func naiveAllowance(segs []Segment, airports map[string]Airport) string {
	carrier := segs[0].Carrier
	cross := false
	for _, s := range segs {
		if !cross && airports[s.From].Region != airports[s.To].Region {
			carrier = s.Carrier
			cross = true
		}
	}
	return carrier
}

// naiveFee 独立结构的计费：计重制用“顺序分配合并池”而非汇总公式。
func naiveFee(segs []Segment, in naiveInput) (int64, *Error) {
	car := in.carriers[naiveAllowance(segs, in.airports)]
	seq := 0
	for _, p := range in.pax {
		for _, w := range p.Bags {
			seq++
			if w > car.AbsWeight {
				return 0, &Error{Code: ErrOverweight, BagSeq: seq, Msg: "naive: 超重拒收"}
			}
		}
	}
	if car.Policy == PiecePolicy {
		var fee int64
		for _, p := range in.pax {
			over := 0
			for _, w := range p.Bags {
				if w > car.PieceFreeWeight {
					over++
				}
			}
			extra := int64(len(p.Bags)) - car.FreePieces - in.tiers[p.Tier].ExtraPieces
			if extra < 0 {
				extra = 0
			}
			fee += extra*car.PieceFee + int64(over)*car.OverweightFee
		}
		return fee, nil
	}
	// 计重制：合并池按旅客顺序分配；会员额外重量先抵扣本人。
	var pool int64
	for range in.pax {
		pool += car.FreeTotalWeight
	}
	var excess int64
	for _, p := range in.pax {
		var w int64
		for _, b := range p.Bags {
			w += b
		}
		own := in.tiers[p.Tier].ExtraWeight
		if own > w {
			own = w
		}
		need := w - own
		use := need
		if use > pool {
			use = pool
		}
		pool -= use
		excess += need - use
	}
	return excess * car.UnitFee, nil
}

// naiveRun 按与正式实现相同的拒绝次序独立执行一次办理。
func naiveRun(in naiveInput) naiveResult {
	// 参数非法
	if in.pnr == "" || len(in.pax) == 0 || len(in.itin) == 0 {
		return naiveResult{err: &Error{Code: ErrInvalidParam, Msg: "naive: 参数非法"}}
	}
	seen := map[string]bool{}
	for _, p := range in.pax {
		if p.Passenger == "" || seen[p.Passenger] || len(p.Bags) == 0 {
			return naiveResult{err: &Error{Code: ErrInvalidParam, Msg: "naive: 旅客或件数非法"}}
		}
		seen[p.Passenger] = true
		for _, w := range p.Bags {
			if w < 0 {
				return naiveResult{err: &Error{Code: ErrInvalidParam, Msg: "naive: 重量为负"}}
			}
		}
		if p.Tier != "" {
			if _, ok := in.tiers[p.Tier]; !ok {
				return naiveResult{err: &Error{Code: ErrInvalidParam, Msg: "naive: 未知等级"}}
			}
		}
	}
	for i, sg := range in.itin {
		if _, ok := in.airports[sg.From]; !ok {
			return naiveResult{err: &Error{Code: ErrInvalidParam, Msg: "naive: 未知机场"}}
		}
		if _, ok := in.airports[sg.To]; !ok {
			return naiveResult{err: &Error{Code: ErrInvalidParam, Msg: "naive: 未知机场"}}
		}
		if _, ok := in.carriers[sg.Carrier]; !ok {
			return naiveResult{err: &Error{Code: ErrInvalidParam, Msg: "naive: 未知承运人"}}
		}
		if sg.Arrive <= sg.Depart {
			return naiveResult{err: &Error{Code: ErrInvalidParam, Msg: "naive: 航段时刻非法"}}
		}
		if i > 0 && (in.itin[i-1].To != sg.From || sg.Depart < in.itin[i-1].Arrive) {
			return naiveResult{err: &Error{Code: ErrInvalidParam, Msg: "naive: 航段不连贯"}}
		}
	}
	// 不存在
	if !in.booking[in.pnr] {
		return naiveResult{err: &Error{Code: ErrNotFound, Msg: "naive: 记录不存在"}}
	}
	for _, p := range in.pax {
		if !in.booking["pax:"+p.Passenger] {
			return naiveResult{err: &Error{Code: ErrNotFound, Msg: "naive: 旅客不存在"}}
		}
	}
	// 已截止
	if in.now >= in.itin[0].Depart-in.cfg.Cutoff {
		return naiveResult{err: &Error{Code: ErrCutoffPassed, Msg: "naive: 已截止"}}
	}
	// 拆段、计费
	var res naiveResult
	for _, r := range naiveSplit(in.itin, in.airports, in.cfg) {
		sec := Section{
			Start:   r[0],
			End:     r[1],
			From:    in.itin[r[0]].From,
			To:      in.itin[r[1]-1].To,
			Carrier: naiveAllowance(in.itin[r[0]:r[1]], in.airports),
		}
		fee, err := naiveFee(in.itin[r[0]:r[1]], in)
		if err != nil {
			return naiveResult{err: err}
		}
		sec.Fee = fee
		res.sections = append(res.sections, sec)
		res.total += fee
		if r[0] > 0 {
			res.extractions = append(res.extractions, in.itin[r[0]].From)
		}
	}
	seq := 0
	for _, p := range in.pax {
		for range p.Bags {
			seq++
			tag := BagTag{Passenger: p.Passenger, Seq: seq}
			for _, sec := range res.sections {
				tag.Dests = append(tag.Dests, sec.To)
			}
			res.tags = append(res.tags, tag)
		}
	}
	return res
}

// genCase 生成一组随机但确定的系统数据与办理请求。
func genCase(r *rand.Rand) naiveInput {
	in := naiveInput{
		airports: map[string]Airport{},
		carriers: map[string]Carrier{},
		tiers:    map[string]Tier{},
		booking:  map[string]bool{},
	}
	in.cfg = Config{
		MinConn: int64(30 + r.Intn(91)),
		Cutoff:  int64(r.Intn(121)),
	}
	in.cfg.MaxConn = in.cfg.MinConn + int64(r.Intn(181))

	regions := []string{"R0", "R1", "R2"}[:1+r.Intn(3)]
	nAir := 2 + r.Intn(5)
	codes := make([]string, nAir)
	for i := range codes {
		codes[i] = fmt.Sprintf("AP%d", i)
		in.airports[codes[i]] = Airport{
			Code:    codes[i],
			Region:  regions[r.Intn(len(regions))],
			Customs: r.Intn(4) == 0,
		}
	}
	nCar := 1 + r.Intn(3)
	cars := make([]string, nCar)
	for i := range cars {
		cars[i] = fmt.Sprintf("C%d", i)
		in.carriers[cars[i]] = Carrier{
			Code:            cars[i],
			Policy:          Policy(r.Intn(2)),
			FreePieces:      int64(r.Intn(4)),
			PieceFreeWeight: int64(100 + r.Intn(201)),
			AbsWeight:       int64(300 + r.Intn(151)),
			FreeTotalWeight: int64(200 + r.Intn(401)),
			PieceFee:        int64(r.Intn(5001)),
			OverweightFee:   int64(r.Intn(3001)),
			UnitFee:         int64(1 + r.Intn(100)),
		}
	}
	tierNames := []string{"", "T0", "T1"}
	for _, tn := range tierNames[1:] {
		in.tiers[tn] = Tier{Name: tn, ExtraPieces: int64(r.Intn(3)), ExtraWeight: int64(r.Intn(151))}
	}

	// 行程：随机游走保证连贯；小概率制造不连贯。
	in.pnr = "PNR1"
	nSeg := 1 + r.Intn(4)
	cur := codes[r.Intn(nAir)]
	depart := int64(1000)
	for i := 0; i < nSeg; i++ {
		next := codes[r.Intn(nAir)]
		for next == cur {
			next = codes[r.Intn(nAir)]
		}
		arrive := depart + 60 + int64(r.Intn(141))
		pnr := "PNR1"
		if r.Intn(2) == 0 {
			pnr = "PNR2"
		}
		in.itin = append(in.itin, Segment{
			From: cur, To: next, Carrier: cars[r.Intn(nCar)], PNR: pnr,
			Depart: depart, Arrive: arrive,
		})
		cur = next
		depart = arrive + int64(r.Intn(301))
	}
	if nSeg > 1 && r.Intn(20) == 0 { // 5% 不连贯
		in.itin[1].From = codes[r.Intn(nAir)]
	}

	// 旅客与行李。
	in.booking["PNR1"] = true
	names := []string{"p0", "p1", "p2"}
	for _, n := range names {
		in.booking["pax:"+n] = true
	}
	nPax := 1 + r.Intn(3)
	for i := 0; i < nPax; i++ {
		p := PassengerBags{Passenger: names[i], Tier: tierNames[r.Intn(len(tierNames))]}
		if r.Intn(30) == 0 { // 约 3% 旅客不存在
			p.Passenger = "ghost"
		}
		nBags := 1 + r.Intn(4)
		if r.Intn(30) == 0 { // 约 3% 件数为零
			nBags = 0
		}
		for j := 0; j < nBags; j++ {
			w := int64(r.Intn(421))
			if r.Intn(40) == 0 { // 约 2.5% 负重量
				w = -1
			}
			p.Bags = append(p.Bags, w)
		}
		in.pax = append(in.pax, p)
	}
	in.now = int64(r.Intn(1051))
	return in
}

// explain 生成判定依据文本（日志用）。
func explain(in naiveInput, res naiveResult) string {
	var b strings.Builder
	if res.err != nil {
		fmt.Fprintf(&b, "拒绝: code=%d seq=%d", res.err.Code, res.err.BagSeq)
		return b.String()
	}
	for i := 1; i < len(in.itin); i++ {
		a := in.airports[in.itin[i].From]
		conn := in.itin[i].Depart - in.itin[i-1].Arrive
		reason := "直挂"
		switch {
		case a.Customs:
			reason = "提取(清关)"
		case in.itin[i-1].PNR != in.itin[i].PNR:
			reason = "提取(跨记录)"
		case conn < in.cfg.MinConn || conn > in.cfg.MaxConn:
			reason = fmt.Sprintf("提取(停留%d分钟越界)", conn)
		}
		fmt.Fprintf(&b, " 站%s:%s", in.itin[i].From, reason)
	}
	for _, sec := range res.sections {
		segs := in.itin[sec.Start:sec.End]
		why := "首航段"
		for _, sg := range segs {
			if in.airports[sg.From].Region != in.airports[sg.To].Region {
				why = "首个跨区域航段"
				break
			}
		}
		fmt.Fprintf(&b, " 段[%d,%d)承运人=%s(%s)费=%d", sec.Start, sec.End, sec.Carrier, why, sec.Fee)
	}
	return b.String()
}

// realRun 用正式实现执行同一用例。
func realRun(t *testing.T, in naiveInput) naiveResult {
	t.Helper()
	s, err := NewSystem(in.cfg)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	for _, a := range in.airports {
		if e := s.AddAirport(a); e != nil {
			t.Fatalf("AddAirport: %v", e)
		}
	}
	for _, c := range in.carriers {
		if e := s.AddCarrier(c); e != nil {
			t.Fatalf("AddCarrier: %v", e)
		}
	}
	for _, tr := range in.tiers {
		if e := s.AddTier(tr); e != nil {
			t.Fatalf("AddTier: %v", e)
		}
	}
	var names []string
	for k := range in.booking {
		if strings.HasPrefix(k, "pax:") {
			names = append(names, strings.TrimPrefix(k, "pax:"))
		}
	}
	if e := s.AddBooking(in.pnr, names...); e != nil {
		t.Fatalf("AddBooking: %v", e)
	}
	rec, cerr := s.CheckIn(in.pnr, in.pax, in.itin, in.now)
	if cerr != nil {
		return naiveResult{err: cerr}
	}
	return naiveResult{
		sections:    rec.Sections,
		extractions: rec.Extractions,
		tags:        rec.Tags,
		total:       rec.TotalFee,
	}
}

func normErr(e *Error) *Error {
	if e == nil {
		return nil
	}
	return &Error{Code: e.Code, BagSeq: e.BagSeq}
}

// TestNaiveComparison 随机行程与行李组合下，正式实现与朴素模型结论一致；
// 并验证“每段托运费用等于单独办理该段”的可交换性。
func TestNaiveComparison(t *testing.T) {
	r := rand.New(rand.NewSource(20261006))
	const cases = 3000
	var ok, rejected int
	for i := 0; i < cases; i++ {
		in := genCase(r)
		want := naiveRun(in)
		got := realRun(t, in)
		t.Logf("用例%d 输入: cfg=%+v pax=%+v itin=%+v now=%d", i, in.cfg, in.pax, in.itin, in.now)
		t.Logf("用例%d 输出: 朴素=%+v 正式=%+v", i, want, got)
		t.Logf("用例%d 依据:%s", i, explain(in, want))
		if !reflect.DeepEqual(normErr(want.err), normErr(got.err)) {
			t.Fatalf("用例 %d 错误不一致: 朴素=%+v 正式=%+v\n输入: %+v", i, want.err, got.err, in)
		}
		if want.err != nil {
			rejected++
			continue
		}
		ok++
		if !reflect.DeepEqual(want.sections, got.sections) ||
			!reflect.DeepEqual(want.extractions, got.extractions) ||
			!reflect.DeepEqual(want.tags, got.tags) ||
			want.total != got.total {
			t.Fatalf("用例 %d 结果不一致:\n朴素=%+v\n正式=%+v\n输入: %+v", i, want, got, in)
		}
		// 可交换性：每段费用等于单独办理该段。
		for _, sec := range got.sections {
			sub := in
			sub.itin = append([]Segment(nil), in.itin[sec.Start:sec.End]...)
			sub.now = 0
			alone := realRun(t, sub)
			if alone.err != nil {
				t.Fatalf("用例 %d 段 %v 单独办理被拒: %v", i, sec, alone.err)
			}
			if len(alone.sections) != 1 || alone.sections[0].Fee != sec.Fee {
				t.Fatalf("用例 %d 段费不可复现: 段=%+v 单独=%+v", i, sec, alone.sections)
			}
		}
	}
	t.Logf("随机比对完成: 共 %d 例, 接受 %d, 拒绝 %d", cases, ok, rejected)
	if ok < cases/3 {
		t.Fatalf("接受用例过少(%d)，随机生成器覆盖不足", ok)
	}
}
