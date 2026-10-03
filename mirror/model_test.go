package mirror

// 本文件包含一个按规格逐条直译的朴素模拟（model），
// 以及 2000 组随机操作序列下与真实实现的对照测试。
// model 不引用 sampler/diff 包的任何逻辑，全部规则在此独立重写。

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"ontology/diff"
)

type mResp struct {
	status int
	fields map[string]string
}

type mMirror struct {
	primary    *mResp
	shadow     *mResp
	done       bool
	errored    bool
	classified bool
}

// model 是规格的逐步直译实现，用于对照真实实现。
type model struct {
	k, cm, bm, e, p int64
	au              bool
	sh, ig          map[string]bool

	c           int64
	s           int64
	pausedUntil int64
	maxNow      int64
	nextID      int64
	inFlight    int64
	byReq       map[string]int64
	mirrors     map[int64]*mMirror

	calls      int64
	skUnsafe   int64
	skLarge    int64
	skPaused   int64
	skNotSamp  int64
	skBusy     int64
	dispatched int64
	completed  int64
	compOK     int64
	merr       int64
	same       int64
	compat     int64
	breaking   int64
}

func newModel(cfg Config) *model {
	sh := map[string]bool{}
	for name, v := range cfg.SensitiveHeader {
		sh[name] = v
	}
	ig := map[string]bool{}
	for name, v := range cfg.IgnoreField {
		ig[name] = v
	}
	return &model{
		k: cfg.K, cm: cfg.Cm, bm: cfg.Bm, e: cfg.E, p: cfg.P,
		au:      cfg.AllowUnsafe,
		sh:      sh,
		ig:      ig,
		nextID:  1,
		byReq:   map[string]int64{},
		mirrors: map[int64]*mMirror{},
	}
}

var mKnownMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true,
	"PUT": true, "PATCH": true, "DELETE": true,
}

// mirror 直译 Mirror 规则：参数非法 > 时间非法 > 时钟回退 > 重复，再按序跳过判定。
func (m *model) mirror(reqID, method string, bodyLen int64, headers map[string]string, now int64) (Result, ErrKind) {
	if reqID == "" {
		return Result{}, ErrInvalidArgument
	}
	if !mKnownMethods[method] {
		return Result{}, ErrInvalidArgument
	}
	if bodyLen < 0 || bodyLen > 1_000_000_000_000 {
		return Result{}, ErrInvalidArgument
	}
	seen := map[string]bool{}
	for name := range headers {
		if name == "" {
			return Result{}, ErrInvalidArgument
		}
		lower := strings.ToLower(name)
		if seen[lower] {
			return Result{}, ErrInvalidArgument
		}
		seen[lower] = true
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return Result{}, ErrInvalidTime
	}
	if now < m.maxNow {
		return Result{}, ErrClockRegression
	}
	if _, ok := m.byReq[reqID]; ok {
		return Result{}, ErrDuplicate
	}

	m.maxNow = now
	m.calls++

	if !m.au && method != "GET" && method != "HEAD" {
		m.skUnsafe++
		return Result{Reason: SkipUnsafe}, errNone
	}
	if bodyLen > m.bm {
		m.skLarge++
		return Result{Reason: SkipBodyTooLarge}, errNone
	}
	if now < m.pausedUntil {
		m.skPaused++
		return Result{Reason: SkipPaused}, errNone
	}
	m.c++
	if m.c%m.k != 0 {
		m.skNotSamp++
		return Result{Reason: SkipNotSampled}, errNone
	}
	if m.inFlight >= m.cm {
		m.skBusy++
		return Result{Reason: SkipBusy}, errNone
	}

	id := m.nextID
	m.nextID++
	m.byReq[reqID] = id
	m.mirrors[id] = &mMirror{}
	m.inFlight++
	m.dispatched++
	out := map[string]string{}
	for name, value := range headers {
		lower := strings.ToLower(name)
		if m.sh[lower] {
			continue
		}
		out[lower] = value
	}
	out["x-shadow"] = "1"
	return Result{Reason: SkipNone, ID: id, Headers: out}, errNone
}

