package gate

import (
	"strconv"
	"testing"
)

func TestTouchedBound(t *testing.T) {
	for _, accountCount := range []int{2, 5000} {
		t.Run("accounts", func(t *testing.T) {
			g := New()
			for i := 0; i < accountCount; i++ {
				acct := []byte("A" + strconv.Itoa(i))
				if err := g.Register(1, acct, []byte("G")); err != nil {
					t.Fatal(err)
				}
			}
			if err := g.SetLimit(2, []byte("S"), 10, 1000000000, 1000000000); err != nil {
				t.Fatal(err)
			}
			checkTouched(t, g, 0, func() error { return g.SetLimit(3, []byte("S"), 11, 1000000000, 1000000000) })
			checkTouched(t, g, 1, func() error { return g.SetHedge(4, []byte("A0"), []byte("S"), Long, 1) })
			checkTouched(t, g, 1, func() error { return g.Order(5, []byte("o"), []byte("A0"), []byte("S"), Long, Open, 1) })
			checkTouched(t, g, 1, func() error { return g.Fill(6, []byte("o"), 1) })
			checkTouched(t, g, 1, func() error { return g.Order(7, []byte("c"), []byte("A0"), []byte("S"), Long, Open, 2) })
			checkTouched(t, g, 1, func() error { return g.Cancel(8, []byte("c")) })
		})
	}
}

func checkTouched(t *testing.T, g *Gateway, want int, action func() error) {
	t.Helper()
	if err := action(); err != nil {
		t.Fatal(err)
	}
	if g.touched != want {
		t.Fatalf("touched=%d want at most/equal %d", g.touched, want)
	}
}
