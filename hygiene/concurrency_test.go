package hygiene_test

import (
	"sync"
	"testing"

	"ontology/hygiene"
)

func TestConcurrentExpansionsAreSerializable(t *testing.T) {
	session := hygiene.NewSession()
	mustPublicDef(t, session, "add2", []string{"x"}, mustPublicTerm(t, "(+ x 2)"))
	mustPublicDef(t, session, "inc", []string{"x"}, mustPublicTerm(t, "(add2 x)"))

	const goroutines = 16
	const perGoroutine = 40
	var wg sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perGoroutine {
				if _, err := session.Expand(mustPublicTerm(t, "(inc 1)")); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()

	if session.BindingCount() != 0 || session.ExpansionCount() != goroutines*perGoroutine*2 {
		t.Fatalf("state N=%d X=%d", session.BindingCount(), session.ExpansionCount())
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() (string, int, int) {
		session := hygiene.NewSession()
		mustPublicDef(t, session, "my-or", []string{"a", "b"}, mustPublicTerm(t, "((lam (t) (if t t b)) a)"))
		mustPublicDef(t, session, "inc", []string{"x"}, mustPublicTerm(t, "(my-or x x)"))
		result, err := session.Expand(mustPublicTerm(t, "(inc (f))"))
		if err != nil {
			t.Fatal(err)
		}
		text, err := hygiene.Render(result)
		if err != nil {
			t.Fatal(err)
		}
		return text, session.BindingCount(), session.ExpansionCount()
	}

	firstText, firstN, firstX := run()
	secondText, secondN, secondX := run()
	if firstText != secondText || firstN != secondN || firstX != secondX {
		t.Fatalf("replay mismatch: (%q,%d,%d) vs (%q,%d,%d)", firstText, firstN, firstX, secondText, secondN, secondX)
	}
}

func mustPublicTerm(t *testing.T, text string) *hygiene.Term {
	t.Helper()
	term, err := hygiene.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return term
}

func mustPublicDef(t *testing.T, session *hygiene.Session, name string, params []string, template *hygiene.Term) {
	t.Helper()
	if err := session.DefMacro(name, params, template); err != nil {
		t.Fatal(err)
	}
}
