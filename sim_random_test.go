package store

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"ontology/cond"
)

type simOp struct {
	kind    string
	tenant  string
	key     string
	size    int64
	etag    string
	ver     int64
	now     int64
	c       cond.Cond
	q       int64
	on      bool
	storage bool
	desc    string
}

func simRandomCond(r *rand.Rand) cond.Cond {
	var c cond.Cond
	if r.Intn(2) == 0 {
		c.IfMatch = strp(fmt.Sprintf("e%d-%d", r.Intn(8), r.Intn(3)))
	}
	if r.Intn(2) == 0 {
		s := r.Int63n(1_000_000_000_002) - 1
		c.IfUnmodifiedSince = &s
	}
	if r.Intn(2) == 0 {
		c.IfNoneMatchStar = true
	}
	return c
}

func simCondDesc(c cond.Cond) string {
	var parts []string
	if c.IfMatch != nil {
		parts = append(parts, "IfMatch="+strconv.Quote(*c.IfMatch))
	}
	if c.IfUnmodifiedSince != nil {
		parts = append(parts, fmt.Sprintf("IfUnmodifiedSince=%d", *c.IfUnmodifiedSince))
	}
	if c.IfNoneMatchStar {
		parts = append(parts, "IfNoneMatch=*")
	}
	if len(parts) == 0 {
		return "{}"
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func simRandomOp(r *rand.Rand, step int) simOp {
	tenant := "t1"
	if r.Intn(5) == 0 {
		tenant = "t2"
	}
	key := []string{"a", "b", "c"}[r.Intn(3)]
	op := simOp{tenant: tenant, key: key, ver: -99, now: r.Int63n(1_000_000_000_001)}
	switch r.Intn(10) {
	case 0:
		op.kind = "quota"
		op.q = r.Int63n(201)
		op.desc = fmt.Sprintf("SetQuota(%s,%d)", tenant, op.q)
	case 1:
		op.kind = "versioning"
		op.on = r.Intn(4) != 0
		op.desc = fmt.Sprintf("SetVersioning(%s,%v)", tenant, op.on)
	case 2, 3, 4, 5:
		op.kind = "put"
		op.size = r.Int63n(81)
		op.etag = fmt.Sprintf("e%d-%d", step, r.Intn(3))
		op.c = simRandomCond(r)
		if r.Intn(15) == 0 {
			op.size = r.Int63n(2_000_000_000_001)
		}
		if r.Intn(20) == 0 {
			op.now = r.Int63n(5) * 400_000_000_000
		}
		op.storage = r.Intn(12) == 0
		op.desc = fmt.Sprintf("Put(%s,%s,size=%d,etag=%q,cond=%s,now=%d)%s",
			tenant, key, op.size, op.etag, simCondDesc(op.c), op.now, simFailTag(op.storage))
	default:
		op.kind = "delete"
		switch r.Intn(3) {
		case 0:
			op.ver = -1
		case 1:
			op.ver = r.Int63n(6) - 1
		default:
			op.ver = r.Int63n(30)
		}
		op.c = simRandomCond(r)
		op.storage = r.Intn(12) == 0
		op.desc = fmt.Sprintf("Delete(%s,%s,ver=%d,cond=%s,now=%d)%s",
			tenant, key, op.ver, simCondDesc(op.c), op.now, simFailTag(op.storage))
	}
	return op
}

func simFailTag(fail bool) string {
	if fail {
		return " [AfterReserve=fail]"
	}
	return ""
}

// TestRandomAgainstNaiveModel runs 1500 random operation sequences. Every
// input, output (kind/version/delta), and final state must match the naive
// step-by-step simulation. Logs print input, output and the deciding reason.
func TestRandomAgainstNaiveModel(t *testing.T) {
	verbose := os.Getenv("VERBOSE") == "1"
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq)*1_000_003 + 7))
		model := newNaiveModel()
		sut := NewStore()
		failStorage := false
		sut.SetAfterReserve(func() error {
			if failStorage {
				return errInjected
			}
			return nil
		})
		var log []string
		steps := 40 + r.Intn(40)
		for step := 0; step < steps; step++ {
			op := simRandomOp(r, step)
			failStorage = op.storage
			var got, want simOutcome
			switch op.kind {
			case "quota":
				got.kind = failKind(sut.SetQuota(op.tenant, op.q))
				want = model.setQuota(op.tenant, op.q)
			case "versioning":
				got.kind = failKind(sut.SetVersioning(op.tenant, op.on))
				want = model.setVersioning(op.tenant, op.on)
			case "put":
				res, err := sut.Put(op.tenant, op.key, op.size, op.etag, op.c, op.now)
				got = simOutcome{kind: failKind(err), ver: res.Version, d: res.Delta}
				want = model.put(op.tenant, op.key, op.size, op.etag, op.c, op.now, op.storage)
			case "delete":
				res, err := sut.Delete(op.tenant, op.key, op.ver, op.c, op.now)
				got = simOutcome{kind: failKind(err), ver: res.Version, d: res.Delta}
				want = model.del(op.tenant, op.key, op.ver, op.c, op.now, op.storage)
			}
			entry := fmt.Sprintf("seq=%d step=%d %s -> got=%s(v%d,d%d) want=%s(v%d,d%d)",
				seq, step, op.desc, got.kind, got.ver, got.d, want.kind, want.ver, want.d)
			if verbose {
				t.Log(entry)
			}
			if got != want {
				log = append(log, entry)
			}
		}
		if diff := simDiffState(sut, model); diff != "" {
			log = append(log, "state mismatch:\n"+diff)
		}
		for _, line := range log {
			t.Error(line)
		}
	}
}

