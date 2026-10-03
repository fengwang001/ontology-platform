package frequency

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type refRecord struct {
	time     int64
	campaign string
	creative string
}

type refUser struct {
	records      []refRecord
	lastTime     int64
	lastCreative string
	run          int
	day          int64
	daily        map[string]int64
}

type refState map[string]*refUser

func newRefState() refState {
	return make(refState)
}

func (s refState) user(name string) *refUser {
	u := s[name]
	if u == nil {
		u = &refUser{day: -1, daily: make(map[string]int64)}
		s[name] = u
	}
	return u
}

func refEval(u *refUser, req Request, cfg refConfig) Decision {
	evidence := Evidence{
		LastTime:     u.lastTime,
		LastCreative: u.lastCreative,
		CreativeRun:  u.run,
		Day:          req.Now / 86400,
	}

	if u.run > 0 && req.Now < u.lastTime {
		return Decision{Reason: ReasonClockRollback, Evidence: evidence}
	}

	if u.run > 0 && u.lastCreative == req.Creative {
		evidence.RequiredGap = cfg.g0 * int64(min(u.run, 3))
		evidence.Elapsed = req.Now - u.lastTime
		if evidence.Elapsed < evidence.RequiredGap {
			return Decision{Reason: ReasonCreativeInterval, Evidence: evidence}
		}
	}

	var windowCount int64
	for _, record := range u.records {
		if record.time+cfg.wg > req.Now {
			windowCount++
		}
	}
	evidence.WindowCount = windowCount
	if windowCount >= cfg.cg {
		return Decision{Reason: ReasonGlobalWindow, Evidence: evidence}
	}

	day := req.Now / 86400
	if u.day != day {
		u.day = day
		u.daily = make(map[string]int64)
	}
	dayCount := u.daily[req.Campaign]
	effective := cfg.cc - windowCount/cfg.k
	if effective < 1 {
		effective = 1
	}
	evidence.CampaignDayCount = dayCount
	evidence.EffectiveDaily = effective
	if dayCount >= effective {
		return Decision{Reason: ReasonCampaignDaily, Evidence: evidence}
	}

	u.records = append(u.records, refRecord{time: req.Now, campaign: req.Campaign, creative: req.Creative})
	if u.run == 0 || u.lastCreative != req.Creative {
		u.run = 1
	} else {
		u.run++
	}
	u.lastTime = req.Now
	u.lastCreative = req.Creative
	u.daily[req.Campaign]++
	return Decision{Allowed: true, Evidence: evidence}
}

type refConfig struct {
	wg, cg, k, cc, g0 int64
}

func refOne(s refState, user, campaign, creative string, now int64, cfg refConfig) Decision {
	if reason := validateRequest(user, campaign, creative, now); reason != ReasonOK {
		return Decision{Reason: reason}
	}
	return refEval(s.user(user), Request{Campaign: campaign, Creative: creative, Now: now}, cfg)
}

func refPeek(s refState, user, campaign, creative string, now int64, cfg refConfig) Decision {
	if reason := validateRequest(user, campaign, creative, now); reason != ReasonOK {
		return Decision{Reason: reason}
	}
	copied := copyRefUser(s.user(user))
	return refEval(&copied, Request{Campaign: campaign, Creative: creative, Now: now}, cfg)
}

func copyRefUser(u *refUser) refUser {
	records := make([]refRecord, len(u.records))
	copy(records, u.records)
	daily := make(map[string]int64, len(u.daily))
	for campaign, count := range u.daily {
		daily[campaign] = count
	}
	return refUser{
		records:      records,
		lastTime:     u.lastTime,
		lastCreative: u.lastCreative,
		run:          u.run,
		day:          u.day,
		daily:        daily,
	}
}

func refBatch(s refState, user string, requests []Request, cfg refConfig) BatchResult {
	if user == "" || len(requests) < 1 || len(requests) > 1000 {
		return BatchResult{Index: -1, Reason: ReasonInvalidArgument}
	}
	for i, req := range requests {
		if reason := validateRequest(user, req.Campaign, req.Creative, req.Now); reason != ReasonOK {
			return BatchResult{Index: i, Reason: reason}
		}
	}

	copied := copyRefUser(s.user(user))
	for i, req := range requests {
		decision := refEval(&copied, req, cfg)
		if !decision.Allowed {
			return BatchResult{Index: i, Reason: decision.Reason, Evidence: decision.Evidence}
		}
	}
	*s.user(user) = copied
	return BatchResult{Allowed: true, Index: -1}
}

type randomOp struct {
	kind     int
	user     string
	campaign string
	creative string
	now      int64
	requests []Request
}

func TestRandomSequencesAgainstNaiveSimulation(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(1263))

	for seq := 0; seq < sequences; seq++ {
		cfg := refConfig{
			wg: int64(rng.Intn(50) + 1),
			cg: int64(rng.Intn(8) + 1),
			k:  int64(rng.Intn(5) + 1),
			cc: int64(rng.Intn(6) + 1),
			g0: int64(rng.Intn(8) + 1),
		}
		c := mustController(t, cfg.wg, cfg.cg, cfg.k, cfg.cc, cfg.g0)
		replay := mustController(t, cfg.wg, cfg.cg, cfg.k, cfg.cc, cfg.g0)
		reference := newRefState()
		var log strings.Builder
		fmt.Fprintf(&log, "seq=%d cfg=(%d,%d,%d,%d,%d)", seq, cfg.wg, cfg.cg, cfg.k, cfg.cc, cfg.g0)

		ops := 20 + rng.Intn(21)
		for opIndex := 0; opIndex < ops; opIndex++ {
			op := makeRandomOp(rng, opIndex)
			got, want := applyOp(c, op, cfg), applyRefOp(reference, op, cfg)
			again := applyOp(replay, op, cfg)
			assertDecisionPair(t, seq, opIndex, got, want, op)
			assertDecisionPair(t, seq, opIndex, again, want, op)
			appendOpLog(&log, op, got)

			for _, name := range []string{"u0", "u1", "u2", "u3"} {
				assertStateMatchesReference(t, c, reference, name, cfg)
			}
		}
		t.Logf("%s", log.String())
	}
}

