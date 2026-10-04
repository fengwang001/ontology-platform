package paywall

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"ontology/meter"
)

type naiveKind uint8

const (
	naiveQuota naiveKind = iota + 1
	naiveGift
)

type naiveRecord struct {
	article string
	at      int64
	kind    naiveKind
}

type naiveToken struct {
	id       int64
	article  string
	issued   int64
	redeemed map[string]struct{}
}

type naiveModel struct {
	n, g, k  int
	m, e     int64
	maxNow   int64
	articles map[string]bool
	bound    map[string]string
	books    map[string]map[int64]map[string]naiveRecord
	subs     map[string]int64
	tokens   map[int64]*naiveToken
	gifts    map[string]map[int64]int
	next     int64
}

type opKind uint8

const (
	opRead opKind = iota
	opLogin
	opLogout
	opSubscribe
	opGift
)

type operation struct {
	kind                  opKind
	now                   int64
	device, user, article string
	until, tokenID        int64
	token                 *Token
}

func newNaive(n, g, k int, m, e int64) *naiveModel {
	return &naiveModel{
		n: n, g: g, k: k, m: m, e: e,
		articles: map[string]bool{},
		bound:    map[string]string{},
		books:    map[string]map[int64]map[string]naiveRecord{},
		subs:     map[string]int64{},
		tokens:   map[int64]*naiveToken{},
		gifts:    map[string]map[int64]int{},
	}
}

func (m *naiveModel) month(now int64) int64 { return now / m.m }

func (m *naiveModel) book(subject string, month int64) map[string]naiveRecord {
	if months, ok := m.books[subject]; ok {
		return months[month]
	}
	return nil
}

func (m *naiveModel) ensure(subject string, month int64) map[string]naiveRecord {
	months, ok := m.books[subject]
	if !ok {
		months = map[int64]map[string]naiveRecord{}
		m.books[subject] = months
	}
	current, ok := months[month]
	if !ok {
		current = map[string]naiveRecord{}
		months[month] = current
	}
	return current
}

func (m *naiveModel) subject(device string) string {
	if user, ok := m.bound[device]; ok {
		return user
	}
	return device
}

func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (m *naiveModel) read(o operation) (Result, error) {
	if !validNow(o.now) || o.device == "" || o.article == "" || (o.token != nil && o.token.id < 1) {
		return Result{}, ErrInvalidArgument
	}
	if o.now < m.maxNow {
		return Result{}, ErrClockSkew
	}
	free, ok := m.articles[o.article]
	if !ok {
		return Result{}, ErrArticleNotFound
	}
	m.maxNow = o.now

	subject := m.subject(o.device)
	if user, bound := m.bound[o.device]; bound && o.now < m.subs[user] {
		return Result{Allowed: true, Reason: Subscription}, nil
	}
	if free {
		return Result{Allowed: true, Reason: FreeArticle}, nil
	}
	month := m.month(o.now)
	if _, ok := m.book(subject, month)[o.article]; ok {
		return Result{Allowed: true, Reason: AlreadyUnlocked}, nil
	}
	if state, ok := m.validToken(subject, o.article, o.now, o.token); ok {
		state.redeemed[subject] = struct{}{}
		m.ensure(subject, month)[o.article] = naiveRecord{o.article, o.now, naiveGift}
		return Result{Allowed: true, Reason: Gift, TokenID: state.id}, nil
	}

	used := 0
	for _, record := range m.book(subject, month) {
		if record.kind == naiveQuota {
			used++
		}
	}
	if used < m.n {
		m.ensure(subject, month)[o.article] = naiveRecord{o.article, o.now, naiveQuota}
		return Result{Allowed: true, Reason: Quota}, nil
	}
	return Result{Allowed: false, Reason: Denied}, nil
}

