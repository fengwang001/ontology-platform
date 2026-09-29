package tokenize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		Domains: map[string]Domain{
			"email": {MaxTokens: 100},
			"phone": {MaxTokens: 2},
		},
		Tables: map[string]Table{
			"users":  {SensitiveColumns: map[string]string{"email": "email", "phone": "phone"}},
			"orders": {SensitiveColumns: map[string]string{"buyer_email": "email", "contact_phone": "phone"}},
		},
	}
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, &buf
}

func TestCrossTableSameDomainAndOrdering(t *testing.T) {
	logger, logs := captureLogger()
	tz, err := New(testConfig(), logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	ev1 := Event{
		Table:  "users",
		Before: map[string]any{"email": "a@x", "name": "Ann"},
		After:  map[string]any{"email": "b@x", "name": "Bob"},
	}
	out1, err := tz.Transform(ctx, ev1)
	if err != nil {
		t.Fatalf("ev1: %v", err)
	}
	if got := out1.Before["email"]; got != "email-1" {
		t.Fatalf("before email = %v, want email-1", got)
	}
	if got := out1.After["email"]; got != "email-2" {
		t.Fatalf("after email = %v, want email-2", got)
	}
	if got := out1.Before["name"]; got != "Ann" {
		t.Fatalf("passthrough name = %v", got)
	}

	ev2 := Event{
		Table:  "orders",
		Before: map[string]any{"buyer_email": "a@x", "contact_phone": nil},
		After:  map[string]any{"buyer_email": "c@x"},
	}
	out2, err := tz.Transform(ctx, ev2)
	if err != nil {
		t.Fatalf("ev2: %v", err)
	}
	if got := out2.Before["buyer_email"]; got != "email-1" {
		t.Fatalf("cross-table shared value = %v, want email-1", got)
	}
	if got := out2.After["buyer_email"]; got != "email-3" {
		t.Fatalf("new value = %v, want email-3", got)
	}
	if got, ok := out2.Before["contact_phone"]; !ok || got != nil {
		t.Fatalf("null must stay null, got %v", got)
	}

	count, _ := tz.DomainTokenCount("email")
	if count != 3 {
		t.Fatalf("email token count = %d, want 3", count)
	}
	logText := logs.String()
	for _, want := range []string{
		"event received",
		"first occurrence in domain",
		"null kept null",
		"token already committed in domain",
		"committed token",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}
}

func TestNullVersusEmptyString(t *testing.T) {
	tz, _ := New(testConfig(), silentLogger())

	out, err := tz.Transform(context.Background(), Event{
		Table:  "users",
		Before: map[string]any{"email": nil, "phone": ""},
		After:  map[string]any{"email": "", "phone": nil},
	})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out.Before["email"] != nil {
		t.Fatalf("null before = %v", out.Before["email"])
	}
	if out.After["phone"] != nil {
		t.Fatalf("null after = %v", out.After["phone"])
	}
	if out.Before["phone"] != "phone-1" || out.After["email"] != "email-1" {
		t.Fatalf("empty string must allocate: before phone=%v after email=%v",
			out.Before["phone"], out.After["email"])
	}
	phoneCount, _ := tz.DomainTokenCount("phone")
	emailCount, _ := tz.DomainTokenCount("email")
	if phoneCount != 1 || emailCount != 1 {
		t.Fatalf("counts phone=%d email=%d, want 1/1", phoneCount, emailCount)
	}
}

func TestOverfillRollbackCommittedAndStaged(t *testing.T) {
	t.Run("committed table rejects new event", func(t *testing.T) {
		tz, _ := New(testConfig(), silentLogger())
		ctx := context.Background()
		for i, value := range []string{"p1", "p2"} {
			if _, err := tz.Transform(ctx, Event{Table: "users", After: map[string]any{"phone": value}}); err != nil {
				t.Fatalf("seed %d: %v", i, err)
			}
		}
		_, err := tz.Transform(ctx, Event{
			Table:  "orders",
			Before: map[string]any{"contact_phone": "p3"},
			After:  map[string]any{"contact_phone": "p1"},
		})
		if KindOf(err) != KindTokenLimitExceeded {
			t.Fatalf("err kind = %v, want token_limit_exceeded", KindOf(err))
		}
		count, _ := tz.DomainTokenCount("phone")
		if count != 2 {
			t.Fatalf("count after reject = %d, want 2", count)
		}
		tokens, _ := tz.DomainTokens("phone")
		if _, leaked := tokens["p3"]; leaked {
			t.Fatalf("rejected value leaked into token table: %v", tokens)
		}
		if tokens["p1"] != "phone-1" || tokens["p2"] != "phone-2" {
			t.Fatalf("numbering not dense: %v", tokens)
		}
	})

	t.Run("staged allocations within event are revoked", func(t *testing.T) {
		tz, _ := New(testConfig(), silentLogger())
		if _, err := tz.Transform(context.Background(), Event{
			Table: "users",
			After: map[string]any{"phone": "p0"},
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		_, err := tz.Transform(context.Background(), Event{
			Table:  "users",
			Before: map[string]any{"phone": "p1"}, // 暂存 phone-2
			After:  map[string]any{"phone": "p2"}, // 试图分配 phone-3 -> 超限
		})
		if KindOf(err) != KindTokenLimitExceeded {
			t.Fatalf("err kind = %v, want token_limit_exceeded", KindOf(err))
		}
		count, _ := tz.DomainTokenCount("phone")
		if count != 1 {
			t.Fatalf("count after staged rollback = %d, want 1", count)
		}
		tokens, _ := tz.DomainTokens("phone")
		if _, ok := tokens["p1"]; ok {
			t.Fatalf("staged value p1 leaked: %v", tokens)
		}
		if tokens["p0"] != "phone-1" {
			t.Fatalf("committed token altered: %v", tokens)
		}
	})
}

func TestInvalidConfig(t *testing.T) {
	cases := map[string]Config{
		"no domains":          {Domains: nil, Tables: map[string]Table{"t": {}}},
		"no tables":           {Domains: map[string]Domain{"d": {MaxTokens: 1}}, Tables: nil},
		"empty domain name":   {Domains: map[string]Domain{"": {MaxTokens: 1}}, Tables: map[string]Table{}},
		"non-positive limit":  {Domains: map[string]Domain{"d": {MaxTokens: 0}}, Tables: map[string]Table{}},
		"undefined domain":    {Domains: map[string]Domain{"d": {MaxTokens: 1}}, Tables: map[string]Table{"t": {SensitiveColumns: map[string]string{"c": "other"}}}},
		"empty column domain": {Domains: map[string]Domain{"d": {MaxTokens: 1}}, Tables: map[string]Table{"t": {SensitiveColumns: map[string]string{"c": ""}}}},
		"empty column name":   {Domains: map[string]Domain{"d": {MaxTokens: 1}}, Tables: map[string]Table{"t": {SensitiveColumns: map[string]string{"": "d"}}}},
		"empty table name":    {Domains: map[string]Domain{"d": {MaxTokens: 1}}, Tables: map[string]Table{"": {}}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			tz, err := New(cfg, nil)
			if KindOf(err) != KindInvalidConfig {
				t.Fatalf("err = %v, want invalid_config", err)
			}
			if tz != nil {
				t.Fatalf("tokenizer must be nil on invalid config")
			}
		})
	}
}

func TestInvalidEventsLeaveStateUntouched(t *testing.T) {
	tz, _ := New(testConfig(), silentLogger())
	ctx := context.Background()

	_, err := tz.Transform(ctx, Event{Table: "ghost", After: map[string]any{"x": "y"}})
	if KindOf(err) != KindUnknownTable {
		t.Fatalf("unknown table kind = %v", KindOf(err))
	}
	_, err = tz.Transform(ctx, Event{Table: "users"})
	if KindOf(err) != KindInvalidEvent {
		t.Fatalf("both-nil kind = %v", KindOf(err))
	}
	_, err = tz.Transform(ctx, Event{
		Table:  "users",
		Before: map[string]any{"email": "a@x"},
		After:  map[string]any{"email": 42},
	})
	if KindOf(err) != KindInvalidEvent {
		t.Fatalf("wrong-type kind = %v", KindOf(err))
	}

	for _, domain := range []string{"email", "phone"} {
		count, _ := tz.DomainTokenCount(domain)
		if count != 0 {
			t.Fatalf("domain %s count = %d after rejected calls, want 0", domain, count)
		}
	}
}

func TestErrorKindsDistinct(t *testing.T) {
	seen := map[ErrorKind]bool{}
	for _, k := range []ErrorKind{KindInvalidConfig, KindUnknownTable, KindInvalidEvent, KindTokenLimitExceeded} {
		if seen[k] {
			t.Fatalf("duplicate error kind value %d", k)
		}
		seen[k] = true
		if k.String() == "unknown" {
			t.Fatalf("kind %d has no readable name", k)
		}
	}
	if KindOf(errors.New("plain")) != 0 {
		t.Fatalf("KindOf must return 0 for foreign errors")
	}
}

func TestCallerEventNotModified(t *testing.T) {
	tz, _ := New(testConfig(), silentLogger())
	ev := Event{
		Table:  "users",
		Before: map[string]any{"email": "a@x", "name": "Ann"},
		After:  map[string]any{"email": "b@x"},
	}
	snapshot := deepCopyEvent(ev)
	if _, err := tz.Transform(context.Background(), ev); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if !reflect.DeepEqual(ev, snapshot) {
		t.Fatalf("caller event mutated:\n got %#v\nwant %#v", ev, snapshot)
	}
}

func deepCopyEvent(ev Event) Event {
	cp := func(m map[string]any) map[string]any {
		if m == nil {
			return nil
		}
		c := make(map[string]any, len(m))
		for k, v := range m {
			c[k] = v
		}
		return c
	}
	return Event{Table: ev.Table, Before: cp(ev.Before), After: cp(ev.After)}
}

// ---- 朴素参照：用最直白的方式独立实现同一规范 ----

type naiveDomain struct {
	maxTokens int
	tokens    map[string]string
}

type naiveTokenizer struct {
	domains map[string]*naiveDomain
	tables  map[string]map[string]string
}

func newNaive(cfg Config) *naiveTokenizer {
	n := &naiveTokenizer{domains: map[string]*naiveDomain{}, tables: map[string]map[string]string{}}
	for name, d := range cfg.Domains {
		n.domains[name] = &naiveDomain{maxTokens: d.MaxTokens, tokens: map[string]string{}}
	}
	for name, tbl := range cfg.Tables {
		n.tables[name] = tbl.SensitiveColumns
	}
	return n
}

func (n *naiveTokenizer) run(ev Event) (out Event, kind ErrorKind) {
	cols, ok := n.tables[ev.Table]
	if !ok {
		return Event{}, KindUnknownTable
	}
	if ev.Before == nil && ev.After == nil {
		return Event{}, KindInvalidEvent
	}
	staged := map[string]map[string]string{}
	mask := func(img map[string]any) (map[string]any, ErrorKind) {
		if img == nil {
			return nil, 0
		}
		names := make([]string, 0, len(img))
		for c := range img {
			names = append(names, c)
		}
		sort.Strings(names)
		res := map[string]any{}
		for _, col := range names {
			raw := img[col]
			domain, sensitive := cols[col]
			if !sensitive {
				res[col] = raw
				continue
			}
			if raw == nil {
				res[col] = nil
				continue
			}
			value, ok := raw.(string)
			if !ok {
				return nil, KindInvalidEvent
			}
			d := n.domains[domain]
			token, known := d.tokens[value]
			if !known {
				if s := staged[domain]; s != nil {
					if tok, ok2 := s[value]; ok2 {
						token, known = tok, true
					}
				}
			}
			if !known {
				if len(d.tokens)+len(staged[domain]) >= d.maxTokens {
					return nil, KindTokenLimitExceeded
				}
				seq := len(d.tokens) + len(staged[domain]) + 1
				token = fmt.Sprintf("%s-%d", domain, seq)
				if staged[domain] == nil {
					staged[domain] = map[string]string{}
				}
				staged[domain][value] = token
			}
			res[col] = token
		}
		return res, 0
	}
	before, kind := mask(ev.Before)
	if kind != 0 {
		return Event{}, kind
	}
	after, kind := mask(ev.After)
	if kind != 0 {
		return Event{}, kind
	}
	for domain, values := range staged {
		d := n.domains[domain]
		for value, token := range values {
			d.tokens[value] = token
		}
	}
	return Event{Table: ev.Table, Before: before, After: after}, 0
}

func randomEvents(rng *rand.Rand) []Event {
	const n = 400
	emails := []string{"a@x", "b@x", "c@x", "d@x", "e@x"}
	phones := []string{"p1", "p2", "p3", "p4"}
	events := make([]Event, 0, n)
	img := func(colEmail, colPhone string) map[string]any {
		m := map[string]any{}
		switch rng.Intn(3) {
		case 0:
			m[colEmail] = nil
		case 1:
			m[colEmail] = ""
		default:
			m[colEmail] = emails[rng.Intn(len(emails))]
		}
		if rng.Intn(2) == 0 {
			m[colPhone] = phones[rng.Intn(len(phones))]
		}
		m["trace"] = rng.Intn(1000)
		return m
	}
	for i := 0; i < n; i++ {
		var table, colEmail, colPhone string
		if rng.Intn(2) == 0 {
			table, colEmail, colPhone = "users", "email", "phone"
		} else {
			table, colEmail, colPhone = "orders", "buyer_email", "contact_phone"
		}
		ev := Event{Table: table}
		if rng.Intn(2) == 0 {
			ev.Before = img(colEmail, colPhone)
		}
		ev.After = img(colEmail, colPhone)
		events = append(events, ev)
	}
	return events
}

func TestMatchesNaiveReference(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		events := randomEvents(rng)
		got, _ := New(testConfig(), silentLogger())
		want := newNaive(testConfig())
		for i, ev := range events {
			gOut, gErr := got.Transform(context.Background(), ev)
			wOut, wKind := want.run(ev)
			if KindOf(gErr) != wKind {
				t.Fatalf("seed %d event %d: got kind %v want %v (err=%v)", seed, i, KindOf(gErr), wKind, gErr)
			}
			if wKind == 0 && !reflect.DeepEqual(gOut, wOut) {
				t.Fatalf("seed %d event %d output mismatch:\n got %#v\nwant %#v", seed, i, gOut, wOut)
			}
		}
		for domain := range testConfig().Domains {
			gTokens, _ := got.DomainTokens(domain)
			if !reflect.DeepEqual(gTokens, want.domains[domain].tokens) {
				t.Fatalf("seed %d domain %s token table mismatch:\n got %v\nwant %v",
					seed, domain, gTokens, want.domains[domain].tokens)
			}
		}
	}
}

func TestReproducibleAcrossRuns(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	events := randomEvents(rng)
	run := func() map[string]map[string]string {
		tz, _ := New(testConfig(), silentLogger())
		result := map[string]map[string]string{}
		for _, ev := range events {
			if _, err := tz.Transform(context.Background(), ev); err != nil {
				result[err.Error()] = nil
			}
		}
		for _, domain := range []string{"email", "phone"} {
			tokens, _ := tz.DomainTokens(domain)
			result[domain] = tokens
		}
		return result
	}
	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("tokenization not reproducible:\nfirst  %v\nsecond %v", first, second)
	}
}

