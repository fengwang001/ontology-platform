package chat

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Naive reference model
//
// modelChannel is a deliberately simple, independent implementation of the
// channel semantics: plain slices and maps, linear scans everywhere, no
// shared data structures with the real implementation. The randomized test
// below replays identical operation sequences against both and requires
// identical outputs.
// ---------------------------------------------------------------------------

type modelMsg struct {
	author     string
	body       string
	mentions   map[string]bool
	sentAt     int64
	edits      int
	recalled   bool
	recalledBy string
	recalledAt int64
}

type modelMember struct {
	admin         bool
	watermark     int
	joinWatermark int
}

type modelChannel struct {
	e, r    int64
	k       int
	lastNow int64
	msgs    []modelMsg
	members map[string]*modelMember
}

func newModelChannel(e, r int64, k int) *modelChannel {
	return &modelChannel{e: e, r: r, k: k, members: map[string]*modelMember{}}
}

func (m *modelChannel) checkNowRange(now int64) Code {
	if now < 0 || now > MaxNow {
		return ErrInvalidParam
	}
	return 0
}

func (m *modelChannel) checkClock(now int64) Code {
	if now < m.lastNow {
		return ErrClockSkew
	}
	return 0
}

func (m *modelChannel) checkNow(now int64) Code {
	if c := m.checkNowRange(now); c != 0 {
		return c
	}
	return m.checkClock(now)
}

func (m *modelChannel) isMember(u string) bool {
	_, ok := m.members[u]
	return ok
}

func validBody(body string) bool {
	return body != "" && len([]rune(body)) <= MaxBodyLen
}

func (m *modelChannel) mentionShapeOK(mentions []string) Code {
	if len(mentions) > MaxMentions {
		return ErrInvalidParam
	}
	seen := map[string]bool{}
	for _, id := range mentions {
		if id == "" || seen[id] || !m.isMember(id) {
			return ErrInvalidParam
		}
		seen[id] = true
	}
	return 0
}

func (m *modelChannel) join(user string, now int64) Code {
	if user == "" {
		return ErrInvalidParam
	}
	if c := m.checkNow(now); c != 0 {
		return c
	}
	if _, ok := m.members[user]; !ok {
		m.members[user] = &modelMember{
			admin:         len(m.members) == 0,
			watermark:     len(m.msgs),
			joinWatermark: len(m.msgs),
		}
	}
	m.lastNow = now
	return 0
}

func (m *modelChannel) leave(user string, now int64) Code {
	if user == "" {
		return ErrInvalidParam
	}
	if c := m.checkNow(now); c != 0 {
		return c
	}
	if !m.isMember(user) {
		return ErrNotMember
	}
	delete(m.members, user)
	m.lastNow = now
	return 0
}

func (m *modelChannel) promote(admin, target string, now int64) Code {
	if admin == "" || target == "" {
		return ErrInvalidParam
	}
	if c := m.checkNowRange(now); c != 0 {
		return c
	}
	tm, ok := m.members[target]
	if !ok {
		return ErrInvalidParam
	}
	if c := m.checkClock(now); c != 0 {
		return c
	}
	am, ok := m.members[admin]
	if !ok {
		return ErrNotMember
	}
	if !am.admin {
		return ErrPermissionDenied
	}
	tm.admin = true
	m.lastNow = now
	return 0
}

func (m *modelChannel) send(user, body string, mentions []string, now int64) (int, Code) {
	if user == "" || !validBody(body) {
		return 0, ErrInvalidParam
	}
	if c := m.checkNowRange(now); c != 0 {
		return 0, c
	}
	if c := m.mentionShapeOK(mentions); c != 0 {
		return 0, c
	}
	for _, id := range mentions {
		if id == user {
			return 0, ErrInvalidParam
		}
	}
	if c := m.checkClock(now); c != 0 {
		return 0, c
	}
	if !m.isMember(user) {
		return 0, ErrNotMember
	}
	set := map[string]bool{}
	for _, id := range mentions {
		set[id] = true
	}
	m.msgs = append(m.msgs, modelMsg{author: user, body: body, mentions: set, sentAt: now})
	m.lastNow = now
	return len(m.msgs), 0
}

