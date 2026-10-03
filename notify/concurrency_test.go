package notify

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentOperations(t *testing.T) {
	s := New("en")
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				loc := []string{"en", "zh-TW", "zh-Hant-TW"}[i%3]
				ch := []string{"sms", "push", "email", "*"}[(g+i)%4]
				if g < 2 {
					_ = s.Retire("n", loc, "sms", int64(i%5))
				} else if g%2 == 0 {
					_ = s.Publish("n", loc, ch, fmt.Sprintf("v{x}-%d", i), int64(i%5))
				} else {
					_, _ = s.Render("n", loc, []string{"sms", "push", "email"}[i%3], int64(i%6),
						map[string]string{"x": "好"})
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestReplayDeterminism(t *testing.T) {
	ops := []func(s *Store) (*Result, *Error){
		func(s *Store) (*Result, *Error) {
			return nil, s.Publish("welcome", "en", "*", "Hi {name}", 0)
		},
		func(s *Store) (*Result, *Error) {
			return nil, s.Publish("welcome", "zh", "sms", "你好{name}，{code}", 0)
		},
		func(s *Store) (*Result, *Error) {
			return nil, s.Publish("welcome", "zh-Hant", "*", "您好 {name|貴賓}", 100)
		},
		func(s *Store) (*Result, *Error) {
			return s.Render("welcome", "zh-Hant-TW", "sms", 150, map[string]string{"name": "Li"})
		},
		func(s *Store) (*Result, *Error) {
			return s.Render("welcome", "zh-Hant-TW", "sms", 50, map[string]string{})
		},
	}
	var outs []string
	for round := 0; round < 3; round++ {
		s := New("en")
		var cur []string
		for _, op := range ops {
			r, e := op(s)
			cur = append(cur, describeOutcome(r, e))
		}
		outs = append(outs, strings.Join(cur, "|"))
	}
	if outs[0] != outs[1] || outs[1] != outs[2] {
		t.Fatalf("replay not deterministic:\n%s\n%s\n%s", outs[0], outs[1], outs[2])
	}
}
