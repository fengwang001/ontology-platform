package pbft

import (
	"math/rand"
	"reflect"
	"testing"
)

type naiveLog struct {
	f        int
	n        int
	view     int64
	limit    int64
	primary  int
	executed int64
	entries  map[int64]*naiveEntry
}

type naiveEntry struct {
	prePrepare    string
	hasPrePrepare bool
	prepares      map[int]string
	commits       map[int]string
}

func newNaiveLog(f int, view int64, limit int64) *naiveLog {
	return &naiveLog{
		f:       f,
		n:       3*f + 1,
		view:    view,
		limit:   limit,
		primary: int(uint64(view) % uint64(3*f+1)),
		entries: make(map[int64]*naiveEntry),
	}
}

func (l *naiveLog) entry(seq int64) *naiveEntry {
	entry := l.entries[seq]
	if entry == nil {
		entry = &naiveEntry{
			prepares: make(map[int]string),
			commits:  make(map[int]string),
		}
		l.entries[seq] = entry
	}
	return entry
}

func (l *naiveLog) observe(msg Message) error {
	if msg.From < 0 || msg.From >= l.n {
		return ErrSenderOutOfRange
	}
	if msg.Digest == "" {
		return ErrEmptyDigest
	}
	if msg.View != l.view {
		return ErrWrongView
	}
	if msg.Seq <= l.executed || msg.Seq > l.executed+l.limit {
		return ErrSeqOutOfWindow
	}

	switch msg.Kind {
	case PrePrepare:
		if msg.From != l.primary {
			return ErrPrePrepareFromBackup
		}
	case Prepare:
		if msg.From == l.primary {
			return ErrPrepareFromPrimary
		}
	case Commit:
	default:
		return ErrUnknownMessageKind
	}

	entry := l.entry(msg.Seq)
	switch msg.Kind {
	case PrePrepare:
		if entry.hasPrePrepare && entry.prePrepare != msg.Digest {
			return ErrPrePrepareDigestClash
		}
		entry.hasPrePrepare = true
		entry.prePrepare = msg.Digest
	case Prepare:
		if digest, ok := entry.prepares[msg.From]; ok && digest != msg.Digest {
			return ErrPrepareDigestClash
		}
		entry.prepares[msg.From] = msg.Digest
	case Commit:
		if digest, ok := entry.commits[msg.From]; ok && digest != msg.Digest {
			return ErrCommitDigestClash
		}
		entry.commits[msg.From] = msg.Digest
	}
	return nil
}

func (l *naiveLog) execute() []Execution {
	var result []Execution
	for seq := l.executed + 1; ; seq++ {
		entry := l.entries[seq]
		if entry == nil || !entry.hasPrePrepare {
			break
		}

		prepareCount := 0
		for from, digest := range entry.prepares {
			if from != l.primary && digest == entry.prePrepare {
				prepareCount++
			}
		}
		commitCount := 0
		for _, digest := range entry.commits {
			if digest == entry.prePrepare {
				commitCount++
			}
		}
		if prepareCount < 2*l.f || commitCount < 2*l.f+1 {
			break
		}

		result = append(result, Execution{Seq: seq, Digest: entry.prePrepare})
		delete(l.entries, seq)
		l.executed = seq
	}

	out := make([]Execution, len(result))
	copy(out, result)
	return out
}

