package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// step 记录随机序列中的一步，供日志逐行复现。
type step struct {
	kind string
	at   int64
	args string
	engR string
	refR string
	why  string
}

func codeOf(err error) (Code, string) {
	if err == nil || (reflect.ValueOf(err).Kind() == reflect.Ptr && reflect.ValueOf(err).IsNil()) {
		return 0, SubNone
	}
	if ee, ok := err.(*Error); ok && ee != nil {
		return ee.Code, ee.SubCode
	}
	return -1, err.Error()
}

func isNilErr(err error) bool {
	c, _ := codeOf(err)
	return c == 0
}

func classifyWhy(c Code, sub string) string {
	switch c {
	case 0:
		return "accepted"
	case ErrInvalidParam:
		return "非法参数：拒绝且不推进时钟"
	case ErrClockRegression:
		return "时钟回退：拒绝且不推进时钟"
	case ErrSessionNotFound:
		return "会话不存在"
	case ErrSessionEnded:
		return "会话已结束/已结算（结算不可变）"
	case ErrCredential:
		return "凭证无效或代次过期：" + sub
	case ErrStateNotAllowed:
		return "当前状态不允许该操作（暂停/锁定/非暂停续考）"
	case ErrAnswerLateOrDuplicate:
		return "作答" + sub + "：序号水位/重复序号判定"
	default:
		return "unknown"
	}
}

func snapEqual(a, b Snapshot) (string, bool) {
	keys := []string{
		"State", "UsedActive", "TotalPaused", "Warnings", "Locked",
		"Violation", "Settled", "EndAt", "EndReason", "LastSeq",
		"WindowCount", "Generation",
	}
	av := []any{a.State, a.UsedActive, a.TotalPaused, a.Warnings, a.Locked,
		a.Violation, a.Settled, a.EndAt, a.EndReason, a.LastSeq, a.WindowCount, a.Generation}
	bv := []any{b.State, b.UsedActive, b.TotalPaused, b.Warnings, b.Locked,
		b.Violation, b.Settled, b.EndAt, b.EndReason, b.LastSeq, b.WindowCount, b.Generation}
	for i := range keys {
		if av[i] != bv[i] {
			return fmt.Sprintf("%s: engine=%v ref=%v", keys[i], av[i], bv[i]), false
		}
	}
	if len(a.Answers) != len(b.Answers) {
		return "answers len differ", false
	}
	for q, x := range a.Answers {
		y, ok := b.Answers[q]
		if !ok || x != y {
			return fmt.Sprintf("answer[%s]: engine=%+v ref=%+v", q, x, y), false
		}
	}
	return "", true
}

type fuzzRunner struct {
	t      *testing.T
	rng    *rand.Rand
	eng    *Engine
	ref    *naiveModel
	log    []step
	now    int64
	cfg    Config
	gen    int64
	cred   string
	nextQ  int
	seqGen int64
}

func runFuzz(t *testing.T, seed int64, ops int) {
	rng := rand.New(rand.NewSource(seed))
	cfg := Config{
		Budget:         30 + rng.Int63n(40),
		Deadline:       0,
		PauseBudget:    8 + rng.Int63n(20),
		MaxSinglePause: 5 + rng.Int63n(15),
		WindowLen:      4 + rng.Int63n(8),
		WarnThreshold:  2,
		LockThreshold:  3,
		MaxWarnings:    2 + rng.Intn(3),
	}
	now := int64(rng.Intn(5))
	cfg.Deadline = now + 80 + rng.Int63n(80)

	r := &fuzzRunner{
		t: t, rng: rng, eng: NewEngine(),
		cfg: cfg, now: now, gen: 1, seqGen: 0,
	}
	if err := r.eng.CreateSession("s", now, cfg); err != nil {
		t.Fatal(err)
	}
	r.ref = newNaiveModel(cfg, now)
	r.record("create", now, fmt.Sprintf("cfg=%+v", cfg), "ok", "ok", "会话创建")

	for i := 0; i < ops; i++ {
		// 时刻：大概率前进、小概率同刻；不产生回退（回退路径由专门用例覆盖）。
		switch rng.Intn(4) {
		case 0:
		default:
			now += int64(rng.Intn(6))
		}
		r.now = now
		r.oneOp(i)
	}
}

func (r *fuzzRunner) record(kind string, at int64, args, er, rr, why string) {
	r.log = append(r.log, step{kind: kind, at: at, args: args, engR: er, refR: rr, why: why})
}

func (r *fuzzRunner) fail(msg string) {
	var b strings.Builder
	b.WriteString(msg + "\n逐步日志（输入 -> 引擎/朴素模型输出 -> 判定依据）:\n")
	for i, s := range r.log {
		fmt.Fprintf(&b, "%03d t=%d %-10s %s | eng=%s ref=%s | %s\n",
			i, s.at, s.kind, s.args, s.engR, s.refR, s.why)
	}
	r.t.Fatal(b.String())
}

