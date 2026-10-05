package throttle

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/classify"
	"ontology/ocstate"
)

// 本文件把控制器与一份按规格逐条规则独立写成的朴素模拟对照：
// 1500 组随机操作序列，逐操作比较输出、错误类别与全部服务器状态。

// ---- 错误类别 ----

const (
	catOK = iota
	catInvalid
	catClock
	catNotFound
	catStale
	catNoInFlight
	catExists
)

func errCategory(err error) int {
	switch {
	case err == nil:
		return catOK
	case errors.Is(err, ErrInvalidParam), errors.Is(err, classify.ErrInvalidParam):
		return catInvalid
	case errors.Is(err, ErrClockRegression):
		return catClock
	case errors.Is(err, ErrServerNotFound):
		return catNotFound
	case errors.Is(err, ocstate.ErrStaleReport):
		return catStale
	case errors.Is(err, ocstate.ErrNoInFlight):
		return catNoInFlight
	case errors.Is(err, ErrAlreadyExists):
		return catExists
	}
	return -1
}

var catNames = map[int]string{
	catOK: "OK", catInvalid: "参数非法", catClock: "时钟回退",
	catNotFound: "服务器不存在", catStale: "通告过期",
	catNoInFlight: "无在途", catExists: "已存在",
}

// ---- 朴素模拟（独立实现，直接按规格文字展开） ----

type mServer struct {
	p, d, expiry                 int64
	inFlight, maxSeq             int64
	arrivals, forwarded, dropped int64
}

type mModel struct {
	thetaLow, thetaNorm, dCap, w int64
	servers                      map[int64]*mServer
	maxNow                       int64
}

func mClassify(msg classify.Message) (classify.Class, int) {
	valid := map[string]bool{
		"INVITE": true, "ACK": true, "BYE": true, "CANCEL": true,
		"REGISTER": true, "OPTIONS": true, "SUBSCRIBE": true, "NOTIFY": true,
		"MESSAGE": true, "PRACK": true, "UPDATE": true, "REFER": true,
		"PUBLISH": true, "INFO": true,
	}
	if !valid[msg.Method] {
		return 0, catInvalid
	}
	switch msg.Priority {
	case classify.PriorityEmergency, classify.PriorityNormal, classify.PriorityNonUrgent:
	default:
		return 0, catInvalid
	}
	if msg.InDialog || msg.Priority == classify.PriorityEmergency ||
		msg.Method == "ACK" || msg.Method == "BYE" || msg.Method == "CANCEL" || msg.Method == "PRACK" {
		return classify.ClassExempt, catOK
	}
	if msg.Priority == classify.PriorityNonUrgent ||
		msg.Method == "OPTIONS" || msg.Method == "SUBSCRIBE" || msg.Method == "MESSAGE" {
		return classify.ClassLow, catOK
	}
	return classify.ClassNormal, catOK
}

func (m *mModel) addServer(id int64) int {
	if id < 1 || id > 1_000_000 {
		return catInvalid
	}
	if _, ok := m.servers[id]; ok {
		return catExists
	}
	m.servers[id] = &mServer{}
	return catOK
}

func (m *mModel) report(server, seq, percent, validity, now int64) int {
	if percent < 0 || percent > 100 || validity < 0 || validity > 1_000_000_000 ||
		now < 0 || now > 1_000_000_000_000 {
		return catInvalid
	}
	if now < m.maxNow {
		return catClock
	}
	s, ok := m.servers[server]
	if !ok {
		return catNotFound
	}
	if seq <= s.maxSeq {
		return catStale
	}
	s.maxSeq = seq
	if validity == 0 {
		s.p, s.d, s.expiry = 0, 0, 0
	} else {
		if now >= s.expiry {
			s.d = 0
		}
		s.p = percent
		s.expiry = now + validity
	}
	m.maxNow = now
	return catOK
}

func (m *mModel) done(server, now int64) int {
	if now < 0 || now > 1_000_000_000_000 {
		return catInvalid
	}
	if now < m.maxNow {
		return catClock
	}
	s, ok := m.servers[server]
	if !ok {
		return catNotFound
	}
	if s.inFlight == 0 {
		return catNoInFlight
	}
	s.inFlight--
	m.maxNow = now
	return catOK
}

