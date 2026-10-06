package fontcore

import (
	"log"
	"sync"
)

// Package fontcore 是网页字体匹配、子集加载与回退渲染的协调内核。
//
// 五部分协作：
//   - 登记（Register）：族/人脸声明校验、重复检测、字符范围索引；
//   - 匹配（family.match）：宽度→倾斜→字重三维无歧义选择；
//   - 子集加载（trigger/Loaded/Failed）：人脸仅在与待渲染文本有交集
//     且匹配胜出时首次触发，期起算点固定为首次触发时刻；
//   - 状态期（periods/primaryReady）：阻塞/交换/回退/可选四策略的
//     阻塞期与交换期，边界左闭右开；
//   - 整形（Shape/ShapeChars）：逐字符给出 (族, 人脸, 时期, 合成斜体)
//     并合并连续段落，回退族只用已就绪人脸且不触发自身加载。
//
// 所有可变状态由单把互斥量保护，并发调用等价于某个串行顺序。

// faceLoad 保存每张人脸的加载状态期信息。
type faceLoad struct {
	triggered bool  // 是否曾被整形触发
	triggerAt int64 // 首次触发时刻（期的起算点）
	loaded    bool  // 是否报告过加载完成
	loadAt    int64
	failed    bool // 是否报告过加载失败
	failAt    int64
}

// engineState 是受单一互斥量保护的全部可变状态。
// Engine 是协调内核；全部入口在同一把锁下串行化，
// 因而并发调用的结果等价于某个满足互斥顺序的串行执行。
type Engine struct {
	mu       sync.Mutex
	cfg      Config
	now      int64
	families map[string]*family
	loads    map[[2]string]*faceLoad // (family, face)
	logger   *log.Logger
}

// New 创建内核并校验配置（阈值颠倒、负时长等先于一切状态变更被拒绝）。
func New(cfg Config) (*Engine, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &Engine{
		cfg:      cfg,
		families: map[string]*family{},
		loads:    map[[2]string]*faceLoad{},
	}, nil
}

// SetLogger 打开输入/输出/判定依据日志（传 nil 关闭）。
func (e *Engine) SetLogger(l *log.Logger) {
	e.mu.Lock()
	e.logger = l
	e.mu.Unlock()
}

func (e *Engine) logf(format string, args ...any) {
	if e.logger != nil {
		e.logger.Printf(format, args...)
	}
}

// Register 登记一个字体族。
func (e *Engine) Register(spec FamilySpec) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	// 拒绝次序：参数非法 -> 重复登记（族重名）。
	fm, err := newFamily(spec, e.cfg)
	if err != nil {
		e.logf("register REJECT name=%q err=%v", spec.Name, err)
		return err
	}
	if _, exists := e.families[spec.Name]; exists {
		err = wrapErr(ErrDuplicateRegister, "family already registered: "+spec.Name)
		e.logf("register REJECT name=%q err=%v", spec.Name, err)
		return err
	}
	e.families[spec.Name] = fm
	for _, fc := range fm.faces {
		e.loads[[2]string{spec.Name, fc.spec.Name}] = &faceLoad{}
	}
	e.logf("register OK family=%q faces=%d fallbacks=%v",
		spec.Name, len(fm.faces), spec.Fallbacks)
	return nil
}

// Advance 单调推进时钟；回退直接拒绝且不改变任何状态。
func (e *Engine) Advance(t int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t < e.now {
		err := wrapErr(ErrClockRewound, sprintf("now=%d requested=%d", e.now, t))
		e.logf("advance REJECT %v", err)
		return err
	}
	if t != e.now {
		e.logf("advance %d -> %d", e.now, t)
	}
	e.now = t
	return nil
}

