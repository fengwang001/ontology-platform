package gate_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/gate"
)

type modelSide struct {
	position     int64
	pendingOpen  int64
	pendingClose int64
}

type modelOrder struct {
	acct     string
	sym      string
	side     gate.Side
	offset   gate.Offset
	remain   int64
	finished bool
}

type naiveModel struct {
	now     int64
	groups  map[string]string
	limits  map[string][3]int64
	hedges  map[string]int64
	sides   map[string]map[string][2]modelSide
	filled  map[string]map[string]int64
	orders  map[string]modelOrder
	nextOID int
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		groups: map[string]string{},
		limits: map[string][3]int64{},
		hedges: map[string]int64{},
		sides:  map[string]map[string][2]modelSide{},
		filled: map[string]map[string]int64{},
		orders: map[string]modelOrder{},
	}
}

func TestRandomReplayMatchesNaive(t *testing.T) {
	for seed := int64(0); seed < 1500; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			g := gate.New()
			model := newNaiveModel()

			for _, acct := range []string{"A1", "A2", "A3"} {
				group := "G"
				if acct == "A3" {
					group = "G2"
				}
				applyAndCheck(t, g, model, op{kind: "register", now: 1, acct: acct, group: group})
				model.ensure(acct, "S")
			}
			applyAndCheck(t, g, model, op{kind: "limit", now: 2, sym: "S", la: 40, lg: 70, day: 100})

			for step := 0; step < 80; step++ {
				now := model.now + int64(rng.Intn(4))
				in := randomOp(rng, model, now)
				applyAndCheck(t, g, model, in)
			}
		})
	}
}

func applyAndCheck(t *testing.T, g *gate.Gateway, model *naiveModel, in op) {
	t.Helper()
	expected := model.apply(in)
	actual := doOp(g, in)
	t.Logf("random input=%+v output=%s model=%s decision=%s", in, status(actual), status(expected), decision(in, expected, actual))
	if !sameError(expected, actual) {
		t.Fatalf("error mismatch: got %v want %v", actual, expected)
	}
	if actual == nil {
		model.verify(t, g)
	}
}

func decision(in op, expected, actual error) string {
	if expected != nil || actual != nil {
		return "first matching rejection rule"
	}
	return in.kind + " accepted and state recomputed from orders"
}

func sameError(want, got error) bool {
	if want == nil || got == nil {
		return want == got
	}
	return want.Error() == got.Error()
}