func simDiffState(sut *Store, m *naiveModel) string {
	var b strings.Builder
	for _, tenant := range []string{"t1", "t2"} {
		if sut.Quota(tenant) != m.quota[tenant] {
			fmt.Fprintf(&b, "%s quota sut=%d model=%d\n", tenant, sut.Quota(tenant), m.quota[tenant])
		}
		if sut.Used(tenant) != m.used[tenant] {
			fmt.Fprintf(&b, "%s used sut=%d model=%d\n", tenant, sut.Used(tenant), m.used[tenant])
		}
		if sut.Versioning(tenant) != m.versioned[tenant] {
			fmt.Fprintf(&b, "%s versioning sut=%v model=%v\n", tenant, sut.Versioning(tenant), m.versioned[tenant])
		}
		for _, key := range []string{"a", "b", "c"} {
			modelV := m.objects[tenant][key]
			for ver, mv := range modelV {
				sv, ok := sut.VersionSize(tenant, key, ver)
				if !ok || sv != mv.size {
					fmt.Fprintf(&b, "%s %s v%d sut(size=%d,ok=%v) model(size=%d)\n",
						tenant, key, ver, sv, ok, mv.size)
				}
			}
			// Detect versions the store holds but the model removed/never made.
			for ver := int64(0); ver <= 300; ver++ {
				if _, inModel := modelV[ver]; inModel {
					continue
				}
				if sv, ok := sut.VersionSize(tenant, key, ver); ok {
					fmt.Fprintf(&b, "%s %s v%d unexpectedly present in sut(size=%d)\n",
						tenant, key, ver, sv)
				}
			}
			cur := sut.Current(tenant, key)
			mv2 := m.view(tenant, key)
			if cur.Exists != mv2.Exists || cur.Marker != mv2.Marker {
				fmt.Fprintf(&b, "%s %s current existence mismatch sut=%+v model=%+v\n", tenant, key, cur, mv2)
			} else if cur.Exists && !cur.Marker {
				if mcv, _, _ := m.current(tenant, key); cur.Etag != mcv.etag || cur.Mtime != mcv.mtime {
					fmt.Fprintf(&b, "%s %s current data mismatch sut=%+v model etag=%s mtime=%d\n",
						tenant, key, cur, mcv.etag, mcv.mtime)
				}
			}
		}
	}
	return b.String()
}