func (m *naiveModel) validToken(subject, article string, now int64, external *Token) (*naiveToken, bool) {
	if external == nil {
		return nil, false
	}
	state, ok := m.tokens[external.id]
	if !ok || state.article != article {
		return nil, false
	}
	_, already := state.redeemed[subject]
	if !already && len(state.redeemed) >= m.k {
		return nil, false
	}
	if now < state.issued || now >= state.issued+m.e {
		return nil, false
	}
	return state, true
}

func (m *naiveModel) gift(o operation) (int64, error) {
	if !validNow(o.now) || o.user == "" || o.article == "" {
		return 0, ErrInvalidArgument
	}
	if o.now < m.maxNow {
		return 0, ErrClockSkew
	}
	if _, ok := m.articles[o.article]; !ok {
		return 0, ErrArticleNotFound
	}
	if o.now >= m.subs[o.user] {
		return 0, ErrNotSubscribed
	}
	month := m.month(o.now)
	if m.gifts[o.user] == nil {
		m.gifts[o.user] = map[int64]int{}
	}
	if m.gifts[o.user][month] >= m.g {
		return 0, ErrGiftQuota
	}
	m.maxNow = o.now
	m.next++
	m.tokens[m.next] = &naiveToken{id: m.next, article: o.article, issued: o.now, redeemed: map[string]struct{}{}}
	m.gifts[o.user][month]++
	return m.next, nil
}

func (m *naiveModel) login(o operation) error {
	if !validNow(o.now) || o.device == "" || o.user == "" {
		return ErrInvalidArgument
	}
	if o.now < m.maxNow {
		return ErrClockSkew
	}
	if _, ok := m.bound[o.device]; ok {
		return ErrAlreadyBound
	}
	m.maxNow = o.now
	m.bound[o.device] = o.user

	month := m.month(o.now)
	merged := map[string]naiveRecord{}
	for _, record := range m.book(o.user, month) {
		merged[record.article] = record
	}
	for _, deviceRecord := range m.book(o.device, month) {
		userRecord, ok := merged[deviceRecord.article]
		if !ok {
			merged[deviceRecord.article] = deviceRecord
			continue
		}
		if deviceRecord.at < userRecord.at {
			userRecord.at = deviceRecord.at
		}
		if deviceRecord.kind == naiveGift || userRecord.kind == naiveGift {
			userRecord.kind = naiveGift
		}
		merged[deviceRecord.article] = userRecord
	}

	quotas := make([]naiveRecord, 0, len(merged))
	for _, record := range merged {
		if record.kind == naiveQuota {
			quotas = append(quotas, record)
		}
	}
	sort.Slice(quotas, func(i, j int) bool {
		if quotas[i].at != quotas[j].at {
			return quotas[i].at < quotas[j].at
		}
		return quotas[i].article < quotas[j].article
	})
	for i := m.n; i < len(quotas); i++ {
		delete(merged, quotas[i].article)
	}
	m.ensure(o.user, month)
	m.books[o.user][month] = merged
	return nil
}

func (m *naiveModel) logout(o operation) error {
	if !validNow(o.now) || o.device == "" {
		return ErrInvalidArgument
	}
	if o.now < m.maxNow {
		return ErrClockSkew
	}
	if _, ok := m.bound[o.device]; !ok {
		return ErrNotBound
	}
	m.maxNow = o.now
	delete(m.bound, o.device)
	return nil
}

func (m *naiveModel) subscribe(o operation) error {
	if !validNow(o.now) || o.user == "" || o.until <= o.now {
		return ErrInvalidArgument
	}
	if o.now < m.maxNow {
		return ErrClockSkew
	}
	m.maxNow = o.now
	m.subs[o.user] = o.until
	return nil
}