// Now 返回当前时钟。
func (e *Engine) Now() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// resolveLoad 按“参数非法 -> 时钟回退 -> 族不存在 -> 人脸不存在 ->
// 加载状态不允许”的次序校验并定位加载记录。调用方持锁。
func (e *Engine) resolveLoad(familyName, faceName string) (*family, *face, *faceLoad, error) {
	fm, ok := e.families[familyName]
	if !ok {
		return nil, nil, nil, wrapErr(ErrFamilyNotFound, familyName)
	}
	fc, ok := fm.byName[faceName]
	if !ok {
		return nil, nil, nil, wrapErr(ErrFaceNotFound, familyName+"/"+faceName)
	}
	ld := e.loads[[2]string{familyName, faceName}]
	if !ld.triggered {
		return nil, nil, nil, wrapErr(ErrInvalidLoadState,
			"face never triggered: "+familyName+"/"+faceName)
	}
	return fm, fc, ld, nil
}

// Loaded 报告某张人脸资源加载完成。
func (e *Engine) Loaded(familyName, faceName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _, ld, err := e.resolveLoad(familyName, faceName)
	if err != nil {
		e.logf("loaded REJECT %s/%s err=%v", familyName, faceName, err)
		return err
	}
	if ld.loaded || ld.failed {
		err = wrapErr(ErrInvalidLoadState,
			"face already settled: "+familyName+"/"+faceName)
		e.logf("loaded REJECT %s/%s err=%v", familyName, faceName, err)
		return err
	}
	ld.loaded = true
	ld.loadAt = e.now
	e.logf("loaded OK %s/%s at t=%d", familyName, faceName, e.now)
	return nil
}

// Failed 报告某张人脸资源加载失败。
func (e *Engine) Failed(familyName, faceName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _, ld, err := e.resolveLoad(familyName, faceName)
	if err != nil {
		e.logf("failed REJECT %s/%s err=%v", familyName, faceName, err)
		return err
	}
	if ld.loaded || ld.failed {
		err = wrapErr(ErrInvalidLoadState,
			"face already settled: "+familyName+"/"+faceName)
		e.logf("failed REJECT %s/%s err=%v", familyName, faceName, err)
		return err
	}
	ld.failed = true
	ld.failAt = e.now
	e.logf("failed OK %s/%s at t=%d", familyName, faceName, e.now)
	return nil
}

// periods 返回 (阻塞期长度, 交换期长度, 是否存在交换期)。
// 边界一律左闭右开，由调用方用严格不等式比较。
func (e *Engine) periods(fc *face) (int64, int64, bool) {
	c := e.cfg
	switch fc.spec.Display {
	case DisplayBlock:
		return c.BlockBlock, 0, true // 交换期无限
	case DisplaySwap:
		return c.SwapBlock, 0, true
	case DisplayFallback:
		return c.FallbackBlock, c.FallbackSwap, true
	default: // DisplayOptional
		return c.OptionalBlock, 0, false
	}
}

// renderState 是单字符渲染判定的直接产物。
type renderState struct {
	family          string
	face            string
	phase           Phase
	syntheticItalic bool
}

type shapeReq struct {
	weight int
	width  int
	style  Style
}

// trigger 首次触发人脸加载；重复调用不会重算起算时刻。
// 返回该人脸的加载记录。调用方持锁。
func (e *Engine) trigger(familyName string, fc *face) *faceLoad {
	key := [2]string{familyName, fc.spec.Name}
	ld := e.loads[key]
	if !ld.triggered {
		ld.triggered = true
		ld.triggerAt = e.now
		e.logf("trigger family=%q face=%q at t=%d resource=%s",
			familyName, fc.spec.Name, e.now, fc.spec.Resource)
	}
	return ld
}