func makeRandomOp(rng *rand.Rand, index int) randomOp {
	op := randomOp{
		kind:     rng.Intn(3),
		user:     "u" + fmt.Sprint(rng.Intn(4)),
		campaign: "c" + fmt.Sprint(rng.Intn(3)),
		creative: "x" + fmt.Sprint(rng.Intn(3)),
		now:      int64(rng.Intn(90)),
	}
	if rng.Intn(10) == 0 {
		switch rng.Intn(4) {
		case 0:
			op.user = ""
		case 1:
			op.campaign = ""
		case 2:
			op.creative = ""
		case 3:
			if rng.Intn(2) == 0 {
				op.now = -1
			} else {
				op.now = 1_000_000_000_000_001
			}
		}
	}
	if op.kind == 2 {
		length := 1 + rng.Intn(5)
		if index == 0 && rng.Intn(20) == 0 {
			length = 1001
		}
		op.requests = make([]Request, length)
		for i := range op.requests {
			op.requests[i] = Request{
				Campaign: "c" + fmt.Sprint(rng.Intn(3)),
				Creative: "x" + fmt.Sprint(rng.Intn(3)),
				Now:      int64(rng.Intn(90)),
			}
		}
	}
	return op
}

func applyOp(c *Controller, op randomOp, cfg refConfig) BatchResult {
	switch op.kind {
	case 0:
		d := c.Admit(op.user, op.campaign, op.creative, op.now)
		return BatchResult{Allowed: d.Allowed, Index: -1, Reason: d.Reason, Evidence: d.Evidence}
	case 1:
		d := c.Peek(op.user, op.campaign, op.creative, op.now)
		return BatchResult{Allowed: d.Allowed, Index: -1, Reason: d.Reason, Evidence: d.Evidence}
	default:
		return c.AdmitBatch(op.user, op.requests)
	}
}

func applyRefOp(s refState, op randomOp, cfg refConfig) BatchResult {
	switch op.kind {
	case 0:
		d := refOne(s, op.user, op.campaign, op.creative, op.now, cfg)
		return BatchResult{Allowed: d.Allowed, Index: -1, Reason: d.Reason, Evidence: d.Evidence}
	case 1:
		d := refPeek(s, op.user, op.campaign, op.creative, op.now, cfg)
		return BatchResult{Allowed: d.Allowed, Index: -1, Reason: d.Reason, Evidence: d.Evidence}
	default:
		return refBatch(s, op.user, op.requests, cfg)
	}
}

func assertDecisionPair(t *testing.T, seq, opIndex int, got, want BatchResult, op randomOp) {
	t.Helper()
	if got.Allowed != want.Allowed || got.Index != want.Index || got.Reason != want.Reason || got.Evidence != want.Evidence {
		t.Fatalf("seq=%d op=%d op=%+v got=%+v want=%+v", seq, opIndex, op, got, want)
	}
}

func assertStateMatchesReference(t *testing.T, c *Controller, reference refState, name string, cfg refConfig) {
	t.Helper()
	actual := c.inspect(name)
	ru := reference[name]
	if ru == nil {
		if len(actual.Records) != 0 || actual.CreativeRun != 0 {
			t.Fatalf("user %s unexpectedly has state %+v", name, actual)
		}
		return
	}

	var active []refRecord
	for _, record := range ru.records {
		if record.time+cfg.wg > actual.LastTime {
			active = append(active, record)
		}
	}
	if len(active) != len(actual.Records) {
		t.Fatalf("user %s active length = %d, controller = %d", name, len(active), len(actual.Records))
	}
	for i, record := range active {
		if record.time != actual.Records[i].time || record.campaign != actual.Records[i].campaign || record.creative != actual.Records[i].creative {
			t.Fatalf("user %s record %d = %+v, want %+v", name, i, actual.Records[i], record)
		}
	}
	if actual.LastTime != ru.lastTime || actual.LastCreative != ru.lastCreative || actual.CreativeRun != ru.run || actual.Day != ru.day {
		t.Fatalf("user %s state = %+v, want last=(%d,%q,%d) day=%d", name, actual, ru.lastTime, ru.lastCreative, ru.run, ru.day)
	}
	for campaign, count := range ru.daily {
		if actual.Daily[campaign] != count {
			t.Fatalf("user %s daily[%s] = %d, want %d", name, campaign, actual.Daily[campaign], count)
		}
	}
}

func appendOpLog(log *strings.Builder, op randomOp, result BatchResult) {
	kind := []string{"ADMIT", "PEEK", "BATCH"}[op.kind]
	fmt.Fprintf(log, " | %s(%s,%s,%s,%d)=>%s/%d", kind, op.user, op.campaign, op.creative, op.now, result.Reason, result.Index)
	if result.Reason == ReasonOK {
		return
	}
	e := result.Evidence
	fmt.Fprintf(log, "[s=%d,elapsed=%d/%d,cg=%d,n=%d/E=%d,day=%d]",
		e.CreativeRun, e.Elapsed, e.RequiredGap, e.WindowCount, e.CampaignDayCount, e.EffectiveDaily, e.Day)
}
