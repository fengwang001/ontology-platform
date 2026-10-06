package certselector

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// modelResult is the naive model's classification of one selection.
type modelResult struct {
	id     string
	source Source
	err    error
}

// naiveModel is an intentionally simple, independent reference implementation
// that scans every certificate on every selection.
type naiveModel struct {
	certs    map[string]Certificate
	defaults string
}

func newNaiveModel() *naiveModel {
	return &naiveModel{certs: map[string]Certificate{}}
}

func validateCertModel(c Certificate) error {
	if c.ID == "" || len(c.Names) == 0 {
		return ErrInvalidArgument
	}
	if c.Key != ECDSA && c.Key != RSA {
		return ErrInvalidArgument
	}
	if c.NotBefore < 0 || c.NotAfter < 0 || c.NotBefore >= c.NotAfter {
		return ErrInvalidArgument
	}
	for _, san := range c.Names {
		if _, _, err := parseSAN(san); err != nil {
			return err
		}
	}
	return nil
}

func (m *naiveModel) add(c Certificate) error {
	if err := validateCertModel(c); err != nil {
		return err
	}
	if _, ok := m.certs[c.ID]; ok {
		return ErrConflict
	}
	cp := c
	cp.Names = append([]string(nil), c.Names...)
	m.certs[c.ID] = cp
	return nil
}

func (m *naiveModel) remove(id string) error {
	if _, ok := m.certs[id]; !ok {
		return ErrNotFound
	}
	delete(m.certs, id)
	if m.defaults == id {
		m.defaults = ""
	}
	return nil
}

func (m *naiveModel) setDefault(id string) error {
	if id == "" {
		return ErrInvalidArgument
	}
	if _, ok := m.certs[id]; !ok {
		return ErrNotFound
	}
	m.defaults = id
	return nil
}

func (m *naiveModel) clearDefault() { m.defaults = "" }

func (m *naiveModel) selectCert(name string, supported map[KeyType]bool, now int64) modelResult {
	if len(supported) == 0 || now < 0 {
		return modelResult{err: ErrInvalidArgument}
	}
	for k := range supported {
		if k != ECDSA && k != RSA {
			return modelResult{err: ErrInvalidArgument}
		}
	}

	type cand struct {
		cert   Certificate
		source Source
	}
	var candidates []cand
	hadMatch := false

	if name != "" {
		norm, err := normalizeName(name)
		if err != nil {
			return modelResult{err: ErrInvalidArgument}
		}
		var exactIDs, wildIDs []string
		for _, c := range m.certs {
			for _, san := range c.Names {
				base, wildcard, perr := parseSAN(san)
				if perr != nil {
					continue
				}
				if wildcard {
					if wildcardMatch(norm, base) {
						wildIDs = append(wildIDs, c.ID)
					}
				} else if base == norm {
					exactIDs = append(exactIDs, c.ID)
				}
			}
		}
		if len(exactIDs) > 0 {
			hadMatch = true
			for _, id := range uniqSorted(exactIDs) {
				candidates = append(candidates, cand{m.certs[id], SourceExact})
			}
		} else if len(wildIDs) > 0 {
			hadMatch = true
			for _, id := range uniqSorted(wildIDs) {
				candidates = append(candidates, cand{m.certs[id], SourceWildcard})
			}
		}
	}

	if !hadMatch {
		if m.defaults == "" {
			return modelResult{err: ErrNoMatch, source: SourceDefault}
		}
		candidates = []cand{{m.certs[m.defaults], SourceDefault}}
	}

	var timeValid []cand
	var usable []cand
	for _, cn := range candidates {
		if now >= cn.cert.NotBefore && now < cn.cert.NotAfter {
			timeValid = append(timeValid, cn)
			if supported[cn.cert.Key] {
				usable = append(usable, cn)
			}
		}
	}
	if len(usable) == 0 {
		if len(timeValid) > 0 {
			return modelResult{err: ErrUnsupportedKey, source: candidates[0].source}
		}
		return modelResult{err: ErrExpired, source: candidates[0].source}
	}
	sort.Slice(usable, func(i, j int) bool { return prefer(usable[i].cert, usable[j].cert) })
	return modelResult{id: usable[0].cert.ID, source: usable[0].source}
}

