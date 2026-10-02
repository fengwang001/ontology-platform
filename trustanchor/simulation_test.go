package trustanchor

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func formatObservation(raw []Entry, rawSigners [][]byte) string {
	items := make([]string, 0, len(raw)+len(rawSigners))
	for _, item := range raw {
		name := string(item.Key)
		if item.Revoked {
			name += "!"
		}
		items = append(items, name)
	}
	signers := make([]string, len(rawSigners))
	for i, signer := range rawSigners {
		signers[i] = string(signer)
	}
	return "{" + strings.Join(items, ",") + "}/[" + strings.Join(signers, ",") + "]"
}

type simRecord struct {
	status string
	since  int64
	at     int64
	count  int
}

type naiveTracker struct {
	h         int64
	r         int64
	kmax      int
	m         int
	q         int
	last      int64
	keys      map[string]simRecord
	banned    map[string]int64
	processed int64
}

func newNaive(h, r int64, kmax, m, q int, anchors []string) *naiveTracker {
	sim := &naiveTracker{
		h:      h,
		r:      r,
		kmax:   kmax,
		m:      m,
		q:      q,
		keys:   make(map[string]simRecord),
		banned: make(map[string]int64),
	}
	for _, anchor := range anchors {
		sim.keys[anchor] = simRecord{status: StatusValid}
	}
	return sim
}

func (s *naiveTracker) state(key string) KeyState {
	if at, ok := s.banned[key]; ok {
		return KeyState{Status: StatusBanned, At: at}
	}
	if rec, ok := s.keys[key]; ok {
		return KeyState{Status: rec.status, Since: rec.since, At: rec.at, Count: rec.count}
	}
	return KeyState{Status: StatusUntracked}
}

func (s *naiveTracker) observe(raw []Entry, rawSigners [][]byte, now int64) string {
	if now < 0 || now > 1_000_000_000_000_000 || len(raw) < 1 || len(raw) > 64 {
		return ReasonInvalidArgs
	}
	seen := map[string]bool{}
	for _, item := range raw {
		if len(item.Key) == 0 || seen[string(item.Key)] {
			return ReasonInvalidArgs
		}
		seen[string(item.Key)] = true
	}
	for _, signer := range rawSigners {
		if len(signer) == 0 {
			return ReasonInvalidArgs
		}
	}
	if now < s.last {
		return ReasonClockRollback
	}

	trusted := map[string]bool{}
	for _, signer := range rawSigners {
		if rec, ok := s.keys[string(signer)]; ok && rec.status == StatusValid {
			trusted[string(signer)] = true
		}
	}
	if len(trusted) < s.q {
		return ReasonUntrusted
	}

	nextKeys := map[string]simRecord{}
	for id, rec := range s.keys {
		nextKeys[id] = rec
	}
	nextBanned := map[string]int64{}
	for id, at := range s.banned {
		nextBanned[id] = at
	}
	processed := len(s.keys)

	for id, rec := range nextKeys {
		if !seen[id] {
			if rec.status == StatusValid {
				rec.status = StatusMissing
				rec.since = now
				rec.at = 0
				rec.count = 0
				nextKeys[id] = rec
			} else if rec.status == StatusAddPend {
				delete(nextKeys, id)
			}
		}
	}

	for _, item := range raw {
		id := string(item.Key)
		if _, banned := nextBanned[id]; banned {
			continue
		}
		processed++
		rec, tracked := nextKeys[id]
		if item.Revoked {
			if !tracked {
				continue
			}
			if rec.status == StatusValid || rec.status == StatusMissing {
				rec.status = StatusRevoked
				rec.at = now
				rec.since = 0
				rec.count = 0
				nextKeys[id] = rec
			} else if rec.status == StatusAddPend {
				delete(nextKeys, id)
			}
			continue
		}
		if !tracked {
			nextKeys[id] = simRecord{status: StatusAddPend, since: now, count: 1}
			continue
		}
		if rec.status == StatusAddPend {
			rec.count++
			nextKeys[id] = rec
		} else if rec.status == StatusMissing {
			rec.status = StatusValid
			rec.since = 0
			rec.at = 0
			rec.count = 0
			nextKeys[id] = rec
		}
	}

	valid := 0
	for id, rec := range nextKeys {
		if rec.status == StatusAddPend && now >= rec.since+s.h && rec.count >= s.m {
			rec.status = StatusValid
			rec.since = 0
			rec.count = 0
			nextKeys[id] = rec
		}
		if rec.status == StatusValid {
			valid++
		}
		if rec.status == StatusRevoked && now >= rec.at+s.r {
			nextBanned[id] = rec.at
			delete(nextKeys, id)
		}
		if rec.status == StatusMissing && now >= rec.since+s.r {
			delete(nextKeys, id)
		}
	}
	if valid < s.q {
		return ReasonDeadlock
	}
	if len(nextKeys) > s.kmax {
		return ReasonLimit
	}

	s.keys = nextKeys
	s.banned = nextBanned
	s.last = now
	s.processed += int64(processed)
	return ""
}

func TestRandomSequencesMatchNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for sequence := 0; sequence < 2000; sequence++ {
		h := int64(rng.Intn(5))
		r := int64(rng.Intn(8) + 1)
		kmax := 2 + rng.Intn(7)
		m := 1 + rng.Intn(4)
		anchorCount := kmax
		if rng.Intn(3) > 0 {
			anchorCount = 1 + rng.Intn(kmax)
		}
		q := 1 + rng.Intn(anchorCount)
		anchors := make([]string, anchorCount)
		anchorBytes := make([][]byte, anchorCount)
		for i := range anchors {
			anchors[i] = fmt.Sprintf("a%d", i)
			anchorBytes[i] = []byte(anchors[i])
		}

		tracker, err := New(h, r, kmax, m, q, anchorBytes...)
		if err != nil {
			t.Fatalf("sequence %d construction: %v", sequence, err)
		}
		sim := newNaive(h, r, kmax, m, q, anchors)
		t.Logf("sequence=%d H=%d R=%d Kmax=%d M=%d Q=%d anchors=%v", sequence, h, r, kmax, m, q, anchors)

		var now int64
		for step := 0; step < 18; step++ {
			poolSize := anchorCount + 4
			entryCount := 1 + rng.Intn(min(6, poolSize))
			raw := make([]Entry, 0, entryCount)
			ids := rng.Perm(poolSize)
			for i := 0; i < entryCount; i++ {
				raw = append(raw, entry(fmt.Sprintf("a%d", ids[i]), rng.Intn(4) == 0))
			}
			signerCount := rng.Intn(5)
			rawSigners := make([][]byte, signerCount)
			for i := range rawSigners {
				id := fmt.Sprintf("a%d", rng.Intn(anchorCount+5))
				rawSigners[i] = []byte(id)
			}
			if rng.Intn(8) == 0 {
				now--
			} else {
				now += int64(rng.Intn(4))
			}

			err = tracker.Observe(raw, rawSigners, now)
			actualReason := rejectReason(err)
			expectedReason := sim.observe(raw, rawSigners, now)
			decision := "ACCEPT"
			if expectedReason != "" {
				decision = "REJECT"
			}
			if actualReason != expectedReason {
				t.Fatalf("seq=%d step=%d now=%d entries=%v signers=%v => %s got=%q want=%q", sequence, step, now, raw, rawSigners, decision, actualReason, expectedReason)
			}

			for id := 0; id < anchorCount+5; id++ {
				key := fmt.Sprintf("a%d", id)
				if got, want := tracker.State([]byte(key)), sim.state(key); got != want {
					t.Fatalf("seq=%d step=%d now=%d entries=%v signers=%v decision=%s key=%s got=%+v want=%+v", sequence, step, now, raw, rawSigners, decision, key, got, want)
				}
			}
			if tracker.last != sim.last {
				t.Fatalf("seq=%d step=%d clock got=%d want=%d; entries=%v signers=%v", sequence, step, tracker.last, sim.last, raw, rawSigners)
			}
			if tracker.processed != sim.processed {
				t.Fatalf("seq=%d step=%d processed got=%d want=%d; entries=%v signers=%v", sequence, step, tracker.processed, sim.processed, raw, rawSigners)
			}
			t.Logf("seq=%d step=%d input=%s now=%d => %s reason=%q accepted-clock=%d", sequence, step, formatObservation(raw, rawSigners), now, decision, expectedReason, sim.last)
		}
	}
}
