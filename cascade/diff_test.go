package cascade_test

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"

	"ontology/cascade"
	"ontology/cascade/internal/testutil/naive"
)

// opLogger 记录每个操作的输入、实际输出与判定依据，并同步打印。
type opLogger struct {
	t    *testing.T
	b    strings.Builder
	step int
}

func (l *opLogger) log(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.step++
	entry := fmt.Sprintf("step %03d: %s", l.step, line)
	l.b.WriteString(entry + "\n")
	fmt.Fprintln(os.Stdout, entry)
}

func (l *opLogger) dump() {
	if l.t.Failed() {
		l.t.Logf("\n%s", l.b.String())
	}
}

func canonicalController(snap cascade.Snapshot) map[string]string {
	out := map[string]string{}
	for id, o := range snap.Objects {
		refs := ""
		for _, r := range o.Owners {
			b := "0"
			if r.Blocking {
				b = "1"
			}
			refs += r.OwnerID + ":" + b + ","
		}
		fins := append([]string(nil), o.Finalizers...)
		sort.Strings(fins)
		del := "0"
		if o.Deleting {
			del = "1"
		}
		out[id] = fmt.Sprintf("owners=[%s]fins=%vdel=%sst=%d", refs, fins, del, o.Strategy)
	}
	return out
}

func sameState(a, b map[string]string) (bool, string) {
	keys := map[string]struct{}{}
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	for k := range keys {
		_, inA := a[k]
		_, inB := b[k]
		if inA != inB {
			return false, fmt.Sprintf("object %q presence impl=%v naive=%v (sets %d vs %d)\n impl=%q\nnaive=%q", k, inA, inB, len(a), len(b), a[k], b[k])
		}
		if a[k] != b[k] {
			return false, fmt.Sprintf("object %q differs:\n impl=%q\nnaive=%q", k, a[k], b[k])
		}
	}
	return true, ""
}

