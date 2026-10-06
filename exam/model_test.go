package exam

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 朴素模型：按规则独立实现，刻意使用 O(n) 的全量重算，
// 与引擎的 O(1) 增量实现互为对照。

type modelSession struct {
	status   Status
	start    int64
	budget   int64
	deadline int64

	activeIntervals [][2]int64 // 已关闭的进行区间
	activeStart     int64      // 打开的进行区间起点，-1 表示无
	pauseIntervals  [][2]int64
	pauseStart      int64

	answers    []Answer // 全部已接受作答
	events     []int64  // 全部事件到达时刻
	warnings   int
	generation int64
	token      string
	waterline  int64
	maxSeenSeq int64
	settled    *Settlement
}

type model struct {
	cfg      Config
	now      int64
	sessions map[string]*modelSession
	tokenSeq int64
	note     string // 上一步的判定依据，供日志输出
}

func newModel(cfg Config) *model {
	return &model{cfg: cfg, sessions: make(map[string]*modelSession)}
}

func (ms *modelSession) activeTime(t int64) int64 {
	total := int64(0)
	for _, iv := range ms.activeIntervals {
		total += iv[1] - iv[0]
	}
	if ms.activeStart >= 0 {
		total += t - ms.activeStart
	}
	return total
}

func (ms *modelSession) pauseTotal(t int64) int64 {
	total := int64(0)
	for _, iv := range ms.pauseIntervals {
		total += iv[1] - iv[0]
	}
	if ms.pauseStart >= 0 {
		total += t - ms.pauseStart
	}
	return total
}

func (ms *modelSession) used(t int64, cfg *Config) int64 {
	excess := ms.pauseTotal(t) - cfg.PauseBudget
	if excess < 0 {
		excess = 0
	}
	return ms.activeTime(t) + excess
}

