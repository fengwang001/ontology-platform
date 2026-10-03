package gate_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/gate"
	"ontology/introspect"
)

var errUpstream = errors.New("scripted upstream")

type naiveResult struct {
	active bool
	sub    string
	iat    int64
	exp    int64
	scopes []string
}

type naiveEntry struct {
	result naiveResult
	f0     int64
	until  int64
}

type naive struct {
	p, n, g int64
	values  map[string]naiveResult
	fails   map[string]int
	calls   int64
	cache   map[string]naiveEntry
	nb      map[string]int64
	maxNow  int64
}

type naiveDecision struct {
	verdict gate.Verdict
	source  gate.Source
	missing string
	err     error
}

func newNaive(p, n, g int64, values map[string]naiveResult, fails map[string]int) *naive {
	return &naive{p: p, n: n, g: g, values: values, fails: fails, cache: map[string]naiveEntry{}, nb: map[string]int64{}}
}

func (m *naive) check(token string, need []string, now int64) naiveDecision {
	if token == "" || len(need) == 0 || hasEmpty(need) {
		return naiveDecision{err: gate.ErrInvalidArgument}
	}
	if now < 0 || now > 100_000_000_000_000 {
		return naiveDecision{err: gate.ErrInvalidTime}
	}
	if now < m.maxNow {
		return naiveDecision{err: gate.ErrClockRollback}
	}
	m.maxNow = now

	source := gate.Fresh
	entry, fresh := m.cache[token]
	if fresh && now < entry.until {
		source = gate.Cache
	} else {
		m.calls++
		if m.fails[token] > 0 {
			m.fails[token]--
			source = gate.None
			stale, hasStale := m.cache[token]
			canStale := hasStale && stale.result.active && stale.until <= now && now < stale.until+m.g && now < stale.result.exp
			if highRisk(need) || !canStale {
				return naiveDecision{verdict: gate.Unavailable, source: gate.None}
			}
			entry = stale
			source = gate.Stale
		} else {
			result := m.values[token]
			entry = naiveEntry{result: result, f0: now, until: now + m.n}
			if result.active {
				entry.until = now + m.p
				if result.exp < entry.until {
					entry.until = result.exp
				}
			}
			m.cache[token] = entry
		}
	}

	result := entry.result
	if !result.active {
		return naiveDecision{verdict: gate.Inactive, source: source}
	}
	revokedAt := int64(-1)
	if value, ok := m.nb[result.sub]; ok {
		revokedAt = value
	}
	if result.iat <= revokedAt {
		return naiveDecision{verdict: gate.Revoked, source: source}
	}
	if now >= result.exp {
		return naiveDecision{verdict: gate.Expired, source: source}
	}
	if missing := firstMissing(result.scopes, need); missing != "" {
		return naiveDecision{verdict: gate.Scope, source: source, missing: missing}
	}
	return naiveDecision{verdict: gate.Allow, source: source}
}

func (m *naive) revoke(sub string, at, now int64) error {
	if sub == "" || at < 0 || at > 100_000_000_000_000 {
		return gate.ErrInvalidArgument
	}
	if now < 0 || now > 100_000_000_000_000 {
		return gate.ErrInvalidTime
	}
	if now < m.maxNow {
		return gate.ErrClockRollback
	}
	m.maxNow = now
	revokedAt := int64(-1)
	if value, ok := m.nb[sub]; ok {
		revokedAt = value
	}
	if at > revokedAt {
		m.nb[sub] = at
	}
	return nil
}

func hasEmpty(values []string) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}

func highRisk(need []string) bool {
	for _, item := range need {
		last := item
		for i := len(item) - 1; i >= 0; i-- {
			if item[i] == ':' {
				last = item[i+1:]
				break
			}
		}
		if last == "write" || last == "admin" {
			return true
		}
	}
	return false
}

func covers(granted, wanted string) bool {
	return granted == wanted || len(wanted) > len(granted) && wanted[:len(granted)] == granted && wanted[len(granted)] == ':'
}

