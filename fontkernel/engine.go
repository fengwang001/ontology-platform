package fontkernel

import (
	"io"
	"log"
	"sync"
)

// Engine 是协调内核。所有方法可被并发调用；一把互斥锁把登记、加载报告、
// 时钟推进与整形串行化，使可观察结果等价于某个串行顺序。
type Engine struct {
	mu    sync.Mutex
	cfg   Config
	log   *log.Logger
	reg   *registry
	now   int64
	state map[string]map[int]*faceState // state[family][faceIndex]
}

type faceState struct {
	status    FaceStatus
	triggerAt int64
	loadedAt  int64
	gaveUp    bool // 已永久回退，后续完成不得替换
}

// New 创建内核。logger 为 nil 时丢弃日志。
func New(cfg Config, logger io.Writer) *Engine {
	if logger == nil {
		logger = io.Discard
	}
	return &Engine{
		cfg:   cfg,
		log:   log.New(logger, "[fontkernel] ", log.LstdFlags|log.Lmicroseconds),
		reg:   newRegistry(),
		state: map[string]map[int]*faceState{},
	}
}

func (e *Engine) validateConfig() error {
	if e.cfg.WeightLow > e.cfg.WeightHigh || e.cfg.WeightLow <= 0 || e.cfg.WeightHigh > 1000 {
		return ErrInvalidArgument
	}
	if e.cfg.NormalWidth <= 0 {
		e.cfg.NormalWidth = 100
	}
	for _, d := range []int64{
		e.cfg.BlockBlockPeriod, e.cfg.SwapBlockPeriod,
		e.cfg.FallbackBlockPeriod, e.cfg.FallbackSwapPeriod,
		e.cfg.OptionalBlockPeriod,
	} {
		if d < 0 {
			return ErrInvalidArgument
		}
	}
	return nil
}

// RegisterFamily 登记字体族。
func (e *Engine) RegisterFamily(spec FamilySpec) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.validateConfig(); err != nil {
		e.log.Printf("REGISTER input=%+v => error=%v", spec, err)
		return err
	}
	if err := e.reg.register(spec); err != nil {
		e.log.Printf("REGISTER input=%+v => error=%v", spec, err)
		return err
	}
	faces := map[int]*faceState{}
	for i := range spec.Faces {
		faces[i] = &faceState{status: FaceUntriggered, triggerAt: -1, loadedAt: -1}
	}
	e.state[spec.Name] = faces
	e.log.Printf("REGISTER family=%s faces=%d => ok", spec.Name, len(spec.Faces))
	return nil
}

// Advance 单调推进逻辑时钟，并使已到期的时期转换生效。
func (e *Engine) Advance(t int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t < 0 {
		return ErrInvalidArgument
	}
	if t < e.now {
		e.log.Printf("ADVANCE input=%d now=%d => error=%v", t, e.now, ErrClockRewound)
		return ErrClockRewound
	}
	if t == e.now {
		return nil
	}
	old := e.now
	e.sweep(t)
	e.now = t
	e.log.Printf("ADVANCE %d -> %d: expired transitions applied", old, t)
	return nil
}

// ReportLoaded 报告某张人脸资源加载完成。
func (e *Engine) ReportLoaded(family string, face int, at int64) error {
	return e.report(family, face, at, true)
}

// ReportFailed 报告某张人脸资源加载失败。
func (e *Engine) ReportFailed(family string, face int, at int64) error {
	return e.report(family, face, at, false)
}

func (e *Engine) report(family string, face int, at int64, loaded bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	// 拒绝次序：参数非法 → 时钟回退 → 族不存在 → 人脸不存在 → 加载状态不允许。
	if at < 0 || face < 0 {
		return ErrInvalidArgument
	}
	if at < e.now {
		e.log.Printf("REPORT family=%s face=%d at=%d => %v", family, face, at, ErrClockRewound)
		return ErrClockRewound
	}
	fam, ok := e.reg.get(family)
	if !ok {
		return ErrFamilyMissing
	}
	if face >= len(fam.faces) {
		return ErrFaceMissing
	}
	st := e.state[family][face]
	if st.status == FaceUntriggered {
		return ErrInvalidLoadOp
	}
	if st.status == FaceLoaded || st.status == FaceFailed || st.gaveUp {
		return ErrInvalidLoadOp
	}
	if at > e.now {
		e.sweep(at)
		e.now = at
	}
	switch {
	case !loaded:
		st.status = FaceFailed
		st.gaveUp = true
	case st.gaveUp:
		st.status = FaceLoadedLate
	default:
		st.status = FaceLoaded
		st.loadedAt = at
	}
	e.log.Printf("REPORT family=%s face=%d at=%d loaded=%v => status=%d", family, face, at, loaded, st.status)
	return nil
}