// primaryReady 判断主人脸此刻是否可用于立即渲染，
// 并区分“尚未到期”“永久回退”。调用方持锁。
// 返回 (可用, 是否已永久回退)。
func (e *Engine) primaryReady(fc *face, ld *faceLoad) (bool, bool) {
	blockLen, swapLen, hasSwap := e.periods(fc)
	blockEnd := ld.triggerAt + blockLen
	if ld.failed {
		return false, true
	}
	if ld.loaded {
		// 左闭右开：完成时刻恰好等于期末时刻，视为下一期，替换无效。
		if !hasSwap {
			if ld.loadAt < blockEnd {
				return true, false
			}
			return false, true // 可选策略：期满后完成永不替换
		}
		if fc.spec.Display == DisplayFallback {
			swapEnd := blockEnd + swapLen
			if ld.loadAt < swapEnd {
				return true, false
			}
			return false, true
		}
		return true, false // 阻塞/交换：交换期无限，完成即可替换
	}
	return false, false
}

// fallbackFace 在回退族列表中找第一个“已就绪且覆盖该字符”的族，
// 在其已就绪人脸中做三维匹配。回退族自身的加载绝不被触发。
// 调用方持锁。
func (e *Engine) fallbackFace(fm *family, r rune, req shapeReq) (*family, *face, bool) {
	for _, fbName := range fm.fallbacks {
		fb, ok := e.families[fbName]
		if !ok {
			continue
		}
		var ids []int
		ids = fb.coveringIDs(r, ids)
		var best *face
		var bestK matchKey
		for _, id := range ids {
			cand := fb.faces[id]
			ld := e.loads[[2]string{fbName, cand.spec.Name}]
			if !ld.loaded {
				continue // 只用已就绪人脸，不触发加载
			}
			k := keyFor(cand, req.weight, req.width, req.style,
				e.cfg.WeightLow, e.cfg.WeightHigh)
			if best == nil || lessKey(k, bestK) {
				best, bestK = cand, k
			}
		}
		if best != nil {
			return fb, best, true
		}
	}
	return nil, nil, false
}

// renderOne 计算单字符的最终渲染状态；triggerAllowed 标识当前族是否为
// 主族（回退族解析时为 false，绝不触发其加载）。调用方持锁。
func (e *Engine) renderOne(fm *family, r rune, req shapeReq, triggerAllowed bool) renderState {
	fc, covered := fm.match(r, req.weight, req.width, req.style,
		e.cfg.WeightLow, e.cfg.WeightHigh)
	if !covered {
		// 主族无人脸覆盖：直接看回退族的已就绪人脸，不触发任何加载。
		if fbf, fface, ok := e.fallbackFace(fm, r, req); ok {
			return renderState{
				family:          fbf.name,
				face:            fface.spec.Name,
				phase:           PhaseSwap,
				syntheticItalic: req.style == StyleItalic && fface.spec.Style == StyleNormal,
			}
		}
		return renderState{family: fm.name, phase: PhaseLastResort}
	}

	var ld *faceLoad
	if triggerAllowed {
		ld = e.trigger(fm.name, fc)
	} else {
		ld = e.loads[[2]string{fm.name, fc.spec.Name}]
	}
	synth := req.style == StyleItalic && fc.spec.Style == StyleNormal

	// 显式失败立即永久回退，即使仍在阻塞期内也不再显示占位。
	if ld.failed {
		if fbf, fface, ok := e.fallbackFace(fm, r, req); ok {
			return renderState{
				family: fbf.name, face: fface.spec.Name, phase: PhaseFailed,
				syntheticItalic: req.style == StyleItalic && fface.spec.Style == StyleNormal,
			}
		}
		return renderState{family: fm.name, face: fc.spec.Name,
			phase: PhaseLastResort, syntheticItalic: synth}
	}

	if ready, permanent := e.primaryReady(fc, ld); ready {
		return renderState{family: fm.name, face: fc.spec.Name,
			phase: PhasePrimary, syntheticItalic: synth}
	} else if permanent {
		if fbf, fface, ok := e.fallbackFace(fm, r, req); ok {
			return renderState{
				family: fbf.name, face: fface.spec.Name, phase: PhaseFailed,
				syntheticItalic: req.style == StyleItalic && fface.spec.Style == StyleNormal,
			}
		}
		return renderState{family: fm.name, face: fc.spec.Name,
			phase: PhaseLastResort, syntheticItalic: synth}
	}

	// 未就绪且未永久失败：按阻塞期 / 交换期判定。
	blockLen, _, _ := e.periods(fc)
	blockEnd := ld.triggerAt + blockLen
	if e.now < blockEnd {
		return renderState{family: fm.name, face: fc.spec.Name,
			phase: PhaseBlock, syntheticItalic: synth}
	}
	if fc.spec.Display == DisplayOptional {
		// 可选策略没有交换期：阻塞期满即永久回退。
		if fbf, fface, ok := e.fallbackFace(fm, r, req); ok {
			return renderState{
				family: fbf.name, face: fface.spec.Name, phase: PhaseFailed,
				syntheticItalic: req.style == StyleItalic && fface.spec.Style == StyleNormal,
			}
		}
		return renderState{family: fm.name, face: fc.spec.Name,
			phase: PhaseLastResort, syntheticItalic: synth}
	}
	if fbf, fface, ok := e.fallbackFace(fm, r, req); ok {
		return renderState{
			family: fbf.name, face: fface.spec.Name, phase: PhaseSwap,
			syntheticItalic: req.style == StyleItalic && fface.spec.Style == StyleNormal,
		}
	}
	// 交换期但没有可用回退：仍标记为交换期的最后手段占位。
	return renderState{family: fm.name, face: fc.spec.Name,
		phase: PhaseLastResort, syntheticItalic: synth}
}

