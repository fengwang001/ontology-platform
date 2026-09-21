package bloom

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

const (
	testN = 10000
	testP = 0.01
)

func elem(prefix string, i int) []byte {
	return []byte(fmt.Sprintf("%s-%d", prefix, i))
}

func newTestFilter(t *testing.T) *Filter {
	t.Helper()
	f, err := New(testN, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return f
}

// 零假阴性：插入过的一万个元素必须全部命中。
func TestNoFalseNegatives(t *testing.T) {
	f := newTestFilter(t)
	for i := 0; i < testN; i++ {
		f.Add(elem("in", i))
	}
	for i := 0; i < testN; i++ {
		if !f.MayContain(elem("in", i)) {
			t.Fatalf("false negative at %d", i)
		}
	}
}

// 实测假阳性率必须落在 [0, 2p]。
func TestFalsePositiveRate(t *testing.T) {
	f := newTestFilter(t)
	for i := 0; i < testN; i++ {
		f.Add(elem("in", i))
	}
	const queries = 100000
	var hits int
	for i := 0; i < queries; i++ {
		if f.MayContain(elem("out", i)) {
			hits++
		}
	}
	fpr := float64(hits) / queries
	t.Logf("measured FPR=%.5f, target p=%v, bound 2p=%v", fpr, testP, 2*testP)
	if fpr < 0 || fpr > 2*testP {
		t.Fatalf("FPR %v out of [0, %v]", fpr, 2*testP)
	}
}

// 同一批元素以任意顺序插入，位数组逐字节相同。
func TestDeterministicShuffledOrder(t *testing.T) {
	a := newTestFilter(t)
	b := newTestFilter(t)
	for i := 0; i < testN; i++ {
		a.Add(elem("in", i))
	}
	perm := rand.New(rand.NewSource(42)).Perm(testN)
	for _, i := range perm {
		b.Add(elem("in", i))
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("bit arrays differ for shuffled insertion order")
	}
}

// 同一元素重复插入一百万次，位数组与只插一次逐字节相同。
func TestDeterministicRepeatedInsert(t *testing.T) {
	once := newTestFilter(t)
	many := newTestFilter(t)
	e := elem("dup", 0)
	once.Add(e)
	for i := 0; i < 1000000; i++ {
		many.Add(e)
	}
	if !bytes.Equal(once.Bytes(), many.Bytes()) {
		t.Fatal("bit arrays differ after repeated insert of same element")
	}
}