func uniqSorted(in []string) []string {
	set := map[string]struct{}{}
	for _, s := range in {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ---- random differential test ----

type diffLogger struct {
	sb strings.Builder
}

func (l *diffLogger) logf(format string, args ...any) {
	fmt.Fprintf(&l.sb, format+"\n", args...)
}

var diffLabels = []string{"alpha", "beta", "gamma", "delta", "host", "a", "b", "foo", "bar", "api", "cdn", "mail"}
var diffZones = []string{"com", "org", "net", "io", "dev"}

func randomName(rng *rand.Rand) string {
	n := 1 + rng.Intn(3)
	parts := make([]string, 0, n+1)
	for i := 0; i < n; i++ {
		parts = append(parts, diffLabels[rng.Intn(len(diffLabels))])
	}
	parts = append(parts, diffZones[rng.Intn(len(diffZones))])
	name := strings.Join(parts, ".")
	switch rng.Intn(6) {
	case 0:
		name = strings.ToUpper(name)
	case 1:
		name = name + "."
	case 2:
		name = strings.ToUpper(name) + "."
	}
	return name
}

func randomSAN(rng *rand.Rand) string {
	name := strings.TrimSuffix(strings.ToLower(randomName(rng)), ".")
	parts := strings.Split(name, ".")
	if rng.Intn(3) == 0 && len(parts) >= 3 {
		return "*." + strings.Join(parts[1:], ".")
	}
	return name
}

func randomBadSAN(rng *rand.Rand) string {
	base := randomSAN(rng)
	switch rng.Intn(7) {
	case 0:
		return "*"
	case 1:
		return "*.com"
	case 2:
		return "a.*.com"
	case 3:
		return "a*.com"
	case 4:
		return "*a.com"
	case 5:
		return "*.example.*"
	default:
		return base + ".."
	}
}

func TestRandomDifferential(t *testing.T) {
	const steps = 1200
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("random seed: %d, steps: %d", seed, steps)

	s := NewSelector()
	m := newNaiveModel()
	logger := &diffLogger{}
	logger.logf("=== differential run seed=%d steps=%d ===", seed, steps)
	defer func() {
		if path := os.Getenv("CERTSELECTOR_LOG"); path != "" {
			_ = os.WriteFile(path, []byte(logger.sb.String()), 0o644)
		}
	}()

	var ids []string
	for step := 0; step < steps; step++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // add
			id := fmt.Sprintf("c%03d", rng.Intn(60))
			nameCount := 1 + rng.Intn(3)
			names := make([]string, 0, nameCount)
			for i := 0; i < nameCount; i++ {
				if rng.Intn(8) == 0 {
					names = append(names, randomBadSAN(rng))
				} else {
					names = append(names, randomSAN(rng))
				}
			}
			key := ECDSA
			if rng.Intn(2) == 0 {
				key = RSA
			}
			nb := int64(rng.Intn(200))
			na := nb + int64(1+rng.Intn(300))
			cert := Certificate{ID: id, Names: names, Key: key, NotBefore: nb, NotAfter: na}
			got := s.Add(cert)
			want := m.add(cert)
			logger.logf("step %4d ADD id=%s names=%v key=%v [%d,%d) -> got=%v want=%v basis={validate;id-unique}",
				step, id, names, key, nb, na, got, want)
			if errKind(got) != errKind(want) {
				t.Fatalf("step %d Add mismatch: got=%v want=%v", step, got, want)
			}
			if got == nil {
				ids = uniqSorted(append(ids, id))
			}
		case 4: // remove
			id := fmt.Sprintf("c%03d", rng.Intn(70))
			got := s.Remove(id)
			want := m.remove(id)
			logger.logf("step %4d REMOVE id=%s -> got=%v want=%v", step, id, got, want)
			if errKind(got) != errKind(want) {
				t.Fatalf("step %d Remove mismatch: got=%v want=%v", step, got, want)
			}
			next := ids[:0]
			for _, x := range ids {
				if x != id {
					next = append(next, x)
				}
			}
			ids = next
		case 5: // set default
			id := "ghost"
			if len(ids) > 0 && rng.Intn(5) != 0 {
				id = ids[rng.Intn(len(ids))]
			}
			got := s.SetDefault(id)
			want := m.setDefault(id)
			logger.logf("step %4d SETDEFAULT id=%s -> got=%v want=%v", step, id, got, want)
			if errKind(got) != errKind(want) {
				t.Fatalf("step %d SetDefault mismatch: got=%v want=%v", step, got, want)
			}
		case 6:
			s.ClearDefault()
			m.clearDefault()
			logger.logf("step %4d CLEARDEFAULT -> ok", step)
		default: // select
			name := ""
			if rng.Intn(6) != 0 {
				name = randomName(rng)
			}
			supported := map[KeyType]bool{}
			switch rng.Intn(4) {
			case 0:
				supported[ECDSA] = true
			case 1:
				supported[RSA] = true
			case 2:
				supported[ECDSA] = true
				supported[RSA] = true
			}
			now := int64(rng.Intn(500))
			if rng.Intn(20) == 0 {
				now = -1
			}
			sel, gotErr := s.Select(name, supported, now)
			want := m.selectCert(name, supported, now)
			gotID, gotSrc := "", Source(-1)
			if gotErr == nil {
				gotID = sel.Certificate.ID
				gotSrc = sel.Source
			}
			logger.logf("step %4d SELECT name=%q supported=%v now=%d -> got=(id=%s src=%s err=%v) want=(id=%s src=%s err=%v) basis={exact>wild>default;ec>rsa>notafter>id}",
				step, name, keySet(supported), now,
				gotID, srcName(gotSrc), gotErr,
				want.id, srcName(want.source), want.err)
			if errKind(gotErr) != errKind(want.err) {
				t.Fatalf("step %d Select error mismatch: got=%v want=%v", step, gotErr, want.err)
			}
			if gotErr == nil && (gotID != want.id || gotSrc != want.source) {
				t.Fatalf("step %d Select result mismatch: got=(%s,%s) want=(%s,%s)",
					step, gotID, srcName(gotSrc), want.id, srcName(want.source))
			}
		}
	}

	if path := os.Getenv("CERTSELECTOR_LOG"); path != "" {
		if err := os.WriteFile(path, []byte(logger.sb.String()), 0o644); err != nil {
			t.Logf("write log failed: %v", err)
		} else {
			t.Logf("differential log written to %s (%d bytes)", path, logger.sb.Len())
		}
	}
	t.Logf("differential trace (%d chars):\n%s", logger.sb.Len(), logger.sb.String())
}

func keySet(m map[KeyType]bool) []string {
	var out []string
	if m[ECDSA] {
		out = append(out, "EC")
	}
	if m[RSA] {
		out = append(out, "RSA")
	}
	return out
}

func srcName(s Source) string {
	switch s {
	case SourceExact:
		return "exact"
	case SourceWildcard:
		return "wildcard"
	case SourceDefault:
		return "default"
	default:
		return "-"
	}
}

func errKind(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errorIs(err, ErrInvalidArgument):
		return "invalid"
	case errorIs(err, ErrConflict):
		return "conflict"
	case errorIs(err, ErrNotFound):
		return "notfound"
	case errorIs(err, ErrNoMatch):
		return "nomatch"
	case errorIs(err, ErrExpired):
		return "expired"
	case errorIs(err, ErrUnsupportedKey):
		return "unsupported"
	default:
		return "unknown"
	}
}

func errorIs(err, target error) bool {
	return err == target || strings.Contains(err.Error(), target.Error())
}
