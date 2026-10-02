package ontology

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
)

type naiveState struct {
	maxPath int
	certs   map[string]Certificate
	revoked map[string]int64
	trusted map[string]struct{}
}

type naiveOutcome struct {
	noPath     bool
	path       []string
	failure    *VerifyFailure
	examined   int
	structural [][]string
}

func newNaive(maxPath int) *naiveState {
	return &naiveState{
		maxPath: maxPath,
		certs:   make(map[string]Certificate),
		revoked: make(map[string]int64),
		trusted: make(map[string]struct{}),
	}
}

func (state *naiveState) add(cert Certificate) bool {
	id := string(cert.ID)
	if _, exists := state.certs[id]; exists {
		return false
	}
	state.certs[id] = cert
	return true
}

func (state *naiveState) candidates(cert Certificate) []Certificate {
	var result []Certificate
	for _, candidate := range state.certs {
		id := string(candidate.ID)
		if id == string(cert.ID) || !bytes.Equal(candidate.Subject, cert.Issuer) || !bytes.Equal(candidate.Key, cert.AuthKey) {
			continue
		}
		result = append(result, candidate)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].NotAfter != result[right].NotAfter {
			return result[left].NotAfter > result[right].NotAfter
		}
		return bytes.Compare(result[left].ID, result[right].ID) < 0
	})
	return result
}

func (state *naiveState) verify(leafID, name string, now int64) naiveOutcome {
	var structural [][]string
	examined := 0
	var enumerate func(path []string, seen map[string]struct{})
	enumerate = func(path []string, seen map[string]struct{}) {
		currentID := path[len(path)-1]
		if _, trusted := state.trusted[currentID]; trusted {
			structural = append(structural, append([]string(nil), path...))
			return
		}
		if len(path) >= state.maxPath {
			if len(path) == state.maxPath {
				examined += len(state.candidates(state.certs[currentID]))
			}
			return
		}
		for _, candidate := range state.candidates(state.certs[currentID]) {
			examined++
			id := string(candidate.ID)
			if _, repeats := seen[id]; repeats {
				continue
			}
			if len(path)+1 > state.maxPath {
				continue
			}
			seen[id] = struct{}{}
			enumerate(append(path, id), seen)
			delete(seen, id)
		}
	}
	enumerate([]string{leafID}, map[string]struct{}{leafID: {}})

	outcome := naiveOutcome{noPath: len(structural) == 0, examined: examined, structural: structural}
	if len(structural) == 0 {
		return outcome
	}
	for _, pathIDs := range structural {
		pathIDs, failure := state.validate(pathIDs, name, now)
		if failure == nil {
			outcome.path = pathIDs
			return outcome
		}
		if outcome.failure == nil {
			outcome.failure = failure
		}
	}
	return outcome
}

func (state *naiveState) validate(ids []string, name string, now int64) ([]string, *VerifyFailure) {
	path := make([]Certificate, len(ids))
	for idx, id := range ids {
		path[idx] = state.certs[id]
	}
	failure := func(reason FailureReason, idx int) *VerifyFailure {
		pathBytes := make([]Bytes, len(ids))
		for i, id := range ids {
			pathBytes[i] = Bytes(id)
		}
		return &VerifyFailure{Reason: reason, CertIdx: idx, Path: pathBytes}
	}
	for idx, cert := range path {
		if now < cert.NotBefore {
			return ids, failure(FailureNotYetValid, idx)
		}
		if now >= cert.NotAfter {
			return ids, failure(FailureExpired, idx)
		}
		if revokedAt, ok := state.revoked[ids[idx]]; ok && now >= revokedAt {
			return ids, failure(FailureRevoked, idx)
		}
	}
	for idx := 1; idx < len(path); idx++ {
		if !path[idx].IsCA {
			return ids, failure(FailureNotCA, idx)
		}
	}
	for idx := 1; idx < len(path); idx++ {
		if path[idx].PathLen >= 0 {
			count := 0
			for inner := 1; inner < idx; inner++ {
				if !bytes.Equal(path[inner].Subject, path[inner].Issuer) {
					count++
				}
			}
			if count > path[idx].PathLen {
				return ids, failure(FailurePathLenExceeded, idx)
			}
		}
	}
	for idx := 1; idx < len(path); idx++ {
		for _, subtree := range path[idx].Excluded {
			if subtreeMatch(name, subtree) {
				return ids, failure(FailureExcludedDNS, idx)
			}
		}
		if len(path[idx].Permitted) > 0 {
			allowed := false
			for _, subtree := range path[idx].Permitted {
				if subtreeMatch(name, subtree) {
					allowed = true
				}
			}
			if !allowed {
				return ids, failure(FailurePermittedDNS, idx)
			}
		}
	}
	for _, san := range path[0].SAN {
		if sanMatch(name, san) {
			return ids, nil
		}
	}
	return ids, failure(FailureSANMismatch, 0)
}