// autoEnd 用二分查找独立求解作答预算耗尽时刻。
func (ms *modelSession) autoEnd(cfg *Config) (int64, EndReason, bool) {
	if ms.status == StatusEnded {
		return 0, EndReasonNone, false
	}
	best, reason := ms.deadline, EndReasonDeadline
	if ms.used(ms.deadline, cfg) >= ms.budget {
		lo, hi := ms.start, ms.deadline
		for lo < hi {
			mid := lo + (hi-lo)/2
			if ms.used(mid, cfg) >= ms.budget {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		if lo <= best {
			best, reason = lo, EndReasonTimeExhausted
		}
	}
	if ms.status == StatusPaused {
		if limit := ms.pauseStart + cfg.SinglePauseMax + 1; limit < best {
			best, reason = limit, EndReasonPauseLimit
		}
	}
	return best, reason, true
}

func (ms *modelSession) settle(end int64, reason EndReason, violation bool, cfg *Config) {
	bestSeq := make(map[string]int64)
	answers := make(map[string]LandedAnswer)
	for _, a := range ms.answers {
		if cur, ok := bestSeq[a.QuestionID]; !ok || a.Seq > cur {
			bestSeq[a.QuestionID] = a.Seq
			answers[a.QuestionID] = LandedAnswer{Seq: a.Seq, Payload: a.Payload, ClientTime: a.ClientTime}
		}
	}
	ms.settled = &Settlement{
		Answers:             answers,
		EffectiveAnswerTime: ms.used(end, cfg),
		TotalPauseTime:      ms.pauseTotal(end),
		Warnings:            ms.warnings,
		Violation:           violation,
		EndAt:               end,
		Reason:              reason,
	}
	ms.status = StatusEnded
}

func (m *model) tick(now int64) error {
	if now < m.now {
		m.note = "clock regression"
		return ErrClockRegression
	}
	m.now = now
	return nil
}

func (m *model) touch(id string) (*modelSession, error) {
	ms := m.sessions[id]
	if ms == nil {
		m.note = "session not found"
		return nil, ErrSessionNotFound
	}
	if ms.status != StatusEnded {
		if t, reason, ok := ms.autoEnd(&m.cfg); ok && m.now >= t {
			m.note = fmt.Sprintf("lazy auto-end at %d (%v)", t, reason)
			ms.settle(t, reason, false, &m.cfg)
		}
	}
	return ms, nil
}

func (m *model) startSession(id string, now, deadline int64) (int64, error) {
	if id == "" || deadline <= now {
		m.note = "invalid param"
		return 0, ErrInvalidParam
	}
	if err := m.tick(now); err != nil {
		return 0, err
	}
	if m.sessions[id] != nil {
		m.note = "session exists"
		return 0, ErrSessionExists
	}
	m.sessions[id] = &modelSession{
		status:      StatusActive,
		start:       now,
		budget:      m.cfg.AnswerBudget,
		deadline:    deadline,
		activeStart: now,
		pauseStart:  -1,
		generation:  1,
		waterline:   -1,
		maxSeenSeq:  -1,
	}
	m.note = "session started"
	return 1, nil
}

func (m *model) submitAnswer(id string, now int64, a Answer) error {
	if id == "" || a.QuestionID == "" || a.Seq < 0 || a.Generation <= 0 {
		m.note = "invalid param"
		return ErrInvalidParam
	}
	if err := m.tick(now); err != nil {
		return err
	}
	ms, err := m.touch(id)
	if err != nil {
		return err
	}
	if ms.status == StatusEnded {
		m.note = "session ended"
		return ErrSessionEnded
	}
	if a.Generation != ms.generation {
		m.note = fmt.Sprintf("stale generation %d != %d", a.Generation, ms.generation)
		return ErrStaleGeneration
	}
	switch ms.status {
	case StatusPaused:
		if a.Seq >= ms.waterline {
			m.note = fmt.Sprintf("paused: seq %d >= waterline %d", a.Seq, ms.waterline)
			return ErrAnswerWhilePaused
		}
	case StatusLocked:
		m.note = "locked"
		return ErrAnswerWhileLocked
	}
	cur, ok := int64(0), false
	for _, x := range ms.answers {
		if x.QuestionID == a.QuestionID && (!ok || x.Seq > cur) {
			cur, ok = x.Seq, true
		}
	}
	if ok {
		if a.Seq < cur {
			m.note = fmt.Sprintf("late: seq %d < landed %d", a.Seq, cur)
			return ErrLateAnswer
		}
		if a.Seq == cur {
			m.note = fmt.Sprintf("duplicate: seq %d == landed %d", a.Seq, cur)
			return ErrDuplicateAnswer
		}
	}
	ms.answers = append(ms.answers, a)
	if a.Seq > ms.maxSeenSeq {
		ms.maxSeenSeq = a.Seq
	}
	m.note = fmt.Sprintf("landed q=%s seq=%d", a.QuestionID, a.Seq)
	return nil
}

func (m *model) recordEvent(id string, now int64, kind EventKind, clientTime int64) error {
	if id == "" || (kind != EventLeavePage && kind != EventSwitchWindow) {
		m.note = "invalid param"
		return ErrInvalidParam
	}
	if err := m.tick(now); err != nil {
		return err
	}
	ms, err := m.touch(id)
	if err != nil {
		return err
	}
	if ms.status == StatusEnded {
		m.note = "session ended"
		return ErrSessionEnded
	}
	ms.events = append(ms.events, now)
	count := 0
	for _, e := range ms.events {
		if e > now-m.cfg.Window && e <= now {
			count++
		}
	}
	m.note = fmt.Sprintf("window count=%d", count)
	if count >= m.cfg.WarnThreshold {
		ms.warnings++
		m.note += fmt.Sprintf(", warn#%d", ms.warnings)
	}
	if count >= m.cfg.LockThreshold && ms.status != StatusLocked {
		if ms.status == StatusPaused {
			ms.pauseIntervals = append(ms.pauseIntervals, [2]int64{ms.pauseStart, now})
			ms.pauseStart = -1
			ms.activeStart = now
		}
		ms.status = StatusLocked
		m.note += ", locked"
	}
	if ms.warnings >= m.cfg.WarnLimit {
		ms.settle(now, EndReasonViolation, true, &m.cfg)
		m.note += ", violation end"
	}
	return nil
}

func (m *model) disconnect(id string, now int64) (string, error) {
	if id == "" {
		m.note = "invalid param"
		return "", ErrInvalidParam
	}
	if err := m.tick(now); err != nil {
		return "", err
	}
	ms, err := m.touch(id)
	if err != nil {
		return "", err
	}
	if ms.status == StatusEnded {
		m.note = "session ended"
		return "", ErrSessionEnded
	}
	if ms.status != StatusActive {
		m.note = "not active"
		return "", ErrNotActive
	}
	m.tokenSeq++
	token := fmt.Sprintf("resume-%s-%d", id, m.tokenSeq)
	ms.activeIntervals = append(ms.activeIntervals, [2]int64{ms.activeStart, now})
	ms.activeStart = -1
	ms.status = StatusPaused
	ms.pauseStart = now
	ms.token = token
	ms.waterline = ms.maxSeenSeq
	m.note = fmt.Sprintf("paused, token=%s waterline=%d", token, ms.waterline)
	return token, nil
}

func (m *model) resume(id, token string, now int64) (int64, error) {
	if id == "" || token == "" {
		m.note = "invalid param"
		return 0, ErrInvalidParam
	}
	if err := m.tick(now); err != nil {
		return 0, err
	}
	ms, err := m.touch(id)
	if err != nil {
		return 0, err
	}
	if ms.status == StatusEnded {
		m.note = "session ended"
		return 0, ErrSessionEnded
	}
	if token != ms.token {
		m.note = "invalid token"
		return 0, ErrInvalidToken
	}
	if ms.status != StatusPaused {
		m.note = "not paused"
		return 0, ErrNotPaused
	}
	ms.pauseIntervals = append(ms.pauseIntervals, [2]int64{ms.pauseStart, now})
	ms.pauseStart = -1
	ms.status = StatusActive
	ms.activeStart = now
	ms.token = ""
	ms.waterline = -1
	ms.generation++
	m.note = fmt.Sprintf("resumed, generation=%d", ms.generation)
	return ms.generation, nil
}

func (m *model) submit(id string, now int64) (Settlement, error) {
	if id == "" {
		m.note = "invalid param"
		return Settlement{}, ErrInvalidParam
	}
	if err := m.tick(now); err != nil {
		return Settlement{}, err
	}
	ms, err := m.touch(id)
	if err != nil {
		return Settlement{}, err
	}
	if ms.settled != nil {
		m.note = "already settled"
		return Settlement{}, ErrAlreadySettled
	}
	ms.settle(now, EndReasonSubmitted, false, &m.cfg)
	m.note = "submitted"
	return *ms.settled, nil
}

func (m *model) extend(id string, now, extra int64) error {
	if id == "" || extra <= 0 {
		m.note = "invalid param"
		return ErrInvalidParam
	}
	if err := m.tick(now); err != nil {
		return err
	}
	ms, err := m.touch(id)
	if err != nil {
		return err
	}
	if ms.status == StatusEnded {
		m.note = "session ended"
		return ErrSessionEnded
	}
	ms.budget += extra
	m.note = fmt.Sprintf("budget extended to %d", ms.budget)
	return nil
}

func (m *model) unlock(id string, now int64) error {
	if id == "" {
		m.note = "invalid param"
		return ErrInvalidParam
	}
	if err := m.tick(now); err != nil {
		return err
	}
	ms, err := m.touch(id)
	if err != nil {
		return err
	}
	if ms.status == StatusEnded {
		m.note = "session ended"
		return ErrSessionEnded
	}
	if ms.status != StatusLocked {
		m.note = "not locked"
		return ErrNotLocked
	}
	ms.status = StatusActive
	m.note = "unlocked"
	return nil
}

func (m *model) settlementOf(id string, now int64) (Settlement, bool, error) {
	if id == "" {
		m.note = "invalid param"
		return Settlement{}, false, ErrInvalidParam
	}
	if err := m.tick(now); err != nil {
		return Settlement{}, false, err
	}
	ms, err := m.touch(id)
	if err != nil {
		return Settlement{}, false, err
	}
	if ms.settled == nil {
		m.note = "ongoing"
		return Settlement{}, false, nil
	}
	m.note = "settled"
	return *ms.settled, true, nil
}

var errCatalog = []error{
	ErrInvalidParam, ErrClockRegression, ErrSessionNotFound, ErrSessionExists,
	ErrSessionEnded, ErrAlreadySettled, ErrInvalidToken, ErrStaleGeneration,
	ErrAnswerWhilePaused, ErrAnswerWhileLocked, ErrNotPaused, ErrNotLocked,
	ErrNotActive, ErrLateAnswer, ErrDuplicateAnswer,
}

func errCode(err error) string {
	if err == nil {
		return "ok"
	}
	for _, e := range errCatalog {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return "unknown: " + err.Error()
}

// 与朴素模型对照随机操作序列：逐步比对错误类别与结算结果，
// 并打印每步输入、输出与判定依据。
func TestRandomizedAgainstModel(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runDifferential(t, seed, 400)
		})
	}
}