var _ = fmt.Sprintf

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	t.Parallel()
	var log strings.Builder
	rng := rand.New(rand.NewSource(1446))
	devices := []string{"d1", "d2", "d3", ""}
	users := []string{"u1", "u2", "u3"}
	names := []string{"d1", "d2", "d3", "u1", "u2", "u3"}
	articles := []string{"a1", "a2", "a3", "a4", "a5"}

	for sequence := 0; sequence < 1500; sequence++ {
		n := rng.Intn(4)
		m := int64(3 + rng.Intn(12))
		g := 1 + rng.Intn(3)
		k := 1 + rng.Intn(3)
		e := int64(1 + rng.Intn(12))
		p := New(n, m, g, k, e)
		model := newNaive(n, g, k, m, e)
		for _, article := range articles {
			free := rng.Intn(5) == 0
			if err := p.AddArticle(article, free); err != nil {
				t.Fatal(err)
			}
			model.articles[article] = free
		}

		now := int64(0)
		var liveTokens []*Token
		for step := 0; step < 70; step++ {
			o := operation{
				kind:    opKind(rng.Intn(5)),
				now:     now,
				device:  devices[rng.Intn(len(devices))],
				user:    users[rng.Intn(len(users))],
				article: articles[rng.Intn(len(articles))],
			}
			if rng.Intn(8) == 0 {
				o.now += int64(rng.Intn(15))
			}
			o.until = o.now + int64(rng.Intn(10))

			var want Result
			var wantErr error
			var wantTokenID int64
			switch o.kind {
			case opRead:
				if len(liveTokens) > 0 && rng.Intn(2) == 0 {
					o.token = liveTokens[rng.Intn(len(liveTokens))]
					o.tokenID = o.token.ID()
				}
				want, wantErr = model.read(o)
			case opLogin:
				wantErr = model.login(o)
			case opLogout:
				wantErr = model.logout(o)
			case opSubscribe:
				wantErr = model.subscribe(o)
			case opGift:
				wantTokenID, wantErr = model.gift(o)
			}

			var got Result
			var gotErr error
			switch o.kind {
			case opRead:
				got, gotErr = p.Read(o.now, o.device, o.article, o.token)
			case opLogin:
				gotErr = p.Login(o.now, o.device, o.user)
			case opLogout:
				gotErr = p.Logout(o.now, o.device)
			case opSubscribe:
				gotErr = p.Subscribe(o.now, o.user, o.until)
			case opGift:
				var token *Token
				token, gotErr = p.Gift(o.now, o.user, o.article)
				if token != nil {
					if token.ID() != wantTokenID {
						t.Fatalf("seq %d step %d token id=%d want=%d", sequence, step, token.ID(), wantTokenID)
					}
					liveTokens = append(liveTokens, token)
				}
			}

			fmt.Fprintf(&log, "seq=%d config={n:%d m:%d g:%d k:%d e:%d} step=%d input={%s} => %s\n",
				sequence, n, m, g, k, e, step, describeOperation(o), describeOutcome(want, wantErr))
			if !sameError(gotErr, wantErr) {
				t.Fatalf("seq %d step %d %s error=%v want=%v", sequence, step, describeOperation(o), gotErr, wantErr)
			}
			if got != want {
				t.Fatalf("seq %d step %d %s result=%+v want=%+v", sequence, step, describeOperation(o), got, want)
			}
			if wantErr == nil {
				now = o.now
			}
		}
		assertNaiveState(t, p, model, names, sequence)
	}
	t.Logf("completed 1500 random sequences; full decision log written to %s", writeDecisionLog(t, &log))
}