func TestRandomSequencesMatchNaiveLog(t *testing.T) {
	for seed := int64(1); seed <= 80; seed++ {
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			f := 1 + rng.Intn(2)
			limit := int64(1 + rng.Intn(4))
			primary := int(uint64(7) % uint64(3*f+1))
			actual := mustNewLog(t, f, 7, int(limit))
			model := newNaiveLog(f, 7, limit)

			for step := 0; step < 260; step++ {
				msg := randomMessage(rng, f, 3*f, primary, actual.executed, limit, step)
				actualErr := actual.Observe(msg)
				modelErr := model.observe(msg)
				t.Logf("seed=%d input=%#v output-error=%v decision=%s", seed, msg, actualErr, observeDecision(actualErr))
				if !sameSentinel(actualErr, modelErr) {
					t.Fatalf("error mismatch: actual=%v model=%v", actualErr, modelErr)
				}

				if rng.Intn(4) == 0 {
					actualResult := actual.Execute()
					modelResult := model.execute()
					t.Logf("seed=%d input=Execute output=%#v decision=%s", seed, actualResult, executeDecision(actualResult))
					if !reflect.DeepEqual(actualResult, modelResult) {
						t.Fatalf("execute mismatch: actual=%#v model=%#v", actualResult, modelResult)
					}
				}
			}

			actualResult := actual.Execute()
			modelResult := model.execute()
			t.Logf("seed=%d input=final Execute output=%#v decision=%s", seed, actualResult, executeDecision(actualResult))
			if !reflect.DeepEqual(actualResult, modelResult) || actual.executed != model.executed {
				t.Fatalf("final mismatch: actual=%#v/%d model=%#v/%d", actualResult, actual.executed, modelResult, model.executed)
			}
		})
	}
}

func TestAcceptedMessageSetIsOrderIndependent(t *testing.T) {
	messages := []Message{
		{Commit, 0, 1, "a", 2},
		{Commit, 0, 1, "a", 0},
		{Commit, 0, 2, "b", 1},
		{Commit, 0, 2, "b", 2},
		{Commit, 0, 2, "b", 0},
		{Prepare, 0, 1, "a", 1},
		{PrePrepare, 0, 2, "b", 0},
		{Commit, 0, 1, "a", 1},
		{Prepare, 0, 2, "b", 2},
		{Prepare, 0, 1, "a", 2},
		{Prepare, 0, 2, "b", 1},
		{PrePrepare, 0, 1, "a", 0},
	}
	want := []Execution{{1, "a"}, {2, "b"}}

	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed + 1000))
		shuffled := append([]Message(nil), messages...)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		log := mustNewLog(t, 1, 0, 2)
		for _, msg := range shuffled {
			if err := log.Observe(msg); err != nil {
				t.Fatalf("seed=%d Observe(%#v): %v", seed, msg, err)
			}
		}
		result := log.Execute()
		t.Logf("seed=%d input=%#v output=%#v decision=order-independent accepted set", seed, shuffled, result)
		if !reflect.DeepEqual(result, want) || log.executed != 2 {
			t.Fatalf("seed=%d result=%#v executed=%d, want %#v and 2", seed, result, log.executed, want)
		}
	}
}

func randomMessage(rng *rand.Rand, f, n, primary int, executed, limit int64, step int) Message {
	kinds := []MessageKind{PrePrepare, Prepare, Commit, Commit, MessageKind(99)}
	kind := kinds[rng.Intn(len(kinds))]
	from := rng.Intn(n + 2)
	if from == n+1 {
		from = -1
	}

	digests := []string{"a", "b", "c", ""}
	digest := digests[rng.Intn(len(digests))]
	view := int64(7)
	if step%17 == 0 {
		view = 8
	}

	seqChoices := []int64{executed, executed + 1, executed + limit, executed + limit + 1, executed + int64(rng.Intn(int(limit)+2))}
	seq := seqChoices[rng.Intn(len(seqChoices))]

	if step%5 == 0 {
		switch kind {
		case PrePrepare:
			from = primary
		case Prepare:
			if n > 1 {
				from = (primary + 1) % n
			}
		}
		digest = "a"
		view = 7
		seq = executed + 1 + int64(rng.Intn(int(limit)))
	}

	return Message{Kind: kind, View: view, Seq: seq, Digest: digest, From: from}
}

func sameSentinel(actual, model error) bool {
	if actual == nil || model == nil {
		return actual == model
	}
	return actual.Error() == model.Error()
}
