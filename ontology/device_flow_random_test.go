package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type simGrant struct {
	id        uint64
	client    string
	code      string
	state     string
	expiresAt int64
	interval  int64
	next      int64
}

type simClient struct {
	events []int64
}

type naiveService struct {
	cfg       Config
	maxNow    int64
	nextID    uint64
	nextToken uint64
	grants    map[uint64]*simGrant
	latest    map[string]*simGrant
	clients   map[string]*simClient
}

type simStart struct {
	id        uint64
	code      string
	interval  int64
	expiresAt int64
}

func newNaive(cfg Config) *naiveService {
	return &naiveService{
		cfg:     cfg,
		grants:  map[uint64]*simGrant{},
		latest:  map[string]*simGrant{},
		clients: map[string]*simClient{},
	}
}

func (n *naiveService) client(key string) *simClient {
	state := n.clients[key]
	if state == nil {
		state = &simClient{}
		n.clients[key] = state
	}
	return state
}

func (n *naiveService) eventInfo(client string, now int64) (int64, int64) {
	var active []int64
	for _, event := range n.client(client).events {
		if event+n.cfg.H > now {
			active = append(active, event)
		}
	}
	count := int64(len(active))
	var u int64
	if count >= n.cfg.Z {
		u = active[count-n.cfg.Z] + n.cfg.H
	}
	return count, u
}

func (n *naiveService) baseInterval(sCount int64) int64 {
	interval := n.cfg.I0 + n.cfg.D*sCount
	if interval > n.cfg.Imax {
		return n.cfg.Imax
	}
	return interval
}

func (n *naiveService) activeCount(client string, now int64) int64 {
	var count int64
	for _, record := range n.grants {
		if record.client == client && now < record.expiresAt && (record.state == StatePending || record.state == StateApproved) {
			count++
		}
	}
	return count
}

func (n *naiveService) Start(client string, now int64, gen func() string) (simStart, string) {
	if client == "" {
		return simStart{}, KindInvalidArgument
	}
	if !validNow(now) {
		return simStart{}, KindInvalidArgument
	}
	if now < n.maxNow {
		return simStart{}, KindClockRewind
	}
	sCount, u := n.eventInfo(client, now)
	if sCount >= n.cfg.Z {
		_ = u
		return simStart{}, KindRateLimited
	}
	if n.activeCount(client, now) >= n.cfg.Cmax {
		return simStart{}, KindCapacity
	}
	failures := 0
	for failures < 100 {
		raw := gen()
		failures++
		normalized, ok := NormalizeUserCode(raw)
		if !ok {
			continue
		}
		if existing := n.latest[normalized]; existing != nil && now < existing.expiresAt {
			continue
		}
		n.maxNow = now
		n.nextID++
		sCount, _ = n.eventInfo(client, now)
		record := &simGrant{
			id:        n.nextID,
			client:    client,
			code:      normalized,
			state:     StatePending,
			expiresAt: now + n.cfg.E,
			interval:  n.baseInterval(sCount),
			next:      now,
		}
		n.grants[record.id] = record
		n.latest[normalized] = record
		return simStart{record.id, raw, record.interval, record.expiresAt}, ""
	}
	return simStart{}, KindGeneration
}

func (n *naiveService) Authorize(code string, approve bool, now int64) string {
	normalized, ok := NormalizeUserCode(code)
	if !ok {
		return KindInvalidArgument
	}
	if !validNow(now) {
		return KindInvalidArgument
	}
	if now < n.maxNow {
		return KindClockRewind
	}
	record := n.latest[normalized]
	if record == nil {
		return KindNotFound
	}
	if now >= record.expiresAt {
		return KindExpired
	}
	if record.state != StatePending {
		return KindAlreadyDecided
	}
	n.maxNow = now
	if approve {
		record.state = StateApproved
	} else {
		record.state = StateDenied
	}
	return ""
}

func (n *naiveService) Poll(id uint64, now int64) (PollResult, uint64, string) {
	if id == 0 || !validNow(now) {
		return PollResult{}, 0, KindInvalidArgument
	}
	if now < n.maxNow {
		return PollResult{}, 0, KindClockRewind
	}
	record := n.grants[id]
	if record == nil {
		return PollResult{}, 0, KindUnknownDevice
	}
	n.maxNow = now
	if record.state == StateConsumed {
		return PollResult{Kind: PollInvalid, Interval: record.interval, NextAllowed: record.next}, 0, ""
	}
	if now >= record.expiresAt {
		return PollResult{Kind: PollExpired, Interval: record.interval, NextAllowed: record.next}, 0, ""
	}
	if now < record.next {
		interval := record.interval + n.cfg.D
		if interval > n.cfg.Imax {
			interval = n.cfg.Imax
		}
		record.interval = interval
		record.next = now + interval
		state := n.client(record.client)
		state.events = append(state.events, now)
		return PollResult{Kind: PollTooFast, Interval: interval, NextAllowed: record.next}, 0, ""
	}
	record.next = now + record.interval
	switch record.state {
	case StatePending:
		return PollResult{Kind: PollWaiting, Interval: record.interval, NextAllowed: record.next}, 0, ""
	case StateApproved:
		n.nextToken++
		token := n.nextToken
		record.state = StateConsumed
		return PollResult{Kind: PollToken, Token: token, Interval: record.interval, NextAllowed: record.next}, token, ""
	default:
		record.state = StateConsumed
		return PollResult{Kind: PollAccessDenied, Interval: record.interval, NextAllowed: record.next}, 0, ""
	}
}