// primary 直译 Primary 规则：参数非法 > 未知 > 重复。
func (m *model) primary(reqID string, resp mResp) ErrKind {
	if reqID == "" {
		return ErrInvalidArgument
	}
	id, ok := m.byReq[reqID]
	if !ok {
		return ErrUnknown
	}
	st := m.mirrors[id]
	if st.primary != nil {
		return ErrDuplicate
	}
	rec := resp
	st.primary = &rec
	m.classify(st)
	return errNone
}

// done 直译 Done 规则：resp 为 nil 表示镜像错误。
func (m *model) done(id int64, resp *mResp, now int64) ErrKind {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < m.maxNow {
		return ErrClockRegression
	}
	st, ok := m.mirrors[id]
	if !ok {
		return ErrUnknown
	}
	if st.done {
		return ErrDuplicate
	}

	m.maxNow = now
	st.done = true
	m.inFlight--
	m.completed++
	if resp == nil {
		st.errored = true
		m.merr++
		if now >= m.pausedUntil {
			m.s++
			if m.s >= m.e {
				m.pausedUntil = now + m.p
				m.s = 0
			}
		}
		return errNone
	}
	rec := *resp
	st.shadow = &rec
	m.compOK++
	if now >= m.pausedUntil {
		m.s = 0
	}
	m.classify(st)
	return errNone
}

// classify 直译分类规则：状态码不等或主字段缺失/值不同→破坏；
// 否则镜像有额外字段→兼容；否则相同。忽略字段不参与比较。
func (m *model) classify(st *mMirror) {
	if st.primary == nil || st.shadow == nil || st.errored || st.classified {
		return
	}
	st.classified = true
	p, s := st.primary, st.shadow
	broken := p.status != s.status
	if !broken {
		for name, pv := range p.fields {
			if m.ig[name] {
				continue
			}
			if sv, ok := s.fields[name]; !ok || sv != pv {
				broken = true
				break
			}
		}
	}
	if broken {
		m.breaking++
		return
	}
	for name := range s.fields {
		if m.ig[name] {
			continue
		}
		if _, ok := p.fields[name]; !ok {
			m.compat++
			return
		}
	}
	m.same++
}

// stats 以与 Stats 相同的口径汇总 model 计数。
func (m *model) stats() Stats {
	return Stats{
		MirrorCalls:       m.calls,
		SkippedUnsafe:     m.skUnsafe,
		SkippedTooLarge:   m.skLarge,
		SkippedPaused:     m.skPaused,
		SkippedNotSampled: m.skNotSamp,
		SkippedBusy:       m.skBusy,
		Dispatched:        m.dispatched,
		InFlight:          m.inFlight,
		Completed:         m.completed,
		CompletedOK:       m.compOK,
		MirrorErrors:      m.merr,
		Same:              m.same,
		Compatible:        m.compat,
		Breaking:          m.breaking,
		AwaitingPrimary:   m.compOK - m.same - m.compat - m.breaking,
		Qualified:         m.c,
		ConsecFails:       m.s,
		PausedUntil:       m.pausedUntil,
		MaxNow:            m.maxNow,
	}
}

// ---- 随机序列生成 ----

