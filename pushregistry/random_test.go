package pushregistry

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
)

type randomOp struct {
	kind   string
	user   string
	device string
	token  string
	now    int64
}

func TestRandomSimulationSkeleton(t *testing.T) {
	for seed := int64(0); seed < 1; seed++ {
		ops := make([]randomOp, 1)
		if len(ops) != 1 {
			t.Fatal("bad skeleton")
		}
	}
}

func TestRandomSimulation2000(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		seed := seed
		t.Run("", func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			d := 1 + rng.Intn(4)
			r := rng.Intn(4)
			g := int64(rng.Intn(9))
			b := int64(rng.Intn(9))
			ops := generateRandomOps(rng, 100)

			registry, err := NewRegistry(d, r, g, b)
			if err != nil {
				t.Fatalf("seed=%d NewRegistry error=%v", seed, err)
			}
			sim := newSimulator(d, r, g, b)

			for index, op := range ops {
				gotErr, gotTargets := runProductionOp(registry, op)
				wantErr, wantTargets := runSimulationOp(sim, op)
				basis := "ok"
				if wantErr != nil {
					basis = wantErr.Error()
				}
				t.Logf("seed=%04d op=%03d input=%s now=%d output(err=%v targets=%v) basis=%s",
					seed, index, formatOp(op), op.now, gotErr, gotTargets, basis)

				if !sameError(gotErr, wantErr) {
					t.Fatalf("seed=%d op=%d %s: error = %v, want %v", seed, index, formatOp(op), gotErr, wantErr)
				}
				if !sameStringSlice(gotTargets, wantTargets) {
					t.Fatalf("seed=%d op=%d %s: targets = %v, want %v", seed, index, formatOp(op), gotTargets, wantTargets)
				}
			}

			assertProductionInvariants(t, registry, d, r)
		})
	}
}

func sameError(left, right error) bool {
	return errors.Is(left, right) && errors.Is(right, left)
}

func generateRandomOps(rng *rand.Rand, count int) []randomOp {
	users := []string{"u1", "u2", "u3"}
	devices := []string{"d1", "d2", "d3", "d4"}
	tokens := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8"}
	ops := make([]randomOp, count)
	lastNow := int64(0)

	for i := range ops {
		kindRoll := rng.Intn(100)
		switch {
		case kindRoll < 45:
			ops[i].kind = "register"
		case kindRoll < 60:
			ops[i].kind = "feedback"
		case kindRoll < 72:
			ops[i].kind = "touch"
		case kindRoll < 84:
			ops[i].kind = "unregister"
		default:
			ops[i].kind = "targets"
		}

		ops[i].user = users[rng.Intn(len(users))]
		ops[i].device = devices[rng.Intn(len(devices))]
		ops[i].token = tokens[rng.Intn(len(tokens))]

		switch rng.Intn(10) {
		case 0:
			ops[i].now = -1
		case 1:
			ops[i].now = 1_000_000_000_001
		case 2:
			if lastNow > 0 {
				ops[i].now = lastNow - int64(rng.Intn(2)+1)
				if ops[i].now < 0 {
					ops[i].now = 0
				}
			} else {
				ops[i].now = lastNow
			}
		default:
			lastNow += int64(rng.Intn(3))
			ops[i].now = lastNow
		}

		if rng.Intn(12) == 0 {
			switch rng.Intn(3) {
			case 0:
				ops[i].user = ""
			case 1:
				ops[i].device = strings.Repeat("d", 65)
			case 2:
				ops[i].token = strings.Repeat("t", 257)
			}
		}
	}
	return ops
}

func runProductionOp(registry *Registry, op randomOp) (error, []string) {
	switch op.kind {
	case "register":
		return registry.Register([]byte(op.user), []byte(op.device), []byte(op.token), op.now), nil
	case "feedback":
		return registry.Feedback([]byte(op.token), op.now), nil
	case "touch":
		return registry.Touch([]byte(op.user), []byte(op.device), op.now), nil
	case "unregister":
		return registry.Unregister([]byte(op.user), []byte(op.device), op.now), nil
	case "targets":
		targets, err := registry.Targets([]byte(op.user), op.now)
		return err, bytesToStrings(targets)
	default:
		panic("unknown op")
	}
}

func runSimulationOp(sim *simulator, op randomOp) (error, []string) {
	switch op.kind {
	case "register":
		return sim.register(op.user, op.device, op.token, op.now), nil
	case "feedback":
		return sim.feedback(op.token, op.now), nil
	case "touch":
		return sim.touch(op.user, op.device, op.now), nil
	case "unregister":
		return sim.unregister(op.user, op.device, op.now), nil
	case "targets":
		targets, err := sim.targets(op.user, op.now)
		return err, targets
	default:
		panic("unknown op")
	}
}

func formatOp(op randomOp) string {
	return op.kind + "(" + op.user + "," + op.device + "," + op.token + ")"
}

func sameStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func assertProductionInvariants(t *testing.T, registry *Registry, deviceLimit, retainedLimit int) {
	t.Helper()
	ownership := make(map[string]int)
	for user, idx := range registry.users {
		if len(idx.bindings) > deviceLimit {
			t.Fatalf("user %q has %d devices, limit %d", user, len(idx.bindings), deviceLimit)
		}
		for device, bound := range idx.bindings {
			if bound.user != user || bound.device != device {
				t.Fatalf("binding index mismatch: %q/%q points to %q/%q", user, device, bound.user, bound.device)
			}
			if len(bound.old) > retainedLimit {
				t.Fatalf("binding %q/%q has %d old tokens, limit %d", user, device, len(bound.old), retainedLimit)
			}
			ownership[bound.cur]++
			for _, item := range bound.old {
				ownership[item.token]++
			}
		}
	}
	for token, owner := range registry.tokenOwners {
		if ownership[token] != 1 {
			t.Fatalf("token %q ownership index count=%d", token, ownership[token])
		}
		if owner.binding == nil {
			t.Fatalf("token %q has nil binding owner", token)
		}
	}
	for token, count := range ownership {
		if count != 1 {
			t.Fatalf("token %q appears %d times", token, count)
		}
		if _, exists := registry.tokenOwners[token]; !exists {
			t.Fatalf("token %q exists without owner index", token)
		}
	}
}
