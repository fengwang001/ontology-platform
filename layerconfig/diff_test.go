package layerconfig_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/layerconfig"
)

// TestRandomDifferential runs a long randomized history of registrations,
// publications (all five ops, all four layers, invalid inputs injected on
// purpose), rollbacks and historical/current resolutions, and compares the
// optimized store against the independent naive model after every single
// operation. Every operation's input, actual output and the verdict basis is
// logged so a failure can be replayed.
func TestRandomDifferential(t *testing.T) {
	for _, seed := range []int64{1, 2, 7, 42, 123456} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runOneDiff(t, seed)
		})
	}
}

func runOneDiff(t *testing.T, seed int64) {
	l := newLogger(t)
	defer l.finish()

	rng := rand.New(rand.NewSource(seed))
	real := layerconfig.NewStore()
	naive := newNaive()

	const nKeys = 8
	const nEnvs = 3
	const nRegions = 3
	const nInstances = 3

	type keyDef struct {
		name string
		sc   layerconfig.Schema
	}
	defs := make([]keyDef, nKeys)
	for i := 0; i < nKeys; i++ {
		defs[i].name = fmt.Sprintf("k%d", i)
		sc := layerconfig.Schema{Key: defs[i].name}
		switch rng.Intn(4) {
		case 0:
			sc.Type = layerconfig.TypeString
			sc.Merge = layerconfig.MergeOverride
		case 1:
			sc.Type = layerconfig.TypeInt
			sc.Merge = layerconfig.MergeOverride
			sc.Min = -10
			sc.Max = 10
		case 2:
			sc.Type = layerconfig.TypeBool
			sc.Merge = layerconfig.MergeOverride
		case 3:
			sc.Type = layerconfig.TypeStringList
			if rng.Intn(2) == 0 {
				sc.Merge = layerconfig.MergeAppend
			} else {
				sc.Merge = layerconfig.MergeOverride
			}
		}
		sc.Required = rng.Intn(3) == 0
		defs[i].sc = sc
	}
	// Register everything in both models.
	for _, d := range defs {
		e1 := real.RegisterKey(d.sc)
		e2 := naive.register(d.sc)
		if (e1 == nil) != (e2 == nil) {
			t.Fatalf("register mismatch for %s: %v vs %v", d.name, e1, e2)
		}
	}

	randomScope := func() layerconfig.Scope {
		switch rng.Intn(4) {
		case 0:
			return layerconfig.Scope{}
		case 1:
			return layerconfig.Scope{Env: fmt.Sprintf("e%d", rng.Intn(nEnvs))}
		case 2:
			return layerconfig.Scope{Env: fmt.Sprintf("e%d", rng.Intn(nEnvs)), Region: fmt.Sprintf("r%d", rng.Intn(nRegions))}
		default:
			return layerconfig.Scope{
				Env:      fmt.Sprintf("e%d", rng.Intn(nEnvs)),
				Region:   fmt.Sprintf("r%d", rng.Intn(nRegions)),
				Instance: fmt.Sprintf("i%d", rng.Intn(nInstances)),
			}
		}
	}
	randomValue := func(sc layerconfig.Schema) layerconfig.Value {
		switch sc.Type {
		case layerconfig.TypeInt:
			// Occasionally inject an out-of-range value.
			if rng.Intn(8) == 0 {
				return layerconfig.Value{Int: sc.Max + 1 + rng.Intn(5)}
			}
			return layerconfig.Value{Int: sc.Min + rng.Intn(sc.Max-sc.Min+1)}
		case layerconfig.TypeBool:
			return layerconfig.Value{Bool: rng.Intn(2) == 0}
		case layerconfig.TypeStringList:
			n := rng.Intn(4)
			out := make([]string, n)
			// Draw from a small alphabet to force duplicates.
			for i := range out {
				out[i] = fmt.Sprintf("t%d", rng.Intn(5))
			}
			return layerconfig.Value{List: out}
		default:
			return layerconfig.Value{Str: fmt.Sprintf("v%d", rng.Intn(6))}
		}
	}

	// compareAllResolves compares a random mix of historical and current
	// resolutions across both models.
	compareResolves := func() {
		nChecks := 6
		for j := 0; j < nChecks; j++ {
			target := randomScope()
			d := defs[rng.Intn(nKeys)]
			// Choose version: 0 (initial), a past version, or current.
			ver := -1
			cur := real.CurrentVersion()
			if cur > 0 {
				switch rng.Intn(3) {
				case 0:
					ver = 0
				case 1:
					ver = rng.Intn(cur + 1)
				}
			}
			got, gErr := real.Resolve(ver, target, d.name)
			want := naive.resolve(ver, target, d.name)
			basis := fmt.Sprintf("differential resolution seed=%d key=%s version=%d target=%s",
				seed, d.name, ver, scopeString(target))
			l.logResolve(ver, target, d.name, got, gErr, basis)
			if gErr != nil {
				t.Fatalf("unexpected resolve error: %v (%s)", gErr, basis)
			}
			if !resolvesEqual(got, want) {
				t.Fatalf("resolution mismatch: got %s naive %s (%s)",
					resolvedString(got), resolvedString(want), basis)
			}
		}
	}

	const steps = 1500
	for step := 0; step < steps; step++ {
		switch rng.Intn(20) {
		case 0:
			// Rollback.
			target := rng.Intn(real.CurrentVersion() + 2) // occasionally too big
			v1, e1 := real.Rollback(target)
			v2, e2 := naive.rollback(target)
			basis := fmt.Sprintf("differential rollback seed=%d step=%d target=%d", seed, step, target)
			l.logRollback(target, v1, e1, basis)
			if errKind(e1) != errKind(e2) || v1 != v2 {
				t.Fatalf("rollback mismatch: real=(%d,%v) naive=(%d,%v) (%s)",
					v1, e1, v2, e2, basis)
			}
		default:
			// Build a small random batch, sometimes injecting an invalid
			// scope/key/op or duplicated targets to exercise error paths.
			batchSize := 1 + rng.Intn(3)
			changes := make([]layerconfig.Change, 0, batchSize)
			injectInvalid := rng.Intn(10) == 0
			injectDup := rng.Intn(8) == 0
			for b := 0; b < batchSize; b++ {
				d := defs[rng.Intn(nKeys)]
				sc := randomScope()
				key := d.name
				if injectInvalid {
					switch rng.Intn(3) {
					case 0:
						sc = layerconfig.Scope{Region: "orphan"}
					case 1:
						key = ""
					case 2:
						sc = layerconfig.Scope{Env: "e1", Instance: "i"}
					}
				}
				if rng.Intn(12) == 0 {
					key = "ghost" // unregistered
				}
				op := []layerconfig.Op{
					layerconfig.OpWrite, layerconfig.OpWrite, layerconfig.OpWrite,
					layerconfig.OpCancel, layerconfig.OpClear,
					layerconfig.OpLock, layerconfig.OpUnlock,
				}[rng.Intn(7)]
				ch := layerconfig.Change{Op: op, Scope: sc, Key: key}
				if op == layerconfig.OpWrite {
					ch.Value = randomValue(d.sc)
				}
				changes = append(changes, ch)
			}
			if injectDup && len(changes) >= 1 {
				dup := changes[0]
				changes = append(changes, dup)
			}

			v1, e1 := real.Publish(changes)
			v2, e2 := naive.publish(changes)
			basis := fmt.Sprintf("differential publish seed=%d step=%d", seed, step)
			l.logPublish(changes, v1, e1, basis)
			if errKind(e1) != errKind(e2) {
				t.Fatalf("publish error mismatch: real=%v naive=%v (%s)", e1, e2, basis)
			}
			if e1 == nil && (v1 != v2 || v1 != real.CurrentVersion()) {
				t.Fatalf("publish version mismatch: real=%d naive=%d (%s)", v1, v2, basis)
			}
			if e1 == nil && real.CurrentVersion() != naive.current {
				t.Fatalf("current version drift real=%d naive=%d", real.CurrentVersion(), naive.current)
			}
		}
		compareResolves()
	}

	// Final full sweep across every registered version, scope, key.
	for ver := 0; ver <= real.CurrentVersion(); ver++ {
		for ei := 0; ei <= nEnvs; ei++ {
			for ri := 0; ri <= nRegions; ri++ {
				for ii := 0; ii <= nInstances; ii++ {
					sc := layerconfig.Scope{}
					if ei > 0 {
						sc.Env = fmt.Sprintf("e%d", ei-1)
					}
					if ri > 0 {
						if ei == 0 {
							continue
						}
						sc.Region = fmt.Sprintf("r%d", ri-1)
					}
					if ii > 0 {
						if ri == 0 {
							continue
						}
						sc.Instance = fmt.Sprintf("i%d", ii-1)
					}
					for _, d := range defs {
						got, err := real.Resolve(ver, sc, d.name)
						if err != nil {
							t.Fatalf("final sweep error v=%d: %v", ver, err)
						}
						want := naive.resolve(ver, sc, d.name)
						if !resolvesEqual(got, want) {
							t.Fatalf("final sweep mismatch v=%d target=%s key=%s got=%s want=%s",
								ver, scopeString(sc), d.name, resolvedString(got), resolvedString(want))
						}
					}
				}
			}
		}
	}
	l.line("differential seed=%d complete: %d randomized steps + full version sweep, all equal to naive model", seed, stepsCount())
}

func errKind(err error) layerconfig.ErrorKind {
	if err == nil {
		return 0
	}
	if e, ok := layerconfig.AsError(err); ok {
		return e.Kind
	}
	return -1
}

func stepsCount() int { return 1500 }