func TestRandomSequencesAgainstNaiveSimulation(t *testing.T) {
	if !testing.Verbose() {
		t.Log("use -v to print all 2000 random inputs, outputs, and decisions; summary is always checked")
	}
	const iterations = 2000
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewPCG(uint64(iter+1), uint64(99_999-iter)))
		limit := 1 + rng.IntN(8)
		v, _ := New(limit, 80)
		model := newNaive(limit)
		var log strings.Builder
		fmt.Fprintf(&log, "case=%d L=%d", iter, limit)

		type nodeInfo struct{ subject, key string }
		nodes := []nodeInfo{{"root", "k0"}}
		for i := 1; i < 7; i++ {
			nodes = append(nodes, nodeInfo{fmt.Sprintf("s%d", i), fmt.Sprintf("k%d", i)})
		}

		for step := 0; step < 42; step++ {
			maker := rng.IntN(10)
			switch {
			case maker < 7:
				isCA := rng.IntN(100) < 72
				issuer := nodes[rng.IntN(len(nodes))]
				var subject, key string
				if isCA && rng.IntN(3) == 0 {
					subject = issuer.subject
					key = fmt.Sprintf("cross-%d-%d", step, iter%17)
				} else {
					subject = fmt.Sprintf("x-%d-%d", iter%11, step)
					key = fmt.Sprintf("key-%d-%d", iter%13, step)
				}
				notBefore := int64(rng.IntN(120))
				notAfter := notBefore + 1 + int64(rng.IntN(180))
				cert := Certificate{
					ID: Bytes(fmt.Sprintf("c-%d-%d", iter, step)), Subject: Bytes(subject), Issuer: Bytes(issuer.subject),
					Key: Bytes(key), AuthKey: Bytes(issuer.key), NotBefore: notBefore, NotAfter: notAfter,
					IsCA: isCA, PathLen: -1,
				}
				if isCA {
					cert.PathLen = []int{-1, 0, 1, 2}[rng.IntN(4)]
					if rng.IntN(2) == 0 {
						base := []string{"example.com", ".example.com", "test.org", ".test.org"}[rng.IntN(4)]
						cert.Permitted = []string{base}
					}
					if rng.IntN(3) == 0 {
						cert.Excluded = []string{"bad.example.com"}
					}
					nodes = append(nodes, nodeInfo{subject, key})
				} else {
					cert.SAN = []string{"*.example.com", "leaf.test.org"}
				}
				actualErr := v.Add(cert)
				modelOK := model.add(cert)
				fmt.Fprintf(&log, "\nadd id=%s sub=%s iss=%s key=%s auth=%s [%d,%d) ca=%t path=%d permitted=%v excluded=%v san=%v => err=%v model=%t",
					cert.ID, subject, issuer.subject, key, issuer.key, notBefore, notAfter, isCA, cert.PathLen, cert.Permitted, cert.Excluded, cert.SAN, actualErr, modelOK)
			case maker == 7:
				if len(model.certs) == 0 {
					continue
				}
				id := randomCertID(rng, model)
				actualErr := v.Trust(Bytes(id))
				model.trusted[id] = struct{}{}
				fmt.Fprintf(&log, "\ntrust %s => err=%v", id, actualErr)
			case maker == 8:
				if len(model.certs) == 0 {
					continue
				}
				id := randomCertID(rng, model)
				at := int64(rng.IntN(300))
				actualErr := v.Revoke(Bytes(id), at)
				if old, ok := model.revoked[id]; !ok || at < old {
					model.revoked[id] = at
				}
				fmt.Fprintf(&log, "\nrevoke %s at=%d => err=%v", id, at, actualErr)
			default:
				if len(model.certs) == 0 {
					continue
				}
				id := randomCertID(rng, model)
				name := randomDNSName(rng)
				now := int64(rng.IntN(300))
				actual, actualErr := v.Verify(Bytes(id), name, now)
				if actualErr != nil {
					t.Fatalf("case %d step %d unexpected error: %v\n%s", iter, step, actualErr, log.String())
				}
				expected := model.verify(id, name, now)
				fmt.Fprintf(&log, "\nverify leaf=%s name=%s now=%d => actual noPath=%t path=%v failure=%#v examined=%d; naive structural=%v selected=%v failure=%#v examined=%d; decision=fixed DFS first passing structural path otherwise first discovered failure",
					id, name, now, actual.NoPath, actual.Path, actual.Failure, actual.ExaminedCandidates(),
					expected.structural, expected.path, expected.failure, expected.examined)
				compareOutcome(t, iter, step, name, now, actual, expected, log.String())
			}
		}
		if testing.Verbose() {
			t.Log(log.String())
		}
	}
}