var (
	rndMethods = []string{"GET", "GET", "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}
	rndHeaders = []string{"x-a", "X-A", "authorization", "Authorization", "cookie", "Cookie",
		"x-shadow", "X-Shadow", "x-token", "x-b"}
	rndFields = []string{"a", "b", "date", "trace"}
	rndValues = []string{"1", "2", "x", "y"}
	shPool    = []string{"authorization", "cookie", "x-token"}
	igPool    = []string{"date", "trace"}
)

func randomConfig(rng *rand.Rand) Config {
	subset := func(pool []string) map[string]bool {
		out := map[string]bool{}
		for _, name := range pool {
			if rng.Intn(2) == 0 {
				out[name] = true
			}
		}
		return out
	}
	return Config{
		K:               1 + int64(rng.Intn(5)),
		Cm:              1 + int64(rng.Intn(3)),
		Bm:              int64(rng.Intn(5)),
		E:               1 + int64(rng.Intn(3)),
		P:               1 + int64(rng.Intn(60)),
		AllowUnsafe:     rng.Intn(2) == 0,
		SensitiveHeader: subset(shPool),
		IgnoreField:     subset(igPool),
	}
}

// randomNow 生成时间：多数单调前进，偶发回退或越界。
func randomNow(rng *rand.Rand, maxNow int64) int64 {
	switch r := rng.Intn(40); {
	case r == 0:
		return -1
	case r == 1:
		return 1_000_000_000_000_001
	case r < 5 && maxNow > 0:
		return maxNow - 1 // 时钟回退
	default:
		return maxNow + int64(rng.Intn(4))
	}
}

func randomHeaders(rng *rand.Rand) map[string]string {
	n := rng.Intn(4)
	out := map[string]string{}
	for i := 0; i < n; i++ {
		name := rndHeaders[rng.Intn(len(rndHeaders))]
		if rng.Intn(40) == 0 {
			name = "" // 偶发非法空头名
		}
		out[name] = fmt.Sprintf("v%d", rng.Intn(3))
	}
	return out
}

func randomResp(rng *rand.Rand) mResp {
	status := 200
	if rng.Intn(6) == 0 {
		status = 500
	}
	fields := map[string]string{}
	for _, name := range rndFields {
		if rng.Intn(2) == 0 {
			fields[name] = rndValues[rng.Intn(len(rndValues))]
		}
	}
	return mResp{status: status, fields: fields}
}

func toDiffResp(r mResp) diff.Response {
	return diff.Response{Status: r.status, Fields: r.fields}
}

func kindOf(err error) ErrKind {
	if err == nil {
		return errNone
	}
	if me, ok := err.(*Error); ok {
		return me.Kind
	}
	panic(fmt.Sprintf("非 *Error 类型错误: %v", err))
}

// recOp 记录一次操作及其在首个实例上的输出，用于重放对照。
type recOp struct {
	kind    string // "mirror" | "primary" | "done"
	reqID   string
	method  string
	bodyLen int64
	headers map[string]string
	id      int64
	resp    *mResp
	now     int64

	out     Result
	errKind ErrKind
}

// applyOp 在真实实例上执行记录的操作并返回输出。
func applyOp(m *Mirror, op recOp) (Result, ErrKind) {
	switch op.kind {
	case "mirror":
		res, err := m.Mirror(op.reqID, op.method, op.bodyLen, op.headers, op.now)
		return res, kindOf(err)
	case "primary":
		return Result{}, kindOf(m.Primary(op.reqID, toDiffResp(*op.resp)))
	case "done":
		var resp *diff.Response
		if op.resp != nil {
			r := toDiffResp(*op.resp)
			resp = &r
		}
		return Result{}, kindOf(m.Done(op.id, resp, op.now))
	}
	panic("未知操作: " + op.kind)
}

// applyOpModel 在朴素模拟上执行记录的操作并返回输出。
func applyOpModel(m *model, op recOp) (Result, ErrKind) {
	switch op.kind {
	case "mirror":
		return m.mirror(op.reqID, op.method, op.bodyLen, op.headers, op.now)
	case "primary":
		return Result{}, m.primary(op.reqID, *op.resp)
	case "done":
		return Result{}, m.done(op.id, op.resp, op.now)
	}
	panic("未知操作: " + op.kind)
}

func describeOp(op recOp) string {
	switch op.kind {
	case "mirror":
		return fmt.Sprintf("Mirror(req=%q method=%s bodyLen=%d headers=%v now=%d)",
			op.reqID, op.method, op.bodyLen, op.headers, op.now)
	case "primary":
		return fmt.Sprintf("Primary(req=%q resp=%+v)", op.reqID, *op.resp)
	case "done":
		return fmt.Sprintf("Done(id=%d resp=%+v now=%d)", op.id, op.resp, op.now)
	}
	return "?"
}

// TestRandomSequencesAgainstModel 以固定种子生成 2000 组随机操作序列，
// 逐步对照真实实现与朴素模拟的输出与统计，并打印输入、输出与判定依据。
func TestRandomSequencesAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		cfg := randomConfig(rng)
		real, err := New(cfg)
		if err != nil {
			t.Fatalf("seq %d: 构造失败: %v", seq, err)
		}
		sim := newModel(cfg)
		t.Logf("seq %d: cfg=%+v", seq, cfg)

		var ops []recOp
		n := 20 + rng.Intn(60)
		for i := 0; i < n; i++ {
			var op recOp
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4:
				op = recOp{kind: "mirror"}
				if rng.Intn(20) == 0 {
					op.reqID = ""
				} else {
					op.reqID = fmt.Sprintf("r%d", rng.Intn(8))
				}
				if rng.Intn(20) == 0 {
					op.method = "BAD"
				} else {
					op.method = rndMethods[rng.Intn(len(rndMethods))]
				}
				switch rng.Intn(30) {
				case 0:
					op.bodyLen = -1
				case 1:
					op.bodyLen = 1_000_000_000_001
				default:
					op.bodyLen = int64(rng.Intn(8))
				}
				op.headers = randomHeaders(rng)
				op.now = randomNow(rng, sim.maxNow)
			case 5, 6, 7:
				op = recOp{kind: "primary"}
				if rng.Intn(30) == 0 {
					op.reqID = ""
				} else {
					op.reqID = fmt.Sprintf("r%d", rng.Intn(8))
				}
				r := randomResp(rng)
				op.resp = &r
			default:
				op = recOp{kind: "done"}
				op.id = int64(rng.Intn(int(sim.nextID) + 2))
				if rng.Intn(10) < 3 {
					op.resp = nil // 镜像错误
				} else {
					r := randomResp(rng)
					op.resp = &r
				}
				op.now = randomNow(rng, sim.maxNow)
			}

			gotRes, gotKind := applyOp(real, op)
			wantRes, wantKind := applyOpModel(sim, op)
			op.out, op.errKind = gotRes, gotKind
			ops = append(ops, op)

			t.Logf("seq %d op %d: %s -> reason=%s id=%d headers=%v err=%v (依据: %s)",
				seq, i, describeOp(op), gotRes.Reason, gotRes.ID, gotRes.Headers, gotKind,
				rationale(cfg, sim, op))

			if gotKind != wantKind {
				t.Fatalf("seq %d op %d: %s\n错误类别=%v, 模拟=%v", seq, i, describeOp(op), gotKind, wantKind)
			}
			if gotKind == errNone && !reflect.DeepEqual(gotRes, wantRes) {
				t.Fatalf("seq %d op %d: %s\n结果=%+v, 模拟=%+v", seq, i, describeOp(op), gotRes, wantRes)
			}
			if got, want := real.Stats(), sim.stats(); got != want {
				t.Fatalf("seq %d op %d: %s\n统计=%+v\n模拟=%+v", seq, i, describeOp(op), got, want)
			}
		}

		// 重放确定性：相同操作序列在全新实例上得到完全相同的结果与统计。
		replay, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for i, op := range ops {
			res, kind := applyOp(replay, op)
			if kind != op.errKind || (kind == errNone && !reflect.DeepEqual(res, op.out)) {
				t.Fatalf("seq %d 重放 op %d 不一致: %s", seq, i, describeOp(op))
			}
		}
		if got, want := replay.Stats(), sim.stats(); got != want {
			t.Fatalf("seq %d 重放统计不一致:\n%v\n%v", seq, got, want)
		}
		assertInvariants(t, cfg, sim.stats())
	}
}

// rationale 给出当前操作判定依据的可读说明（用于日志）。
func rationale(cfg Config, sim *model, op recOp) string {
	if op.kind != "mirror" {
		return fmt.Sprintf("c=%d s=%d pausedUntil=%d inFlight=%d", sim.c, sim.s, sim.pausedUntil, sim.inFlight)
	}
	return fmt.Sprintf("k=%d cm=%d bm=%d au=%v c=%d pausedUntil=%d inFlight=%d",
		cfg.K, cfg.Cm, cfg.Bm, cfg.AllowUnsafe, sim.c, sim.pausedUntil, sim.inFlight)
}