func firstMissing(granted, need []string) string {
outer:
	for _, wanted := range need {
		for _, have := range granted {
			if covers(have, wanted) {
				continue outer
			}
		}
		return wanted
	}
	return ""
}

func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	scopeWords := []string{"orders", "orders:read", "orders:write", "orders:write:x", "billing", "billing:read", "ordersx"}
	for iteration := 0; iteration < 2000; iteration++ {
		values := map[string]naiveResult{}
		results := map[string]introspect.Result{}
		tokens := []string{"t0", "t1", "t2", "t3"}
		for index, token := range tokens {
			active := rng.Intn(6) > 0
			iat := int64(rng.Intn(8))
			exp := int64(8 + rng.Intn(30))
			scopes := []string{scopeWords[rng.Intn(len(scopeWords))]}
			if rng.Intn(2) == 0 {
				scopes = append(scopes, scopeWords[rng.Intn(len(scopeWords))])
			}
			sub := fmt.Sprintf("u%d", rng.Intn(3))
			values[token] = naiveResult{active, sub, iat, exp, scopes}
			if active {
				results[token] = activeResult(sub, iat, exp, scopes...)
			}
			_ = index
		}

		failMap := map[string]int{}
		for _, token := range tokens {
			if rng.Intn(2) == 0 {
				failMap[token] = rng.Intn(3)
			}
		}
		p, n, g := int64(2+rng.Intn(7)), int64(2+rng.Intn(5)), int64(1+rng.Intn(9))
		model := newNaive(p, n, g, values, cloneFails(failMap))
		up := newScripted(results, cloneFails(failMap))
		actual, err := gate.New(p, n, g, up.call)
		if err != nil {
			t.Fatal(err)
		}

		now := int64(0)
		for step := 0; step < 18; step++ {
			now += int64(rng.Intn(5))
			token := tokens[rng.Intn(len(tokens))]
			need := []string{scopeWords[rng.Intn(len(scopeWords))]}
			if rng.Intn(2) == 0 {
				need = append(need, scopeWords[rng.Intn(len(scopeWords))])
			}
			if rng.Intn(7) == 0 {
				sub := fmt.Sprintf("u%d", rng.Intn(3))
				at := int64(rng.Intn(10))
				wantErr := model.revoke(sub, at, now)
				gotErr := actual.Revoke(sub, at, now)
				if !sameError(wantErr, gotErr) {
					t.Fatalf("iter=%d step=%d revoke errors model=%v actual=%v", iteration, step, wantErr, gotErr)
				}
				t.Logf("iter=%d input Revoke sub=%q at=%d now=%d output err=%v", iteration, sub, at, now, gotErr)
				continue
			}

			want := model.check(token, need, now)
			got, gotErr := actual.Check(token, need, now)
			t.Logf("iter=%d input Check token=%q need=%v now=%d output verdict=%s source=%s missing=%q err=%v basis=%s calls=%d",
				iteration, token, need, now, got.Verdict, got.Source, got.Missing, gotErr, got.Reason, actual.Calls())
			if !sameDecision(want, got, gotErr) || model.calls != actual.Calls() {
				t.Fatalf("iter=%d step=%d mismatch model=%+v modelCalls=%d actual=%+v actualCalls=%d",
					iteration, step, want, model.calls, got, actual.Calls())
			}
		}
	}
}

type scripted struct {
	results map[string]introspect.Result
	fails   map[string]int
}

func newScripted(results map[string]introspect.Result, fails map[string]int) *scripted {
	return &scripted{results: results, fails: fails}
}

func (s *scripted) call(token string) (introspect.Result, error) {
	if s.fails[token] > 0 {
		s.fails[token]--
		return introspect.Result{}, errUpstream
	}
	return s.results[token], nil
}

func cloneFails(input map[string]int) map[string]int {
	output := make(map[string]int, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func sameError(want, got error) bool {
	return want == got
}

func sameDecision(want naiveDecision, got gate.Decision, gotErr error) bool {
	return want.err == gotErr && want.verdict == got.Verdict && want.source == got.Source && want.missing == got.Missing
}