// TestDifferentialAgainstNaive 大量随机图与随机操作序列上的逐步对照。
func TestDifferentialAgainstNaive(t *testing.T) {
	const runs, steps = 60, 400
	for seed := int64(1); seed <= runs; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			impl := cascade.New()
			oracle := naive.New()
			lg := &opLogger{t: t}
			defer lg.dump()

			nextID := 0
			newID := func() string {
				nextID++
				return fmt.Sprintf("n%d", nextID)
			}

			randomOwners := func(existing []string, self string) []cascade.OwnerRef {
				var refs []cascade.OwnerRef
				if len(existing) == 0 {
					return nil
				}
				k := rng.Intn(3) // 0..2 个属主
				rng.Shuffle(len(existing), func(i, j int) {
					existing[i], existing[j] = existing[j], existing[i]
				})
				for _, id := range existing {
					if id == self || id == "" {
						continue
					}
					if len(refs) >= k {
						break
					}
					refs = append(refs, cascade.OwnerRef{OwnerID: id, Blocking: rng.Intn(2) == 0})
				}
				return refs
			}

			for step := 0; step < steps; step++ {
				alive := oracleIDs(oracle)

				// 动作分布偏向创建，保证图持续演进。
				var action int
				switch {
				case rng.Intn(100) < 45:
					action = 0 // create
				case len(alive) == 0:
					action = 0
				default:
					action = 1 + rng.Intn(6)
				}

				switch action {
				case 0:
					id := newID()
					cand := append([]string(nil), alive...)
					refs := randomOwners(cand, id)
					fins := randomFinalizers(rng)
					nrefs := toNaiveRefs(refs)
					lg.log("Create id=%s owners=%v finalizers=%v", id, refs, fins)
					errI := impl.Create(id, refs, fins)
					errN := oracle.Create(id, nrefs, fins)
					lg.log("  -> implErr=%v naiveErr=%v 判定依据=按规则优先级校验属主/环/参数", errI, errN)
					checkErrors(t, errI, errN)
				case 1:
					id := pick(rng, alive)
					st := []cascade.Strategy{cascade.Background, cascade.Foreground, cascade.Orphan}[rng.Intn(3)]
					lg.log("Delete id=%s strategy=%s", id, st)
					errI := impl.Delete(id, st)
					errN := oracle.Delete(id, toNaiveStrategy(st))
					lg.log("  -> implErr=%v naiveErr=%v 判定依据=标记删除并收敛级联", errI, errN)
					checkErrors(t, errI, errN)
				case 2:
					id := pick(rng, alive)
					name := finName(rng)
					lg.log("AddFinalizer id=%s name=%s", id, name)
					errI := impl.AddFinalizer(id, name)
					errN := oracle.AddFinalizer(id, name)
					lg.log("  -> implErr=%v naiveErr=%v", errI, errN)
					checkErrors(t, errI, errN)
				case 3:
					id := pick(rng, alive)
					obj, _ := impl.Get(id)
					if len(obj.Finalizers) == 0 {
						lg.log("RemoveFinalizer id=%s name=absent (probe)", id)
						errI := impl.RemoveFinalizer(id, "nope")
						errN := oracle.RemoveFinalizer(id, "nope")
						lg.log("  -> implErr=%v naiveErr=%v 判定依据=不存在必须报错", errI, errN)
						checkErrors(t, errI, errN)
					} else {
						name := obj.Finalizers[rng.Intn(len(obj.Finalizers))]
						lg.log("RemoveFinalizer id=%s name=%s", id, name)
						errI := impl.RemoveFinalizer(id, name)
						errN := oracle.RemoveFinalizer(id, name)
						lg.log("  -> implErr=%v naiveErr=%v 判定依据=摘除终结器并收敛整条级联", errI, errN)
						checkErrors(t, errI, errN)
					}
				case 4:
					id := pick(rng, alive)
					cand := append([]string(nil), alive...)
					refs := randomOwners(cand, id)
					lg.log("ReplaceOwners id=%s owners=%v", id, refs)
					errI := impl.ReplaceOwners(id, refs)
					errN := oracle.ReplaceOwners(id, toNaiveRefs(refs))
					lg.log("  -> implErr=%v naiveErr=%v 判定依据=删除中拒绝/环/缺失校验后重连", errI, errN)
					checkErrors(t, errI, errN)
				case 5:
					// 对已删除中对象重复删除，覆盖升级与幂等。
					var deleting []string
					for _, id := range alive {
						if o, _ := impl.Get(id); o.Deleting {
							deleting = append(deleting, id)
						}
					}
					if len(deleting) == 0 {
						id := pick(rng, alive)
						lg.log("Delete id=%s strategy=Background (repeat probe)", id)
						errI := impl.Delete(id, cascade.Background)
						errN := oracle.Delete(id, naive.Background)
						checkErrors(t, errI, errN)
					} else {
						id := pick(rng, deleting)
						st := []cascade.Strategy{cascade.Background, cascade.Foreground, cascade.Orphan}[rng.Intn(3)]
						lg.log("Delete id=%s strategy=%s (repeat on deleting)", id, st)
						errI := impl.Delete(id, st)
						errN := oracle.Delete(id, toNaiveStrategy(st))
						lg.log("  -> implErr=%v naiveErr=%v 判定依据=幂等，仅后台->前台升级", errI, errN)
						checkErrors(t, errI, errN)
					}
				case 6:
					id := pick(rng, alive)
					lg.log("Get id=%s (read-only)", id)
					_, okI := impl.Get(id)
					_, okN := oracle.Objects[id]
					if okI != okN {
						t.Fatalf("Get existence mismatch for %s", id)
					}
				}

				if ok, why := sameState(canonicalController(impl.Snapshot()), oracle.Canonical()); !ok {
					t.Fatalf("state mismatch after step %d (seed %d): %s", lg.step, seed, why)
				}
				impl.CheckInvariants(t, fmt.Sprintf("seed%d step %d", seed, lg.step))
			}
			if ok, why := sameState(canonicalController(impl.Snapshot()), oracle.Canonical()); !ok {
				t.Fatalf("final state mismatch (seed %d): %s", seed, why)
			}
		})
	}
}

func oracleIDs(m *naive.Model) []string {
	ids := make([]string, 0, len(m.Objects))
	for id := range m.Objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func pick(rng *rand.Rand, xs []string) string {
	return xs[rng.Intn(len(xs))]
}

func randomFinalizers(rng *rand.Rand) []string {
	if rng.Intn(100) >= 35 {
		return nil
	}
	n := 1 + rng.Intn(2)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, finName(rng))
	}
	return out
}

func finName(rng *rand.Rand) string {
	return fmt.Sprintf("f%d", rng.Intn(4))
}

func toNaiveRefs(refs []cascade.OwnerRef) []naive.OwnerRef {
	out := make([]naive.OwnerRef, len(refs))
	for i, r := range refs {
		out[i] = naive.OwnerRef{OwnerID: r.OwnerID, Blocking: r.Blocking}
	}
	return out
}

func toNaiveStrategy(s cascade.Strategy) naive.Strategy {
	switch s {
	case cascade.Foreground:
		return naive.Foreground
	case cascade.Orphan:
		return naive.Orphan
	default:
		return naive.Background
	}
}

func checkErrors(t *testing.T, implErr error, naiveErr *naive.Error) {
	t.Helper()
	if (implErr == nil) != (naiveErr == nil) {
		t.Fatalf("error presence mismatch: impl=%v naive=%v", implErr, naiveErr)
	}
	if implErr == nil {
		return
	}
	implKind := int(implErr.(*cascade.GCError).Kind)
	if implKind != int(naiveErr.Kind) {
		t.Fatalf("error kind mismatch: impl=%s(%d) naive=%d", implErr, implKind, naiveErr.Kind)
	}
}