func (r *fuzzRunner) qid() string {
	r.nextQ++
	return fmt.Sprintf("q%d", 1+r.rng.Intn(6))
}

func (r *fuzzRunner) checkSnap() {
	es, eerr := r.eng.Snapshot("s", r.now)
	if !isNilErr(eerr) {
		r.fail("engine snapshot error: " + eerr.Error())
	}
	rs := r.ref.snapshot(r.now)
	if why, ok := snapEqual(es, rs); !ok {
		r.fail("snapshot mismatch: " + why)
	}
}

func (r *fuzzRunner) oneOp(i int) {
	const (
		opAnswer = iota
		opDisconnect
		opResume
		opEvent
		opUnlock
		opSubmit
		opExtend
		opBadCred
		opOldGen
		opDup
		opLate
		opSnap
	)
	op := r.rng.Intn(12)
	now := r.now
	switch op {
	case opAnswer:
		r.seqGen += 1 + int64(r.rng.Intn(3))
		a := Answer{Question: r.qid(), Seq: r.seqGen, Text: fmt.Sprintf("v%d", r.seqGen)}
		gen := r.gen
		eerr := r.eng.SubmitAnswer("s", now, gen, a)
		rerr := r.ref.answer(now, gen, a)
		r.compare("answer", fmt.Sprintf("gen=%d %+v", gen, a), eerr, rerr)
	case opDisconnect:
		c1, eerr := r.eng.Disconnect("s", now)
		c2, rerr := r.ref.disconnect(now)
		if isNilErr(eerr) {
			r.cred = c1
		}
		r.compare2("disconnect", "", eerr, rerr, c1, c2)
	case opResume:
		cred := r.cred
		if cred == "" {
			cred = "cred-none"
		}
		g1, eerr := r.eng.Resume("s", now, cred)
		g2, rerr := r.ref.resume(now, cred)
		if isNilErr(eerr) {
			r.gen = g1
			r.cred = ""
		}
		r.compare2("resume", "cred="+cred, eerr, rerr,
			fmt.Sprintf("gen=%d", g1), fmt.Sprintf("gen=%d", g2))
	case opEvent:
		kind := []string{"blur", "switch", "leave"}[r.rng.Intn(3)]
		eerr := r.eng.RecordEvent("s", now, kind)
		rerr := r.ref.event(now, int64(i), kind)
		r.compare("event", kind, eerr, rerr)
	case opUnlock:
		eerr := r.eng.Unlock("s", now)
		rerr := r.ref.unlock(now)
		r.compare("unlock", "", eerr, rerr)
	case opSubmit:
		eerr := r.eng.Submit("s", now)
		rerr := r.ref.submit(now)
		r.compare("submit", "", eerr, rerr)
	case opExtend:
		d := int64(1 + r.rng.Intn(15))
		eerr := r.eng.Extend("s", now, d)
		rerr := r.ref.extend(now, d)
		r.compare("extend", fmt.Sprintf("+%d", d), eerr, rerr)
	case opBadCred:
		bad := "cred-bogus"
		_, eerr := r.eng.Resume("s", now, bad)
		_, rerr := r.ref.resume(now, bad)
		r.compare("resume-badcred", bad, eerr, rerr)
	case opOldGen:
		oldgen := r.gen - 1
		if oldgen < 1 {
			oldgen = 1
		}
		a := Answer{Question: r.qid(), Seq: r.seqGen + 50, Text: "oldgen"}
		eerr := r.eng.SubmitAnswer("s", now, oldgen, a)
		rerr := r.ref.answer(now, oldgen, a)
		r.compare("answer-oldgen", fmt.Sprintf("gen=%d seq=%d", oldgen, a.Seq), eerr, rerr)
	case opDup:
		if r.seqGen <= 0 {
			return
		}
		seq := int64(1 + r.rng.Int63n(r.seqGen))
		a := Answer{Question: fmt.Sprintf("q%d", 1+r.rng.Intn(6)), Seq: seq, Text: "dup?"}
		eerr := r.eng.SubmitAnswer("s", now, r.gen, a)
		rerr := r.ref.answer(now, r.gen, a)
		r.compare("answer-dup?", fmt.Sprintf("seq=%d", seq), eerr, rerr)
	case opLate:
		if r.seqGen <= 1 {
			return
		}
		seq := r.seqGen - 1
		a := Answer{Question: r.qid(), Seq: seq, Text: "late"}
		eerr := r.eng.SubmitAnswer("s", now, r.gen, a)
		rerr := r.ref.answer(now, r.gen, a)
		r.compare("answer-late?", fmt.Sprintf("seq=%d", seq), eerr, rerr)
	case opSnap:
		r.checkSnap()
		r.record("snapshot", now, "", "match", "match", "逐项快照对照")
	}
}