func (m *mModel) route(msg classify.Message, cands []int64, now int64) (Outcome, int64, []Step, int) {
	cls, cat := mClassify(msg)
	if cat != catOK {
		return 0, 0, nil, cat
	}
	if now < 0 || now > 1_000_000_000_000 || len(cands) < 1 || len(cands) > 8 {
		return 0, 0, nil, catInvalid
	}
	seen := map[int64]bool{}
	for _, id := range cands {
		if seen[id] {
			return 0, 0, nil, catInvalid
		}
		seen[id] = true
	}
	if now < m.maxNow {
		return 0, 0, nil, catClock
	}
	for _, id := range cands {
		if _, ok := m.servers[id]; !ok {
			return 0, 0, nil, catNotFound
		}
	}
	var trace []Step
	dropped := false
	for _, id := range cands {
		s := m.servers[id]
		if s.inFlight >= m.w {
			trace = append(trace, Step{Server: id, Outcome: StepBusy})
			continue
		}
		s.arrivals++
		effP := s.p
		if now >= s.expiry {
			s.d, effP = 0, 0
		}
		s.d += effP
		if s.d > m.dCap {
			s.d = m.dCap
		}
		shed := cls == classify.ClassLow && s.d >= m.thetaLow ||
			cls == classify.ClassNormal && s.d >= m.thetaNorm
		if shed {
			s.d -= 100
			s.dropped++
			dropped = true
			trace = append(trace, Step{Server: id, Outcome: StepDropped})
			continue
		}
		s.inFlight++
		s.forwarded++
		trace = append(trace, Step{Server: id, Outcome: StepForwarded})
		m.maxNow = now
		return Forwarded, id, trace, catOK
	}
	m.maxNow = now
	if dropped {
		return RejectedOverload, 0, trace, catOK
	}
	return RejectedBusy, 0, trace, catOK
}

func (m *mModel) effD(id, now int64) int64 {
	s := m.servers[id]
	if now >= s.expiry {
		return 0
	}
	return s.d
}

func (m *mModel) effP(id, now int64) int64 {
	s := m.servers[id]
	if now >= s.expiry {
		return 0
	}
	return s.p
}

// ---- 随机操作序列对照 ----

var randMethods = []string{
	"INVITE", "ACK", "BYE", "CANCEL", "REGISTER", "OPTIONS", "SUBSCRIBE",
	"NOTIFY", "MESSAGE", "PRACK", "UPDATE", "REFER", "PUBLISH", "INFO",
	"FOO", "", // 非法方法
}

func randMsg(r *rand.Rand) classify.Message {
	return classify.Message{
		Method:   randMethods[r.Intn(len(randMethods))],
		InDialog: r.Intn(2) == 0,
		Priority: classify.Priority(r.Intn(4)), // 3 为非法优先级
	}
}