func TestConcurrentTransforms(t *testing.T) {
	cfg := Config{
		Domains: map[string]Domain{"d": {MaxTokens: 100000}},
		Tables: map[string]Table{
			"t1": {SensitiveColumns: map[string]string{"c1": "d"}},
			"t2": {SensitiveColumns: map[string]string{"c2": "d"}},
		},
	}
	tz, _ := New(cfg, silentLogger())
	const goroutines, perG = 16, 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	assigned := map[string]string{}
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				value := fmt.Sprintf("v%d", (id*perG+i)%50)
				table := "t1"
				col := "c1"
				if i%2 == 0 {
					table, col = "t2", "c2"
				}
				out, err := tz.Transform(context.Background(), Event{
					Table: table,
					After: map[string]any{col: value},
				})
				if err != nil {
					t.Errorf("transform: %v", err)
					return
				}
				token := out.After[col].(string)
				mu.Lock()
				if prev, ok := assigned[value]; ok && prev != token {
					t.Errorf("value %s got two tokens: %s and %s", value, prev, token)
				}
				assigned[value] = token
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	count, _ := tz.DomainTokenCount("d")
	if count != 50 {
		t.Fatalf("concurrent distinct tokens = %d, want 50", count)
	}
	tokens, _ := tz.DomainTokens("d")
	used := make([]bool, 51)
	for _, token := range tokens {
		var seq int
		if _, err := fmt.Sscanf(token, "d-%d", &seq); err != nil || seq < 1 || seq > 50 || used[seq] {
			t.Fatalf("invalid or duplicated token %q in %v", token, tokens)
		}
		used[seq] = true
	}
}