type sessionShadow struct {
	started bool
	gen     int64
	tokens  []string
	issued  int64 // 客户端已发出的最大序号
}

func runDifferential(t *testing.T, seed int64, steps int) {
	rnd := rand.New(rand.NewSource(seed))
	cfg := Config{
		AnswerBudget:   20 + rnd.Int63n(80),
		PauseBudget:    rnd.Int63n(15),
		SinglePauseMax: 5 + rnd.Int63n(20),
		Window:         3 + rnd.Int63n(15),
		WarnThreshold:  1 + rnd.Intn(3),
		WarnLimit:      2 + rnd.Intn(4),
	}
	cfg.LockThreshold = cfg.WarnThreshold + rnd.Intn(3)
	t.Logf("config=%+v", cfg)

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	mdl := newModel(cfg)

	ids := []string{"s0", "s1", "s2"}
	shadow := map[string]*sessionShadow{}
	for _, id := range ids {
		shadow[id] = &sessionShadow{}
	}

	gnow := int64(0)
	advanceClock := func(now int64, code string) {
		// 参数非法与时钟回退不推进时钟，其余操作（即使被拒绝）推进引擎时钟。
		if code != errCode(ErrInvalidParam) && code != errCode(ErrClockRegression) && now > gnow {
			gnow = now
		}
	}

	for i := 0; i < steps; i++ {
		now := gnow + rnd.Int63n(4)
		if gnow > 0 && rnd.Intn(50) == 0 {
			now = gnow - 1 // 时钟回退探针
		}
		id := ids[rnd.Intn(len(ids))]
		sh := shadow[id]
		op := rnd.Intn(100)

		switch {
		case op < 8: // 开会话
			deadline := now + 1 + rnd.Int63n(120)
			eGen, eErr := eng.StartSession(id, now, deadline)
			mGen, mErr := mdl.startSession(id, now, deadline)
			code := compareErr(t, i, "start", eErr, mErr)
			if eErr == nil && eGen != mGen {
				t.Fatalf("step %d: generation %d != %d", i, eGen, mGen)
			}
			if eErr == nil {
				sh.started, sh.gen, sh.tokens, sh.issued = true, eGen, nil, -1
			}
			t.Logf("step %03d now=%d start id=%s deadline=%d -> %s gen=%d | %s", i, now, id, deadline, code, eGen, mdl.note)
			advanceClock(now, code)

		case op < 40: // 作答
			gen := sh.gen
			switch rnd.Intn(10) {
			case 0:
				gen = sh.gen - 1 // 旧代次
			case 1:
				gen = sh.gen + 1 // 未来代次
			}
			if gen <= 0 {
				gen = sh.gen
			}
			var seq int64
			switch rnd.Intn(10) {
			case 0, 1:
				seq = sh.issued // 重复
			case 2, 3:
				if sh.issued > 0 {
					seq = rnd.Int63n(sh.issued) // 迟到
				}
			default:
				sh.issued += 1 + rnd.Int63n(2) // 新序号，允许跳号
				seq = sh.issued
			}
			a := Answer{
				QuestionID: fmt.Sprintf("q%d", rnd.Intn(3)),
				Seq:        seq,
				Generation: gen,
				Payload:    fmt.Sprintf("v%d", i),
				ClientTime: now - rnd.Int63n(3),
			}
			eErr := eng.SubmitAnswer(id, now, a)
			mErr := mdl.submitAnswer(id, now, a)
			code := compareErr(t, i, "answer", eErr, mErr)
			t.Logf("step %03d now=%d answer id=%s q=%s seq=%d gen=%d -> %s | %s",
				i, now, id, a.QuestionID, a.Seq, a.Generation, code, mdl.note)
			advanceClock(now, code)

		case op < 55: // 异常事件
			kind := EventLeavePage
			if rnd.Intn(2) == 0 {
				kind = EventSwitchWindow
			}
			eErr := eng.RecordEvent(id, now, kind, now-1)
			mErr := mdl.recordEvent(id, now, kind, now-1)
			code := compareErr(t, i, "event", eErr, mErr)
			t.Logf("step %03d now=%d event id=%s kind=%d -> %s | %s", i, now, id, kind, code, mdl.note)
			advanceClock(now, code)

		case op < 65: // 断线
			eTok, eErr := eng.Disconnect(id, now)
			mTok, mErr := mdl.disconnect(id, now)
			code := compareErr(t, i, "disconnect", eErr, mErr)
			if eErr == nil {
				if eTok != mTok {
					t.Fatalf("step %d: token %q != %q", i, eTok, mTok)
				}
				sh.tokens = append(sh.tokens, eTok)
			}
			t.Logf("step %03d now=%d disconnect id=%s -> %s tok=%q | %s", i, now, id, code, eTok, mdl.note)
			advanceClock(now, code)

		case op < 75: // 续考，偶尔使用旧凭证
			token := "bogus"
			if n := len(sh.tokens); n > 0 {
				if rnd.Intn(4) == 0 {
					token = sh.tokens[rnd.Intn(n)] // 可能是旧凭证
				} else {
					token = sh.tokens[n-1]
				}
			}
			eGen, eErr := eng.Resume(id, token, now)
			mGen, mErr := mdl.resume(id, token, now)
			code := compareErr(t, i, "resume", eErr, mErr)
			if eErr == nil {
				if eGen != mGen {
					t.Fatalf("step %d: generation %d != %d", i, eGen, mGen)
				}
				sh.gen = eGen
			}
			t.Logf("step %03d now=%d resume id=%s tok=%q -> %s gen=%d | %s", i, now, id, token, code, eGen, mdl.note)
			advanceClock(now, code)

		case op < 80: // 主动提交
			eSet, eErr := eng.Submit(id, now)
			mSet, mErr := mdl.submit(id, now)
			code := compareErr(t, i, "submit", eErr, mErr)
			if eErr == nil && !reflect.DeepEqual(eSet, mSet) {
				t.Fatalf("step %d: settlement mismatch\nengine=%+v\nmodel =%+v", i, eSet, mSet)
			}
			t.Logf("step %03d now=%d submit id=%s -> %s | %s", i, now, id, code, mdl.note)
			advanceClock(now, code)

		case op < 85: // 延长预算
			extra := int64(1 + rnd.Intn(30))
			eErr := eng.Extend(id, now, extra)
			mErr := mdl.extend(id, now, extra)
			code := compareErr(t, i, "extend", eErr, mErr)
			t.Logf("step %03d now=%d extend id=%s extra=%d -> %s | %s", i, now, id, extra, code, mdl.note)
			advanceClock(now, code)

		case op < 90: // 解除锁定
			eErr := eng.Unlock(id, now)
			mErr := mdl.unlock(id, now)
			code := compareErr(t, i, "unlock", eErr, mErr)
			t.Logf("step %03d now=%d unlock id=%s -> %s | %s", i, now, id, code, mdl.note)
			advanceClock(now, code)

		default: // 查询结算
			eSet, eOK, eErr := eng.SettlementOf(id, now)
			mSet, mOK, mErr := mdl.settlementOf(id, now)
			code := compareErr(t, i, "query", eErr, mErr)
			if eErr == nil {
				if eOK != mOK {
					t.Fatalf("step %d: settled flag %v != %v", i, eOK, mOK)
				}
				if eOK && !reflect.DeepEqual(eSet, mSet) {
					t.Fatalf("step %d: settlement mismatch\nengine=%+v\nmodel =%+v", i, eSet, mSet)
				}
			}
			t.Logf("step %03d now=%d query id=%s -> %s ok=%v | %s", i, now, id, code, eOK, mdl.note)
			advanceClock(now, code)
		}
	}

	// 收尾：全部会话在同一时刻查询结算，完整比对。
	for _, id := range ids {
		eSet, eOK, eErr := eng.SettlementOf(id, gnow)
		mSet, mOK, mErr := mdl.settlementOf(id, gnow)
		if errCode(eErr) != errCode(mErr) || eOK != mOK {
			t.Fatalf("final %s: (%v,%v) vs (%v,%v)", id, eErr, eOK, mErr, mOK)
		}
		if eOK && !reflect.DeepEqual(eSet, mSet) {
			t.Fatalf("final %s: settlement mismatch\nengine=%+v\nmodel =%+v", id, eSet, mSet)
		}
	}
}

func compareErr(t *testing.T, step int, op string, eErr, mErr error) string {
	t.Helper()
	ec, mc := errCode(eErr), errCode(mErr)
	if ec != mc {
		t.Fatalf("step %d op=%s: engine=%q model=%q", step, op, ec, mc)
	}
	return ec
}