func TestRandomSequencesAgainstModel(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq) * 7919))
		thetaLow := 100 + r.Int63n(400)
		thetaNorm := thetaLow + r.Int63n(600)
		dCap := thetaNorm + r.Int63n(2000)
		window := 1 + r.Int63n(4)

		c, err := New(thetaLow, thetaNorm, dCap, window)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		model := &mModel{
			thetaLow: thetaLow, thetaNorm: thetaNorm, dCap: dCap, w: window,
			servers: map[int64]*mServer{},
		}

		now := int64(0)
		checkState := func(opDesc string) {
			t.Helper()
			if c.maxNow != model.maxNow {
				t.Fatalf("seq=%d %s: maxNow=%d, 模拟=%d", seq, opDesc, c.maxNow, model.maxNow)
			}
			if len(c.servers) != len(model.servers) {
				t.Fatalf("seq=%d %s: 服务器数=%d, 模拟=%d", seq, opDesc, len(c.servers), len(model.servers))
			}
			for id, ms := range model.servers {
				cs := c.servers[id]
				if cs == nil {
					t.Fatalf("seq=%d %s: 服务器 %d 缺失", seq, opDesc, id)
				}
				if cs.EffectiveD(now) != model.effD(id, now) || cs.EffectiveP(now) != model.effP(id, now) {
					t.Fatalf("seq=%d %s: 服务器 %d D=%d/%d P=%d/%d", seq, opDesc, id,
						cs.EffectiveD(now), model.effD(id, now), cs.EffectiveP(now), model.effP(id, now))
				}
				if cs.InFlight != ms.inFlight || cs.Arrivals != ms.arrivals ||
					cs.Forwarded != ms.forwarded || cs.Dropped != ms.dropped {
					t.Fatalf("seq=%d %s: 服务器 %d 计数不一致", seq, opDesc, id)
				}
			}
		}

		// 初始登记 1..8 台服务器。
		for i, n := 0, 1+r.Intn(8); i < n; i++ {
			id := 1 + r.Int63n(20)
			got := errCategory(c.AddServer(id))
			want := model.addServer(id)
			if got != want {
				t.Fatalf("seq=%d AddServer(%d): %s, 模拟=%s", seq, id, catNames[got], catNames[want])
			}
		}
		checkState("init")

		for op := 0; op < 60; op++ {
			// 时钟：大概率前进，小概率回退或越界。
			switch r.Intn(20) {
			case 0:
				now -= r.Int63n(100)
			case 1:
				now += 1_000_000_000_000 // 可能越界
			default:
				now += r.Int63n(50)
			}
			kind := r.Intn(100)
			switch {
			case kind < 45: // Route
				msg := randMsg(r)
				k := 1 + r.Intn(8)
				cands := make([]int64, k)
				for i := range cands {
					cands[i] = 1 + r.Int63n(25)
				}
				if k >= 2 && r.Intn(10) == 0 {
					cands[1] = cands[0] // 制造重复候选
				}
				res, err := c.Route(msg, cands, now)
				gotCat := errCategory(err)
				wOut, wTarget, wTrace, wCat := model.route(msg, cands, now)
				if gotCat != wCat {
					t.Fatalf("seq=%d op=%d Route: 错误类别 %s, 模拟=%s", seq, op, catNames[gotCat], catNames[wCat])
				}
				if gotCat == catOK {
					if res.Outcome != wOut || res.Target != wTarget || !reflect.DeepEqual(res.Trace, wTrace) {
						t.Fatalf("seq=%d op=%d Route: (%s,%d,%v), 模拟=(%s,%d,%v)",
							seq, op, res.Outcome, res.Target, res.Trace, wOut, wTarget, wTrace)
					}
					t.Logf("seq=%d op=%d 输入=Route(%+v,%v,now=%d) 输出=(%s,%d,%v) 判定依据=逐台Busy/欠额减载/转发",
						seq, op, msg, cands, now, res.Outcome, res.Target, res.Trace)
				} else {
					t.Logf("seq=%d op=%d 输入=Route(%+v,%v,now=%d) 输出=错误:%s 判定依据=拒绝次序",
						seq, op, msg, cands, now, catNames[gotCat])
				}
			case kind < 70: // Report
				server := 1 + r.Int63n(25)
				var maxSeq int64
				if ms, ok := model.servers[server]; ok {
					maxSeq = ms.maxSeq
				}
				rseq := maxSeq + r.Int63n(4) - 1 // 可能相等或更小
				percent := r.Int63n(106) - 3     // 可能越界
				validity := int64(0)
				if r.Intn(5) != 0 {
					validity = r.Int63n(1_500_000_000) // 可能超过 1e9
				}
				got := errCategory(c.Report(server, rseq, percent, validity, now))
				want := model.report(server, rseq, percent, validity, now)
				if got != want {
					t.Fatalf("seq=%d op=%d Report(%d,%d,%d,%d,now=%d): %s, 模拟=%s",
						seq, op, server, rseq, percent, validity, now, catNames[got], catNames[want])
				}
				t.Logf("seq=%d op=%d 输入=Report(s=%d,seq=%d,p=%d,v=%d,now=%d) 输出=%s 判定依据=校验次序",
					seq, op, server, rseq, percent, validity, now, catNames[got])
			case kind < 85: // Done
				server := 1 + r.Int63n(25)
				got := errCategory(c.Done(server, now))
				want := model.done(server, now)
				if got != want {
					t.Fatalf("seq=%d op=%d Done(%d,now=%d): %s, 模拟=%s",
						seq, op, server, now, catNames[got], catNames[want])
				}
			default: // AddServer
				id := r.Int63n(26)
				got := errCategory(c.AddServer(id))
				want := model.addServer(id)
				if got != want {
					t.Fatalf("seq=%d op=%d AddServer(%d): %s, 模拟=%s",
						seq, op, id, catNames[got], catNames[want])
				}
			}
			checkState("op")
		}
	}
}