func writeDecisionLog(t *testing.T, log *strings.Builder) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "paywall-random-decisions.log")
	if err := os.WriteFile(path, []byte(log.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sameError(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	for _, candidate := range []error{
		ErrInvalidArgument, ErrClockSkew, ErrArticleNotFound,
		ErrAlreadyBound, ErrNotBound, ErrNotSubscribed, ErrGiftQuota,
	} {
		if errors.Is(want, candidate) {
			return errors.Is(got, candidate)
		}
	}
	return got.Error() == want.Error()
}

func (k opKind) String() string {
	switch k {
	case opRead:
		return "Read"
	case opLogin:
		return "Login"
	case opLogout:
		return "Logout"
	case opSubscribe:
		return "Subscribe"
	case opGift:
		return "Gift"
	default:
		return "Unknown"
	}
}

func describeOperation(o operation) string {
	parts := []string{o.kind.String(), fmt.Sprintf("now=%d", o.now)}
	if o.device != "" {
		parts = append(parts, "device="+o.device)
	}
	if o.user != "" {
		parts = append(parts, "user="+o.user)
	}
	if o.article != "" {
		parts = append(parts, "article="+o.article)
	}
	if o.kind == opSubscribe {
		parts = append(parts, fmt.Sprintf("until=%d", o.until))
	}
	if o.tokenID != 0 {
		parts = append(parts, fmt.Sprintf("token=%d", o.tokenID))
	}
	return joinComma(parts)
}

func joinComma(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += ", "
		}
		out += part
	}
	return out
}

func describeOutcome(result Result, err error) string {
	if err != nil {
		return "error=" + err.Error()
	}
	if !result.Allowed {
		return "denied"
	}
	return "allowed reason=" + reasonName(result.Reason)
}

func reasonName(reason Reason) string {
	switch reason {
	case Subscription:
		return "subscription"
	case FreeArticle:
		return "free"
	case AlreadyUnlocked:
		return "already_unlocked"
	case Gift:
		return "gift"
	case Quota:
		return "quota"
	default:
		return "denied"
	}
}

func assertNaiveState(t *testing.T, p *Paywall, model *naiveModel, names []string, sequence int) {
	t.Helper()
	lastMonth := model.month(model.maxNow)
	for month := int64(0); month <= lastMonth; month++ {
		sampleNow := month*model.m + 1
		if sampleNow > model.maxNow {
			continue
		}
		for _, subject := range names {
			modelBook := model.book(subject, month)
			modelUsed := 0
			for _, record := range modelBook {
				if record.kind == naiveQuota {
					modelUsed++
				}
			}
			now := sampleNow
			if month == lastMonth {
				now = model.maxNow
			}
			if got := p.ledger.Used(subject, now); got != modelUsed {
				t.Fatalf("seq %d subject %s month %d used=%d want=%d", sequence, subject, month, got, modelUsed)
			}
			for article := range model.articles {
				record, ok := modelBook[article]
				gotRecord, gotOK := p.ledger.Lookup(subject, now, article)
				if ok != gotOK {
					t.Fatalf("seq %d subject %s month %d article %s presence got=%v want=%v",
						sequence, subject, month, article, gotOK, ok)
				}
				if !ok {
					continue
				}
				wantKind := meter.Quota
				if record.kind == naiveGift {
					wantKind = meter.Gift
				}
				if gotRecord.FirstUnlock != record.at || gotRecord.Kind != wantKind {
					t.Fatalf("seq %d record %s/%d/%s got=(%d,%v) want=(%d,%v)",
						sequence, subject, month, article, gotRecord.FirstUnlock, gotRecord.Kind, record.at, wantKind)
				}
			}
		}
	}

	for device, user := range model.bound {
		got, ok := p.binder.BoundUser(device)
		if !ok || got != user {
			t.Fatalf("seq %d binding %s got=(%s,%v) want=%s", sequence, device, got, ok, user)
		}
	}
	for id, modelToken := range model.tokens {
		live := p.tokens[id]
		if live == nil {
			t.Fatalf("seq %d token %d missing", sequence, id)
		}
		if live.token.article != modelToken.article || live.token.issued != modelToken.issued {
			t.Fatalf("seq %d token %d mismatch", sequence, id)
		}
		if len(live.redeemed) != len(modelToken.redeemed) {
			t.Fatalf("seq %d token %d redemption count got=%d want=%d",
				sequence, id, len(live.redeemed), len(modelToken.redeemed))
		}
		for subject := range modelToken.redeemed {
			if _, ok := live.redeemed[subject]; !ok {
				t.Fatalf("seq %d token %d missing redemption %s", sequence, id, subject)
			}
		}
	}
}