func (m *modelChannel) edit(user string, seq int, body string, mentions []string, now int64) Code {
	if user == "" || !validBody(body) || seq < 1 {
		return ErrInvalidParam
	}
	if c := m.checkNowRange(now); c != 0 {
		return c
	}
	if c := m.mentionShapeOK(mentions); c != 0 {
		return c
	}
	if c := m.checkClock(now); c != 0 {
		return c
	}
	if !m.isMember(user) {
		return ErrNotMember
	}
	if seq > len(m.msgs) {
		return ErrMessageNotFound
	}
	msg := &m.msgs[seq-1]
	for _, id := range mentions {
		if id == msg.author {
			return ErrInvalidParam
		}
	}
	if user != msg.author {
		return ErrPermissionDenied
	}
	if msg.recalled {
		return ErrAlreadyRecalled
	}
	if now-msg.sentAt >= m.e {
		return ErrTimeout
	}
	if msg.edits >= m.k {
		return ErrEditLimit
	}
	set := map[string]bool{}
	for _, id := range mentions {
		set[id] = true
	}
	msg.body = body
	msg.mentions = set
	msg.edits++
	m.lastNow = now
	return 0
}

func (m *modelChannel) recall(user string, seq int, now int64) Code {
	if user == "" || seq < 1 {
		return ErrInvalidParam
	}
	if c := m.checkNowRange(now); c != 0 {
		return c
	}
	if c := m.checkClock(now); c != 0 {
		return c
	}
	caller, ok := m.members[user]
	if !ok {
		return ErrNotMember
	}
	if seq > len(m.msgs) {
		return ErrMessageNotFound
	}
	msg := &m.msgs[seq-1]
	if user != msg.author && !caller.admin {
		return ErrPermissionDenied
	}
	if msg.recalled {
		return ErrAlreadyRecalled
	}
	if !caller.admin && now-msg.sentAt >= m.r {
		return ErrTimeout
	}
	msg.recalled = true
	msg.recalledBy = user
	msg.recalledAt = now
	msg.mentions = nil
	msg.body = ""
	m.lastNow = now
	return 0
}

func (m *modelChannel) markRead(user string, upto int, now int64) Code {
	if user == "" || upto < 0 {
		return ErrInvalidParam
	}
	if c := m.checkNowRange(now); c != 0 {
		return c
	}
	if c := m.checkClock(now); c != 0 {
		return c
	}
	mem, ok := m.members[user]
	if !ok {
		return ErrNotMember
	}
	if upto < mem.watermark {
		return ErrWatermarkRollback
	}
	if upto > len(m.msgs) {
		return ErrOutOfRange
	}
	mem.watermark = upto
	m.lastNow = now
	return 0
}

func (m *modelChannel) unread(user string) (int, Code) {
	if user == "" {
		return 0, ErrInvalidParam
	}
	mem, ok := m.members[user]
	if !ok {
		return 0, ErrNotMember
	}
	n := 0
	for i := mem.watermark + 1; i <= len(m.msgs); i++ {
		msg := m.msgs[i-1]
		if !msg.recalled && msg.author != user {
			n++
		}
	}
	return n, 0
}

func (m *modelChannel) unreadMentions(user string) (int, Code) {
	if user == "" {
		return 0, ErrInvalidParam
	}
	mem, ok := m.members[user]
	if !ok {
		return 0, ErrNotMember
	}
	n := 0
	for i := mem.watermark + 1; i <= len(m.msgs); i++ {
		msg := m.msgs[i-1]
		if !msg.recalled && msg.mentions[user] {
			n++
		}
	}
	return n, 0
}