func validateShapeReq(weight, width int, style Style) error {
	if weight < 0 || width <= 0 {
		return invalidf("weight/width invalid: %d/%d", weight, width)
	}
	if style != StyleNormal && style != StyleItalic {
		return invalidf("unknown style %d", style)
	}
	return nil
}

// ShapeChars 逐字符整形，触发胜出人脸的加载，返回每字符渲染状态。
func (e *Engine) ShapeChars(familyName, text string, weight, width int, style Style) ([]CharResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := validateShapeReq(weight, width, style); err != nil {
		return nil, err
	}
	fm, ok := e.families[familyName]
	if !ok {
		return nil, wrapErr(ErrFamilyNotFound, familyName)
	}
	req := shapeReq{weight: weight, width: width, style: style}
	out := make([]CharResult, 0, len([]rune(text)))
	for _, r := range text {
		st := e.renderOne(fm, r, req, true)
		out = append(out, CharResult{
			Family:          st.family,
			Face:            st.face,
			Phase:           st.phase,
			SyntheticItalic: st.syntheticItalic,
		})
	}
	e.logf("shape family=%q q=(w=%d,wd=%d,it=%v) t=%d chars=%d -> %+v",
		familyName, weight, width, style == StyleItalic, e.now, len(out), out)
	return out, nil
}

// Shape 整形并把连续使用相同（族, 人脸, 时期, 合成斜体）的字符合并成段落。
func (e *Engine) Shape(familyName, text string, weight, width int, style Style) ([]Run, error) {
	chars, err := e.ShapeChars(familyName, text, weight, width, style)
	if err != nil {
		return nil, err
	}
	var runs []Run
	bytePos := 0
	runeIdx := 0
	for _, r := range text {
		size := len(string(r))
		c := chars[runeIdx]
		if len(runs) > 0 {
			last := &runs[len(runs)-1]
			if last.Family == c.Family && last.Face == c.Face &&
				last.Phase == c.Phase && last.SyntheticItalic == c.SyntheticItalic {
				last.End = bytePos + size
				last.Runes++
				bytePos += size
				runeIdx++
				continue
			}
		}
		runs = append(runs, Run{
			Family:          c.Family,
			Face:            c.Face,
			Phase:           c.Phase,
			SyntheticItalic: c.SyntheticItalic,
			Start:           bytePos,
			End:             bytePos + size,
			Runes:           1,
		})
		bytePos += size
		runeIdx++
	}
	e.logf("shape merged -> %d runs", len(runs))
	return runs, nil
}
