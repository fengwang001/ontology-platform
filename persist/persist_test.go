package persist

import (
	"errors"
	"hash/crc32"
	"math/rand"
	"testing"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/vec"
)

const (
	testDim, testBits, testTables = 4, 6, 3
	testNVec                      = 50
)

func testIndex() (*hyper.Family, *bucket.Index, []vec.Vector) {
	rng := rand.New(rand.NewSource(9))
	fam := hyper.New(5, testDim, testBits, testTables)
	bk := bucket.New(testTables)
	vecs := make([]vec.Vector, testNVec)
	for i := range vecs {
		v := make(vec.Vector, testDim)
		for j := range v {
			v[j] = rng.NormFloat64()
		}
		vecs[i] = v
		sigs, _ := fam.Sign(v)
		bk.Add(i, sigs)
	}
	return fam, bk, vecs
}

func hyperEnd() int { return HeaderLen + testTables*testBits*testDim*8 }

func fixCRC(data []byte) {
	c := crc32.ChecksumIEEE(data[:len(data)-4])
	for i := 0; i < 4; i++ {
		data[len(data)-4+i] = byte(c >> (8 * i))
	}
}

func TestRoundTrip(t *testing.T) {
	fam, bk, vecs := testIndex()
	data := Encode(fam, bk, testNVec)
	fam2, bk2, nvec, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if nvec != testNVec || fam2.Dim() != testDim || fam2.Bits() != testBits {
		t.Fatalf("header: nvec=%d dim=%d bits=%d", nvec, fam2.Dim(), fam2.Bits())
	}
	for i, v := range vecs {
		s1, _ := fam.Sign(v)
		s2, _ := fam2.Sign(v)
		for tb := range s1 {
			if s1[tb] != s2[tb] {
				t.Fatalf("vec %d table %d: sig %b vs reloaded %b", i, tb, s1[tb], s2[tb])
			}
		}
		if len(bk2.Candidates(s2)) != len(bk.Candidates(s1)) {
			t.Fatalf("vec %d: candidate count differs after reload", i)
		}
	}
}

func TestTruncationClassification(t *testing.T) {
	fam, bk, _ := testIndex()
	data := Encode(fam, bk, testNVec)
	classify := func(cut int) error {
		switch {
		case cut < HeaderLen:
			return ErrHeader
		case cut < hyperEnd():
			return ErrHyperplanes
		case cut < len(data)-4:
			return ErrBuckets
		default:
			return ErrCRC
		}
	}
	counts := map[error]int{}
	for cut := 1; cut < len(data); cut++ { // every truncation point
		_, _, _, err := Load(data[:cut])
		if want := classify(cut); !errors.Is(err, want) {
			t.Fatalf("cut=%d: err = %v, want %v", cut, err, want)
		}
		counts[classify(cut)]++
	}
	t.Logf("len=%d header=[1,%d) hyper=[%d,%d) buckets=[%d,%d) crc=[%d,%d)",
		len(data), HeaderLen, HeaderLen, hyperEnd(), hyperEnd(), len(data)-4, len(data)-4, len(data))
	t.Logf("counts: header=%d hyper=%d buckets=%d crc=%d",
		counts[ErrHeader], counts[ErrHyperplanes], counts[ErrBuckets], counts[ErrCRC])
}

func TestRecoverPrefix(t *testing.T) {
	fam, bk, _ := testIndex()
	data := Encode(fam, bk, testNVec)
	totalBuckets := 0
	for _, tbl := range bk.Snapshot() {
		totalBuckets += len(tbl)
	}
	for cut := hyperEnd(); cut < len(data)-4; cut++ { // bucket-region truncations
		rec, err := Recover(data[:cut])
		if err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
		for _, tbl := range rec.Buckets.Snapshot() {
			for _, ids := range tbl {
				for _, id := range ids {
					if id >= rec.NVec {
						t.Fatalf("cut=%d: dangling id %d (nvec=%d)", cut, id, rec.NVec)
					}
				}
			}
		}
	}
	for _, frac := range []float64{0.25, 0.5, 0.75, 1.0} {
		cut := hyperEnd() + int(frac*float64(len(data)-4-hyperEnd()))
		rec, _ := Recover(data[:cut])
		t.Logf("cut=%.0f%% of bucket region: recovered %d/%d buckets",
			frac*100, rec.BucketsKept, totalBuckets)
	}
}

func TestDanglingAndDegenerate(t *testing.T) {
	fam, bk, _ := testIndex()
	// dangling: rewrite table0 bucket0 id0 to 9999, fix CRC, Recover must drop it.
	data := Encode(fam, bk, testNVec)
	idOff := hyperEnd() + 4 + 12
	for i := 0; i < 4; i++ {
		data[idOff+i] = byte(uint32(9999) >> (8 * i))
	}
	fixCRC(data)
	rec, err := Recover(data)
	if err != nil || rec.DroppedIDs != 1 {
		t.Fatalf("Recover: dropped=%d err=%v, want 1 dangling id dropped", rec.DroppedIDs, err)
	}
	for _, tbl := range rec.Buckets.Snapshot() {
		for _, ids := range tbl {
			for _, id := range ids {
				if id >= rec.NVec {
					t.Fatalf("dangling id %d survived recovery", id)
				}
			}
		}
	}
	// degenerate: zero the normal of table 1 bit 2, fix CRC, Load must locate it.
	data2 := Encode(fam, bk, testNVec)
	off := HeaderLen + (1*testBits+2)*testDim*8
	for i := 0; i < testDim*8; i++ {
		data2[off+i] = 0
	}
	fixCRC(data2)
	_, _, _, err = Load(data2)
	var de DegenerateError
	if !errors.Is(err, ErrDegenerate) || !errors.As(err, &de) || de.Table != 1 || de.Bit != 2 {
		t.Fatalf("err = %v, want degenerate at table 1 bit 2", err)
	}
}