// periods 返回人脸自触发时刻起的 (阻塞期长, 交换期长)。swap < 0 表示交换期无限。
func (e *Engine) periods(p Policy) (block, swap int64) {
	switch p {
	case PolicyBlock:
		return e.cfg.BlockBlockPeriod, -1
	case PolicySwap:
		return e.cfg.SwapBlockPeriod, -1
	case PolicyFallback:
		return e.cfg.FallbackBlockPeriod, e.cfg.FallbackSwapPeriod
	case PolicyOptional:
		return e.cfg.OptionalBlockPeriod, 0
	}
	return 0, -1
}

// sweep 使截止 t 的全部到期转换生效；调用方持锁。
func (e *Engine) sweep(t int64) {
	for fname, faces := range e.state {
		for fi, st := range faces {
			e.sweepFace(fname, fi, st, t)
		}
	}
}

// sweepFace 使单张人脸截止 t 的到期转换生效；调用方持锁。
func (e *Engine) sweepFace(fname string, fi int, st *faceState, t int64) {
	if st.status != FaceLoading || st.triggerAt < 0 {
		return
	}
	fam := e.reg.families[fname]
	block, swap := e.periods(fam.faces[fi].Policy)
	blockEnd := st.triggerAt + block
	if t < blockEnd {
		return // 左闭右开：t == blockEnd 即已离开阻塞期
	}
	if swap < 0 || t < blockEnd+swap {
		return // 仍在交换期（swap==0 时 blockEnd 即交换期结束时刻）
	}
	st.status = FaceFailed
	st.gaveUp = true
	e.log.Printf("EXPIRE family=%s face=%d at=%d => permanent fallback", fname, fi, t)
}

// Shape 对一段文本进行逐字符裁决与段落合并。整形在当前时钟下进行，
// 推进时刻之前已经发生的转换都已由 Advance/sweep 落入状态机。
func (e *Engine) Shape(req ShapeRequest) (*ShapeResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if req.Weight <= 0 || req.Weight > 1000 || req.Width <= 0 {
		return nil, ErrInvalidArgument
	}
	if req.Style != StyleNormal && req.Style != StyleItalic {
		return nil, ErrInvalidArgument
	}
	fam, ok := e.reg.get(req.Family)
	if !ok {
		return nil, ErrFamilyMissing
	}

	q := matchQuery{weight: req.Weight, style: req.Style, width: req.Width}
	results := make([]RuneResult, 0, len(req.Text))
	e.log.Printf("SHAPE input family=%s fallback=%v weight=%d style=%d width=%v text=%q at=%d",
		req.Family, req.Fallback, req.Weight, req.Style, req.Width, req.Text, e.now)

	for _, r := range req.Text {
		res := e.shapeRune(fam, req.Fallback, q, r)
		results = append(results, res)
		e.log.Printf("SHAPE rune=%q winner=%s/%d period=%d render=%s/%d synthetic=%v last=%v",
			string(r), res.WinnerFamily, res.WinnerFace, res.Period,
			res.RenderFamily, res.RenderFace, res.SyntheticItalic, res.LastResort)
	}

	out := &ShapeResult{Time: e.now, Runes: results, Segments: mergeSegments(results)}
	e.log.Printf("SHAPE output runs=%d segments=%d", len(out.Runes), len(out.Segments))
	return out, nil
}