func (m *modelChannel) fetch(viewer string, before, limit int, now int64) ([]MessageView, Code) {
	if viewer == "" || before < 0 || limit < 1 || limit > MaxFetchLimit {
		return nil, ErrInvalidParam
	}
	if c := m.checkNowRange(now); c != 0 {
		return nil, c
	}
	if c := m.checkClock(now); c != 0 {
		return nil, c
	}
	mem, ok := m.members[viewer]
	if !ok {
		return nil, ErrNotMember
	}
	start := before - 1
	if before == 0 || start > len(m.msgs) {
		start = len(m.msgs)
	}
	var views []MessageView
	for seq := start; seq > mem.joinWatermark && len(views) < limit; seq-- {
		msg := m.msgs[seq-1]
		if msg.recalled {
			views = append(views, MessageView{
				Seq:        seq,
				Recalled:   true,
				RecalledBy: msg.recalledBy,
				RecalledAt: msg.recalledAt,
			})
			continue
		}
		var mentions []string
		for id := range msg.mentions {
			mentions = append(mentions, id)
		}
		// Model mentions come from a map: sort to match the real output.
		sort.Strings(mentions)
		views = append(views, MessageView{
			Seq:       seq,
			Author:    msg.author,
			Body:      msg.body,
			Mentions:  mentions,
			SentAt:    msg.sentAt,
			EditCount: msg.edits,
		})
	}
	m.lastNow = now
	return views, 0
}

// ---------------------------------------------------------------------------
// Randomized differential test
// ---------------------------------------------------------------------------

// op describes one generated operation in a replayable form.
type op struct {
	kind          string
	user, target  string
	body          string
	mentions      []string
	seq, upto     int
	before, limit int
	now           int64
}

func (o op) String() string {
	switch o.kind {
	case "send":
		return fmt.Sprintf("Send(%q, %q, %v, %d)", o.user, o.body, o.mentions, o.now)
	case "edit":
		return fmt.Sprintf("Edit(%q, %d, %q, %v, %d)", o.user, o.seq, o.body, o.mentions, o.now)
	case "recall":
		return fmt.Sprintf("Recall(%q, %d, %d)", o.user, o.seq, o.now)
	case "markread":
		return fmt.Sprintf("MarkRead(%q, %d, %d)", o.user, o.upto, o.now)
	case "fetch":
		return fmt.Sprintf("Fetch(%q, %d, %d, %d)", o.user, o.before, o.limit, o.now)
	case "promote":
		return fmt.Sprintf("Promote(%q, %q, %d)", o.user, o.target, o.now)
	case "join":
		return fmt.Sprintf("Join(%q, %d)", o.user, o.now)
	case "leave":
		return fmt.Sprintf("Leave(%q, %d)", o.user, o.now)
	case "unread":
		return fmt.Sprintf("Unread(%q)", o.user)
	case "unreadmentions":
		return fmt.Sprintf("UnreadMentions(%q)", o.user)
	}
	return o.kind
}

var userPool = []string{"u0", "u1", "u2", "u3", "u4"}

func pickUser(rng *rand.Rand) string {
	switch n := rng.Intn(20); {
	case n < 17:
		return userPool[rng.Intn(len(userPool))]
	case n < 19:
		return "ghost" // never joins
	default:
		return "" // invalid
	}
}

func pickBody(rng *rand.Rand) string {
	switch n := rng.Intn(20); {
	case n < 16:
		return fmt.Sprintf("msg-%d", rng.Intn(1000))
	case n < 18:
		return "" // invalid: empty
	case n == 18:
		return strings.Repeat("x", 4001) // invalid: too long
	default:
		return strings.Repeat("好", 4000) // valid boundary
	}
}

func pickMentions(rng *rand.Rand) []string {
	switch n := rng.Intn(20); {
	case n < 12:
		// Random subset of the pool; may include the sender (invalid).
		var out []string
		for _, u := range userPool {
			if rng.Intn(4) == 0 {
				out = append(out, u)
			}
		}
		return out
	case n < 14:
		return []string{"u0", "u0"} // duplicate
	case n < 16:
		return []string{"ghost"} // non-member
	case n == 16:
		out := make([]string, 21) // too many
		for i := range out {
			out[i] = fmt.Sprintf("m%d", i)
		}
		return out
	default:
		return nil
	}
}

