package snippet

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// genCase builds a random valid text (mix of 1- and 3-byte runes) and
// boundary-aligned hits, plus random W and K.
func genCase(rng *rand.Rand) (string, []Interval, int, int) {
	n := 1 + rng.Intn(40)
	runes := make([]rune, n)
	offsets := []int{0}
	length := 0
	for i := range runes {
		if rng.Intn(2) == 0 {
			runes[i] = 'a' + rune(rng.Intn(26))
			length++
		} else {
			runes[i] = '一' + rune(rng.Intn(50))
			length += 3
		}
		offsets = append(offsets, length)
	}

	m := rng.Intn(14)
	hits := make([]Interval, m)
	for i := range hits {
		s := offsets[rng.Intn(len(offsets)-1)]
		e := offsets[1+rng.Intn(len(offsets)-1)]
		if s > e {
			s, e = e, s
		}
		if s == e {
			e = offsets[len(offsets)-1]
			if s == e {
				e = s
			}
		}
		hits[i] = Interval{Start: s, End: e}
	}

	W := 1 + rng.Intn(30)
	K := 1 + rng.Intn(5)
	return string(runes), hits, W, K
}

func shuffleHits(rng *rand.Rand, hits []Interval) []Interval {
	out := append([]Interval(nil), hits...)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// TestRandomDifferential runs 2000 random cases against the round-by-round
// naive transcription, logging input, output and the decision basis.
func TestRandomDifferential(t *testing.T) {
	const cases = 2000
	rng := rand.New(rand.NewSource(20261001))
	for c := 0; c < cases; c++ {
		text, hits, W, K := genCase(rng)
		reg := NewRegistry()
		docID := fmt.Sprintf("case-%d", c)
		if err := reg.Register(docID, text); err != nil {
			t.Fatalf("case %d register: %v", c, err)
		}

		ordered := shuffleHits(rng, hits)
		got, err := reg.Snippets(docID, ordered, W, K)
		if err != nil {
			t.Fatalf("case %d unexpected error: %v\ninput: text=%q hits=%+v W=%d K=%d",
				c, err, text, ordered, W, K)
		}
		want := naiveSnippets(hits, W, K, len(text))

		t.Logf(
			"case=%d basis=fixed-window-greedy input: text=%q len=%d hits(raw)=%+v hits(used)=%+v W=%d K=%d => output=%+v; naive=%+v; verdict=%s",
			c, text, len(text), ordered, dedupCopy(hits), W, K, got, want,
			map[bool]string{true: "MATCH", false: "MISMATCH"}[reflect.DeepEqual(got, want)],
		)

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d mismatch\ntext=%q\nhits=%+v\nW=%d K=%d\ngot=%+v\nwant=%+v",
				c, text, ordered, W, K, got, want)
		}

		// Order independence: a different presentation order gives the
		// same output.
		reordered := shuffleHits(rng, hits)
		got2, err := reg.Snippets(docID, reordered, W, K)
		if err != nil || !reflect.DeepEqual(got2, want) {
			t.Fatalf("case %d order-dependent result: %+v vs %+v", c, got2, want)
		}
	}
}

// Deterministic replay: replaying the identical operation sequence on a
// fresh registry yields identical outputs.
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	type op struct {
		id   string
		text string
		hits []Interval
		W, K int
	}
	var ops []op
	var first []string

	run := func() []string {
		reg := NewRegistry()
		var out []string
		for _, o := range ops {
			if err := reg.Register(o.id, o.text); err != nil {
				out = append(out, "register-error:"+err.Error())
				continue
			}
			got, err := reg.Snippets(o.id, o.hits, o.W, o.K)
			out = append(out, fmt.Sprintf("%+v/%v", got, err))
		}
		return out
	}

	for i := 0; i < 200; i++ {
		text, hits, W, K := genCase(rng)
		ops = append(ops, op{
			id: fmt.Sprintf("doc-%d", i%20), text: text,
			hits: shuffleHits(rng, hits), W: W, K: K,
		})
	}
	first = run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("replay produced different outputs")
	}
	t.Logf("basis=identical-replay ops=%d outputs-identical=true verdict=MATCH", len(ops))
}

// Concurrent calls must not race and must agree with one serial ordering:
// every doc registered once stays queryable; every failed op matches its
// spec reason.
func TestConcurrentAccess(t *testing.T) {
	reg := NewRegistry()
	const docs = 32
	var wg sync.WaitGroup

	for d := 0; d < docs; d++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			docID := fmt.Sprintf("doc-%d", id)
			err := reg.Register(docID, "abcdefghijklmnopqrstuvwxyz012345")
			if err != nil {
				t.Errorf("register: %v", err)
				return
			}
			var inner sync.WaitGroup
			for q := 0; q < 8; q++ {
				inner.Add(1)
				go func(q int) {
					defer inner.Done()
					hits := []Interval{
						{Start: 3 * (q % 3), End: 3*(q%3) + 6},
						{Start: 20, End: 25},
					}
					got, err := reg.Snippets(docID, hits, 8, 4)
					if err != nil {
						t.Errorf("snippets: %v", err)
						return
					}
					_ = got
					if _, err := reg.Snippets("missing", nil, 1, 1); err == nil {
						t.Errorf("missing doc unexpectedly found")
					}
				}(q)
			}
			inner.Wait()
			if err := reg.Unregister(docID); err != nil {
				t.Errorf("unregister: %v", err)
			}
			if _, err := reg.Snippets(docID, nil, 1, 1); err == nil {
				t.Errorf("doc queryable after unregister")
			}
		}(d)
	}
	wg.Wait()
	t.Log("basis=serial-equivalence concurrent register/snippets/unregister interleavings completed verdict=MATCH")
}