func randomCertID(rng *rand.Rand, state *naiveState) string {
	ids := make([]string, 0, len(state.certs))
	for id := range state.certs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids[rng.IntN(len(ids))]
}

func randomDNSName(rng *rand.Rand) string {
	base := []string{"example.com", "test.org", "bad.example.com", "other.net"}[rng.IntN(4)]
	labels := []string{"a", "b", "leaf"}
	count := rng.IntN(3)
	name := base
	for i := 0; i < count; i++ {
		name = labels[rng.IntN(len(labels))] + "." + name
	}
	return name
}

func compareOutcome(t *testing.T, iter, step int, name string, now int64, actual VerifyResult, expected naiveOutcome, log string) {
	t.Helper()
	if actual.NoPath != expected.noPath {
		t.Fatalf("case %d step %d noPath actual=%t expected=%t\n%s", iter, step, actual.NoPath, expected.noPath, log)
	}
	if actual.ExaminedCandidates() != expected.examined {
		t.Fatalf("case %d step %d examined actual=%d expected=%d name=%s now=%d\n%s", iter, step, actual.ExaminedCandidates(), expected.examined, name, now, log)
	}
	if expected.path != nil {
		actualIDs := stringsToBytes(actual.Path)
		if strings.Join(actualIDs, ",") != strings.Join(expected.path, ",") {
			t.Fatalf("case %d step %d path actual=%v expected=%v\n%s", iter, step, actualIDs, expected.path, log)
		}
		return
	}
	if (actual.Failure == nil) != (expected.failure == nil) {
		t.Fatalf("case %d step %d failure actual=%#v expected=%#v\n%s", iter, step, actual.Failure, expected.failure, log)
	}
	if actual.Failure != nil {
		if actual.Failure.Reason != expected.failure.Reason || actual.Failure.CertIdx != expected.failure.CertIdx {
			t.Fatalf("case %d step %d failure actual=%#v expected=%#v\n%s", iter, step, actual.Failure, expected.failure, log)
		}
		actualPath := stringsToBytes(actual.Failure.Path)
		expectedPath := stringsToBytes(expected.failure.Path)
		if strings.Join(actualPath, ",") != strings.Join(expectedPath, ",") {
			t.Fatalf("case %d step %d failure path actual=%v expected=%v\n%s", iter, step, actualPath, expectedPath, log)
		}
	}
}

func stringsToBytes(values []Bytes) []string {
	result := make([]string, len(values))
	for idx, value := range values {
		result[idx] = string(value)
	}
	return result
}