func (r *fuzzRunner) compare(kind, args string, eerr, rerr error) {
	ec, es := codeOf(eerr)
	rc, rs := codeOf(rerr)
	er, rr := "ok", "ok"
	if !isNilErr(eerr) {
		er = fmt.Sprintf("err(%d,%s)", ec, es)
	}
	if !isNilErr(rerr) {
		rr = fmt.Sprintf("err(%d,%s)", rc, rs)
	}
	if ec != rc || es != rs {
		r.record(kind, r.now, args, er, rr, "错误分类不一致")
		r.fail(fmt.Sprintf("error mismatch on %s: engine=%v ref=%v", kind, eerr, rerr))
	}
	r.record(kind, r.now, args, er, rr, classifyWhy(ec, es))
	if isNilErr(eerr) {
		r.checkSnap()
	}
}

func (r *fuzzRunner) compare2(kind, args string, eerr, rerr error, ev, rv string) {
	ec, es := codeOf(eerr)
	rc, rs := codeOf(rerr)
	if ec != rc || es != rs || (eerr == nil && ev != rv) {
		r.fail(fmt.Sprintf("mismatch %s: engine=(%s,%v) ref=(%s,%v)", kind, ev, eerr, rv, rerr))
	}
	er, rr := "ok:"+ev, "ok:"+rv
	if !isNilErr(eerr) {
		er, rr = eerr.Error(), rerr.Error()
	}
	r.record(kind, r.now, args, er, rr, classifyWhy(ec, es))
	if isNilErr(eerr) {
		r.checkSnap()
	}
}

func TestDifferentialRandom(t *testing.T) {
	for seed := int64(1); seed <= 400; seed++ {
		runFuzz(t, seed, 120)
	}
}

// TestDifferentialDemoLog 固定种子并在 -v 下打印逐步输入、输出与判定依据。
func TestDifferentialDemoLog(t *testing.T) {
	if !testing.Verbose() {
		t.Skip("使用 -v 查看完整逐步日志")
	}
	rng := rand.New(rand.NewSource(42))
	cfg := Config{
		Budget: 30, Deadline: 120, PauseBudget: 10, MaxSinglePause: 8,
		WindowLen: 6, WarnThreshold: 2, LockThreshold: 3, MaxWarnings: 3,
	}
	e := NewEngine()
	if err := e.CreateSession("s", 0, cfg); err != nil {
		t.Fatal(err)
	}
	ref := newNaiveModel(cfg, 0)
	t.Logf("create cfg=%+v", cfg)
	var gen int64 = 1
	var cred string
	now := int64(0)
	emit := func(desc string, err error) {
		c, sub := codeOf(err)
		t.Logf("t=%-3d %-28s -> code=%d sub=%-17s 依据=%s", now, desc, c, sub, classifyWhy(c, sub))
	}
	for i := 0; i < 40; i++ {
		now += int64(rng.Intn(4))
		switch rng.Intn(8) {
		case 0:
			c1, err := e.Disconnect("s", now)
			_, rerr := ref.disconnect(now)
			if codeEq(err, rerr) && isNilErr(err) {
				cred = c1
			}
			emit("disconnect", err)
		case 1:
			use := cred
			if use == "" {
				use = "none"
			}
			g, err := e.Resume("s", now, use)
			_, rerr := ref.resume(now, use)
			if codeEq(err, rerr) && isNilErr(err) {
				gen, cred = g, ""
			}
			emit("resume "+use, err)
		case 2:
			a := Answer{Question: fmt.Sprintf("q%d", 1+rng.Intn(4)), Seq: int64(1 + rng.Intn(8)), Text: "x"}
			err := e.SubmitAnswer("s", now, gen, a)
			rerr := ref.answer(now, gen, a)
			emit(fmt.Sprintf("answer seq=%d q=%s gen=%d", a.Seq, a.Question, gen), firstErr(err, rerr))
		case 3:
			kind := []string{"blur", "switch", "leave"}[rng.Intn(3)]
			err := e.RecordEvent("s", now, kind)
			rerr := ref.event(now, int64(i), kind)
			emit("event "+kind, firstErr(err, rerr))
		case 4:
			err := e.Unlock("s", now)
			rerr := ref.unlock(now)
			emit("unlock", firstErr(err, rerr))
		case 5:
			err := e.Submit("s", now)
			rerr := ref.submit(now)
			emit("submit", firstErr(err, rerr))
		default:
			d := int64(1 + rng.Intn(10))
			err := e.Extend("s", now, d)
			rerr := ref.extend(now, d)
			emit(fmt.Sprintf("extend +%d", d), firstErr(err, rerr))
		}
		es, _ := e.Snapshot("s", now)
		rs := ref.snapshot(now)
		if why, ok := snapEqual(es, rs); !ok {
			t.Fatalf("t=%d snapshot mismatch: %s", now, why)
		}
	}
}

func codeEq(a, b error) bool {
	ac, as := codeOf(a)
	bc, bs := codeOf(b)
	return ac == bc && as == bs
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if !isNilErr(e) {
			return e
		}
	}
	return nil
}