// nextNow generates a mostly monotone timestamp with occasional regressions
// and out-of-range values.
func nextNow(rng *rand.Rand, base int64) (now, nextBase int64) {
	switch n := rng.Intn(20); {
	case n < 16:
		now = base + rng.Int63n(4)
	case n < 19:
		now = base - rng.Int63n(6) // regression, possibly negative
	default:
		now = rng.Int63n(MaxNow + 2) // wild, possibly out of range
	}
	if now >= 0 {
		return now, now
	}
	return now, 0
}

func viewsEqual(a, b []MessageView) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Seq != y.Seq || x.Author != y.Author || x.Body != y.Body ||
			x.SentAt != y.SentAt || x.EditCount != y.EditCount ||
			x.Recalled != y.Recalled || x.RecalledBy != y.RecalledBy ||
			x.RecalledAt != y.RecalledAt {
			return false
		}
		if len(x.Mentions) != len(y.Mentions) {
			return false
		}
		for j := range x.Mentions {
			if x.Mentions[j] != y.Mentions[j] {
				return false
			}
		}
	}
	return true
}

// TestModelComparison replays 1500 random operation sequences against both
// the real channel and the naive model, requiring identical outcomes for
// every single operation. Every step logs its input, both outputs and the
// verdict (visible with `go test -v` or on failure).
func TestModelComparison(t *testing.T) {
	sequences := 1500
	if v := os.Getenv("CHAT_MODEL_SEQUENCES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			sequences = n
		}
	}
	for s := 0; s < sequences; s++ {
		rng := rand.New(rand.NewSource(int64(s) + 1))
		runSequence(t, s, rng)
	}
}