func randomCode(random *rand.Rand) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var builder strings.Builder
	length := 4 + random.Intn(8)
	for range length {
		builder.WriteByte(alphabet[random.Intn(len(alphabet))])
	}
	return builder.String()
}

func TestRandomMatchesNaiveSimulation(t *testing.T) {
	for iteration := range 2000 {
		random := rand.New(rand.NewSource(int64(iteration) + 1))
		cfg := Config{
			E:    int64(3 + random.Intn(30)),
			I0:   int64(1 + random.Intn(4)),
			D:    int64(1 + random.Intn(4)),
			Imax: int64(1 + random.Intn(20)),
			H:    int64(3 + random.Intn(30)),
			Cmax: int64(1 + random.Intn(4)),
			Z:    int64(1 + random.Intn(5)),
			Gen:  nil,
		}
		if cfg.Imax < cfg.I0 {
			cfg.Imax = cfg.I0
		}
		cfg.Gen = func() string {
			return randomCode(random)
		}
		service, err := New(cfg)
		if err != nil {
			t.Fatalf("iteration %d: config rejected: %+v", iteration, cfg)
		}
		naive := newNaive(cfg)
		var knownIDs []uint64
		knownCodes := []string{"ABCD1234"}
		now := int64(0)
		logf := func(format string, args ...any) {
			t.Logf("iter=%d now=%d %s", iteration, now, fmt.Sprintf(format, args...))
		}
		for step := range 60 {
			switch random.Intn(10) {
			case 0, 1, 2:
				client := []byte(fmt.Sprintf("c%d", random.Intn(3)))
				if random.Intn(20) == 0 {
					client = nil
				}
				codes := make([]string, 101)
				for codeIndex := range codes {
					codes[codeIndex] = randomCode(random)
				}
				genIndex := 0
				generator := func() string {
					code := codes[genIndex]
					genIndex++
					return code
				}
				cfg.Gen = generator
				service.gen = generator
				got, gotErr := service.Start(client, now)
				genIndex = 0
				want, wantKind := naive.Start(string(client), now, generator)
				logf("Start client=%q => got=(%+v,%v) want=(%+v,%s) basis=参数/时钟/限流/名额/生成", client, got, gotErr, want, wantKind)
				if errorKind(gotErr) != wantKind {
					t.Fatalf("iteration %d step %d Start kind mismatch: got=%v want=%s", iteration, step, gotErr, wantKind)
				}
				if gotErr == nil {
					if got.DeviceCode != want.id || got.UserCode != want.code || got.Interval != want.interval || got.ExpiresAt != want.expiresAt {
						t.Fatalf("iteration %d Start result mismatch: got=%+v want=%+v", iteration, got, want)
					}
					knownIDs = append(knownIDs, got.DeviceCode)
					knownCodes = append(knownCodes, got.UserCode)
				}
			case 3, 4:
				code := knownCodes[random.Intn(len(knownCodes))]
				if random.Intn(8) == 0 {
					code = "bad!"
				}
				if random.Intn(10) == 0 {
					code = strings.ToLower(code)
				}
				approve := random.Intn(2) == 0
				gotErr := service.Authorize(code, approve, now)
				wantKind := naive.Authorize(code, approve, now)
				logf("Authorize code=%q approve=%v => got=%v want=%s basis=参数/时钟/未找到/过期/已决定", code, approve, gotErr, wantKind)
				if errorKind(gotErr) != wantKind {
					t.Fatalf("iteration %d step %d Authorize mismatch: got=%v want=%s", iteration, step, gotErr, wantKind)
				}
			default:
				id := uint64(1 + random.Intn(len(knownIDs)+2))
				got, gotErr := service.Poll(id, now)
				want, _, wantKind := naive.Poll(id, now)
				logf("Poll device=%d => got=(%+v,%v) want=(%+v,%s) basis=consumed/过期/过快/pending/approved/denied", id, got, gotErr, want, wantKind)
				if errorKind(gotErr) != wantKind {
					t.Fatalf("iteration %d step %d Poll error mismatch: got=%v want=%s", iteration, step, gotErr, wantKind)
				}
				if gotErr == nil && got != want {
					t.Fatalf("iteration %d step %d Poll result mismatch: got=%+v want=%+v", iteration, step, got, want)
				}
			}
			now += int64(random.Intn(4))
		}
		for _, id := range knownIDs {
			got, err := service.Interval(id)
			if err != nil {
				t.Fatalf("interval: %v", err)
			}
			wantRecord := naive.grants[id]
			if got.State != wantRecord.state || got.Interval != wantRecord.interval || got.NextAllowed != wantRecord.next {
				t.Fatalf("iteration %d interval mismatch for %d: got=%+v want=%+v", iteration, id, got, wantRecord)
			}
		}
	}
}