// shapeRune 裁决单个字符；调用方持锁。
func (e *Engine) shapeRune(fam *family, fallback []string, q matchQuery, r rune) RuneResult {
	res := RuneResult{Rune: r, RenderFace: -1, WinnerFace: -1}
	cands := coveringCandidates(fam, r)
	winner := matchFace(q, cands, e.cfg)
	res.WinnerFamily = fam.name
	res.WinnerFace = winner

	if winner < 0 {
		// 主族无人脸覆盖该字符：直接走回退，且不触发主族加载。
		return e.applyFallback(res, fam, fallback, q, r, true)
	}
	spec := fam.faces[winner]
	res.SyntheticItalic = q.style == StyleItalic && spec.Style == StyleNormal
	st := e.state[fam.name][winner]

	// 仅触发胜出人脸，且每张人脸至多触发一次。
	if st.status == FaceUntriggered {
		st.status = FaceLoading
		st.triggerAt = e.now
		e.log.Printf("TRIGGER family=%s face=%d at=%d url=%s", fam.name, winner, e.now, spec.URL)
		// 触发可能发生在某次时钟推进之后：立即按当前时钟判定其时期是否已到期。
		e.sweepFace(fam.name, winner, st, e.now)
	}

	switch {
	case st.status == FaceLoaded:
		res.Period = PeriodLoaded
		res.RenderFamily = fam.name
		res.RenderFace = winner
	case st.status == FaceLoading && e.inBlock(spec.Policy, st):
		res.Period = PeriodBlock
		res.RenderFamily = fam.name // 占位仍归属目标族/人脸，只是不可见
		res.RenderFace = winner
	case st.gaveUp:
		res.Period = PeriodFallbackPermanent
		res = e.applyFallback(res, fam, fallback, q, r, false)
		res.Period = PeriodFallbackPermanent
	default: // 交换期
		res.Period = PeriodSwap
		res = e.applyFallback(res, fam, fallback, q, r, false)
		res.Period = PeriodSwap
	}
	return res
}

func (e *Engine) inBlock(p Policy, st *faceState) bool {
	block, _ := e.periods(p)
	return e.now < st.triggerAt+block
}

// applyFallback 按回退族次序选择第一个“已就绪且覆盖该字符”的族。
// 回退族不触发自身加载；若主族本身无覆盖（noWinner），胜出字段保留空/-1。
func (e *Engine) applyFallback(res RuneResult, primary *family, fallback []string, q matchQuery, r rune, noWinner bool) RuneResult {
	for _, fname := range fallback {
		fam, ok := e.reg.get(fname)
		if !ok || fname == primary.name {
			continue
		}
		cands := coveringCandidates(fam, r)
		pick := matchFace(q, cands, e.cfg)
		if pick < 0 {
			continue
		}
		st := e.state[fname][pick]
		if st.status != FaceLoaded {
			continue // 回退族不触发加载：只接受已就绪人脸
		}
		spec := fam.faces[pick]
		res.RenderFamily = fname
		res.RenderFace = pick
		res.SyntheticItalic = q.style == StyleItalic && spec.Style == StyleNormal
		res.LastResort = false
		return res
	}
	res.RenderFamily = ""
	res.RenderFace = -1
	res.LastResort = true
	return res
}

func coveringCandidates(fam *family, r rune) []matchCandidate {
	idx := fam.coveringFaces(r)
	cands := make([]matchCandidate, 0, len(idx))
	for _, i := range idx {
		cands = append(cands, matchCandidate{face: i, spec: fam.faces[i]})
	}
	return cands
}

// mergeSegments 合并连续使用相同渲染身份的字符。
func mergeSegments(rs []RuneResult) []Segment {
	var segs []Segment
	flush := func(buf []rune, cur RuneResult) {
		segs = append(segs, Segment{
			Text:            string(buf),
			Family:          cur.RenderFamily,
			Face:            cur.RenderFace,
			Period:          cur.Period,
			SyntheticItalic: cur.SyntheticItalic,
			LastResort:      cur.LastResort,
		})
	}
	var buf []rune
	var cur RuneResult
	for i, r := range rs {
		if i == 0 || !sameRender(cur, r) {
			if i > 0 {
				flush(buf, cur)
			}
			buf = buf[:0]
			cur = r
		}
		buf = append(buf, r.Rune)
	}
	if len(buf) > 0 {
		flush(buf, cur)
	}
	return segs
}

func sameRender(a, b RuneResult) bool {
	return a.RenderFamily == b.RenderFamily && a.RenderFace == b.RenderFace &&
		a.Period == b.Period && a.SyntheticItalic == b.SyntheticItalic &&
		a.LastResort == b.LastResort
}

// Now 返回当前逻辑时钟。
func (e *Engine) Now() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// States 返回指定族（family 为空时全部族）人脸的状态快照。
func (e *Engine) States(family string) []FaceState {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []FaceState
	names := e.reg.order
	if family != "" {
		names = []string{family}
	}
	for _, fname := range names {
		fam, ok := e.reg.get(fname)
		if !ok {
			continue
		}
		for i := range fam.faces {
			st := e.state[fname][i]
			out = append(out, FaceState{
				Family: fname, Face: i, Status: st.status,
				TriggerAt: st.triggerAt, LoadedAt: st.loadedAt,
			})
		}
	}
	return out
}