func runSequence(t *testing.T, seed int, rng *rand.Rand) {
	t.Helper()
	windows := []int64{1, 2, 3, 5, 10, 100, 86400}
	e := windows[rng.Intn(len(windows))]
	r := windows[rng.Intn(len(windows))]
	k := []int{0, 1, 2, 3, 5, 10}[rng.Intn(6)]

	real, err := NewChannel(e, r, k)
	if err != nil {
		t.Fatalf("NewChannel: %v", err)
	}
	model := newModelChannel(e, r, k)

	t.Logf("sequence %d: E=%d R=%d K=%d", seed, e, r, k)

	var base int64
	ops := 20 + rng.Intn(40)
	for i := 0; i < ops; i++ {
		var o op
		latest := real.Latest()
		switch n := rng.Intn(100); {
		case n < 12:
			o.kind = "join"
			o.user = pickUser(rng)
		case n < 17:
			o.kind = "leave"
			o.user = pickUser(rng)
		case n < 21:
			o.kind = "promote"
			o.user = pickUser(rng)
			o.target = pickUser(rng)
		case n < 45:
			o.kind = "send"
			o.user = pickUser(rng)
			o.body = pickBody(rng)
			o.mentions = pickMentions(rng)
		case n < 57:
			o.kind = "edit"
			o.user = pickUser(rng)
			o.seq = rng.Intn(latest + 3)
			o.body = pickBody(rng)
			o.mentions = pickMentions(rng)
		case n < 66:
			o.kind = "recall"
			o.user = pickUser(rng)
			o.seq = rng.Intn(latest + 3)
		case n < 78:
			o.kind = "markread"
			o.user = pickUser(rng)
			o.upto = rng.Intn(latest+3) - 1
		case n < 86:
			o.kind = "fetch"
			o.user = pickUser(rng)
			o.before = rng.Intn(latest + 3)
			o.limit = rng.Intn(103)
		case n < 93:
			o.kind = "unread"
			o.user = pickUser(rng)
		default:
			o.kind = "unreadmentions"
			o.user = pickUser(rng)
		}
		o.now, base = nextNow(rng, base)

		var realRes, modelRes string
		switch o.kind {
		case "join":
			rc, mc := CodeOf(real.Join(o.user, o.now)), model.join(o.user, o.now)
			realRes, modelRes = fmt.Sprint(rc), fmt.Sprint(mc)
			compare(t, seed, i, o, rc, mc)
		case "leave":
			rc, mc := CodeOf(real.Leave(o.user, o.now)), model.leave(o.user, o.now)
			realRes, modelRes = fmt.Sprint(rc), fmt.Sprint(mc)
			compare(t, seed, i, o, rc, mc)
		case "promote":
			rc, mc := CodeOf(real.Promote(o.user, o.target, o.now)), model.promote(o.user, o.target, o.now)
			realRes, modelRes = fmt.Sprint(rc), fmt.Sprint(mc)
			compare(t, seed, i, o, rc, mc)
		case "send":
			rseq, rerr := real.Send(o.user, o.body, o.mentions, o.now)
			mseq, mc := model.send(o.user, o.body, o.mentions, o.now)
			rc := CodeOf(rerr)
			realRes = fmt.Sprintf("seq=%d code=%v", rseq, rc)
			modelRes = fmt.Sprintf("seq=%d code=%v", mseq, mc)
			compare(t, seed, i, o, rc, mc)
			if rc == 0 && rseq != mseq {
				t.Fatalf("seed %d op %d %s: real seq=%d model seq=%d", seed, i, o, rseq, mseq)
			}
		case "edit":
			rc, mc := CodeOf(real.Edit(o.user, o.seq, o.body, o.mentions, o.now)),
				model.edit(o.user, o.seq, o.body, o.mentions, o.now)
			realRes, modelRes = fmt.Sprint(rc), fmt.Sprint(mc)
			compare(t, seed, i, o, rc, mc)
		case "recall":
			rc, mc := CodeOf(real.Recall(o.user, o.seq, o.now)), model.recall(o.user, o.seq, o.now)
			realRes, modelRes = fmt.Sprint(rc), fmt.Sprint(mc)
			compare(t, seed, i, o, rc, mc)
		case "markread":
			rc, mc := CodeOf(real.MarkRead(o.user, o.upto, o.now)), model.markRead(o.user, o.upto, o.now)
			realRes, modelRes = fmt.Sprint(rc), fmt.Sprint(mc)
			compare(t, seed, i, o, rc, mc)
		case "unread":
			rn, rerr := real.Unread(o.user)
			mn, mc := model.unread(o.user)
			rc := CodeOf(rerr)
			realRes = fmt.Sprintf("n=%d code=%v", rn, rc)
			modelRes = fmt.Sprintf("n=%d code=%v", mn, mc)
			compare(t, seed, i, o, rc, mc)
			if rc == 0 && rn != mn {
				t.Fatalf("seed %d op %d %s: real=%d model=%d", seed, i, o, rn, mn)
			}
		case "unreadmentions":
			rn, rerr := real.UnreadMentions(o.user)
			mn, mc := model.unreadMentions(o.user)
			rc := CodeOf(rerr)
			realRes = fmt.Sprintf("n=%d code=%v", rn, rc)
			modelRes = fmt.Sprintf("n=%d code=%v", mn, mc)
			compare(t, seed, i, o, rc, mc)
			if rc == 0 && rn != mn {
				t.Fatalf("seed %d op %d %s: real=%d model=%d", seed, i, o, rn, mn)
			}
		case "fetch":
			rv, rerr := real.Fetch(o.user, o.before, o.limit, o.now)
			mv, mc := model.fetch(o.user, o.before, o.limit, o.now)
			rc := CodeOf(rerr)
			realRes = fmt.Sprintf("views=%d code=%v", len(rv), rc)
			modelRes = fmt.Sprintf("views=%d code=%v", len(mv), mc)
			compare(t, seed, i, o, rc, mc)
			if rc == 0 && !viewsEqual(rv, mv) {
				t.Fatalf("seed %d op %d %s:\nreal:  %+v\nmodel: %+v", seed, i, o, rv, mv)
			}
		}
		t.Logf("  seed=%d op=%d in=%s real=[%s] model=[%s] verdict=MATCH", seed, i, o, realRes, modelRes)
	}
}

func compare(t *testing.T, seed, i int, o op, real, model Code) {
	t.Helper()
	if real != model {
		t.Fatalf("seed %d op %d %s: real code=%v model code=%v", seed, i, o, real, model)
	}
}
