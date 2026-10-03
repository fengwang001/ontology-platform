package auditlog

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"sync"
	"testing"
)

// hashEvolve/hashRekey/hashMac fold SHA-256 into uint64. Unlike the linear
// example functions, this key schedule is not invertible, so a key at index
// c does not reveal keys for earlier indices.
func hashDigest(parts ...[]byte) uint64 {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}

func putU64(v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return buf[:]
}

func hashEvolve(k uint64) uint64 {
	return hashDigest([]byte("evolve"), putU64(k))
}

func hashRekey(k uint64, i int64) uint64 {
	return hashDigest([]byte("rekey"), putU64(k), putU64(uint64(i)))
}

func hashMac(k uint64, i int64, typ int32, ts int64, data []byte) uint64 {
	return hashDigest([]byte("mac"), putU64(k), putU64(uint64(i)),
		putU64(uint64(typ)), putU64(uint64(ts)), data)
}

// forge builds entries starting at index start with initial key k, following
// the same rules as the writer.
func forge(k uint64, start int64, plan []struct {
	typ int32
	ts  int64
	dat string
}) []*Entry {
	var out []*Entry
	i := start
	for _, p := range plan {
		var data []byte
		if p.typ == TypData {
			data = []byte(p.dat)
		} else {
			data = []byte(strconv.FormatInt(i, 10))
		}
		e := &Entry{Index: i, Typ: p.typ, Ts: p.ts, Data: data,
			Tag: hashMac(k, i, p.typ, p.ts, data)}
		out = append(out, e)
		if p.typ == TypRekey {
			k = hashRekey(k, i)
		} else {
			k = hashEvolve(k)
		}
		i++
	}
	return out
}

func TestForwardSecurityFromCheckpointKey(t *testing.T) {
	l, err := New(777, 100, 64, hashEvolve, hashRekey, hashMac)
	if err != nil {
		t.Fatal(err)
	}
	// Genuine prefix: data, data, then export the key at c=2.
	_ = l.Append(10, []byte("genuine-0"))
	_ = l.Append(11, []byte("genuine-1"))
	c := int64(2)
	kcAt2, kc, err := l.Export()
	if err != nil || kcAt2 != c {
		t.Fatalf("export = (%d,%d),%v", kcAt2, kc, err)
	}

	genuine := l.Entries()

	// Attacker holding only key kc at index c rewrites index c and later with
	// arbitrary content, including a rekey, and seals.
	attackerTail := forge(kc, c, []struct {
		typ int32
		ts  int64
		dat string
	}{
		{TypData, 11, "forged-2"},
		{TypRekey, 12, ""},
		{TypData, 12, "forged-4"},
		{TypSeal, 13, ""},
	})
	forged := append([]*Entry{}, genuine...)
	forged = append(forged, attackerTail...)

	// The verifier trusts the origin checkpoint and the checkpoint at c.
	cps := []Checkpoint{{0, 777}, {c, kc}}
	r, _ := l.Verify(forged, cps)
	if !r.OK() || r.Verified != int64(len(forged)) || !r.Sealed {
		t.Fatalf("attacker rewrite should verify: %+v", r)
	}
	if r.RekeyCalls != 1 || r.EvolveCalls != int64(len(forged)-1) {
		t.Fatalf("counts = %+v", r)
	}

	// With only the checkpoint key at c, entries before c are not inspected;
	// the origin checkpoint below makes any earlier change detectable.
	bad := cloneEntries(forged)
	bad[0].Data = []byte("tampered-before-c")
	r, _ = l.Verify(bad, cps)
	if r.Kind != KindTampered || r.Pos != 0 {
		t.Fatalf("prefix tamper with origin cp = %+v", r)
	}

	bad = cloneEntries(forged)
	bad[1].Data = []byte("tampered-before-c")
	// Tag mismatch fires first at position 1 when the origin key is present.
	r, _ = l.Verify(bad, cps)
	if r.Kind != KindTampered || r.Pos != 1 {
		t.Fatalf("prefix tamper at 1 = %+v", r)
	}

	// The honest checkpoint key at c catches a subtly different prefix even
	// when an attacker tries to keep tags consistent: changing entry 1's ts
	// changes neither keys nor the tag, so additionally mutate without the
	// honest key by recomputing with a wrong key -> conflict at c.
	bad = cloneEntries(forged)
	wrongK1 := hashEvolve(777) ^ 1
	bad[1].Tag = hashMac(wrongK1, 1, TypData, 11, bad[1].Data)
	r, _ = l.Verify(bad, cps)
	if r.Kind != KindTampered || r.Pos != 1 {
		t.Fatalf("wrong-key tag at 1 = %+v", r)
	}
}

func TestConcurrentUse(t *testing.T) {
	l, err := New(42, 100000, 64, hashEvolve, hashRekey, hashMac)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 16
	const perWriter = 200
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < perWriter; n++ {
				if err := l.Append(10, []byte("x")); err != nil {
					t.Errorf("append: %v", err)
					return
				}
				// Concurrent snapshots must never observe a broken state.
				if _, err := l.SelfVerify([]Checkpoint{{0, 42}}); err != nil {
					t.Errorf("selfverify: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	es := l.Entries()
	if len(es) != writers*perWriter {
		t.Fatalf("entries = %d", len(es))
	}
	r, err := l.SelfVerify([]Checkpoint{{0, 42}})
	if err != nil || !r.OK() {
		t.Fatalf("final verify = %+v, %v", r, err)
	}
}
