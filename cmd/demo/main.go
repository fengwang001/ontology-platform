// Command demo runs the LSH bucket retriever acceptance checks.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"ontology/hyper"
	"ontology/persist"
	"ontology/search"
	"ontology/vec"
)

const (
	dims     = 32
	nVec     = 5000
	nQuery   = 100
	dataSeed = 7
)

var passed, failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

// genData makes n clustered vectors: 50 random centers plus small noise,
// so nearest neighbors are meaningful for recall measurement.
func genData(n int, seed int64) []vec.Vec {
	r := rand.New(rand.NewSource(seed))
	centers := make([]vec.Vec, 50)
	for i := range centers {
		c := make(vec.Vec, dims)
		for j := range c {
			c[j] = r.NormFloat64()
		}
		centers[i] = c
	}
	out := make([]vec.Vec, n)
	for i := range out {
		v := make(vec.Vec, dims)
		c := centers[r.Intn(len(centers))]
		for j := range v {
			v[j] = c[j] + 0.15*r.NormFloat64()
		}
		out[i] = v
	}
	return out
}

// sigTable hashes every vector with every family and serializes the
// signatures, giving a byte-comparable reproducibility fingerprint.
func sigTable(fams []*hyper.Family, vecs []vec.Vec) []byte {
	buf := new(bytes.Buffer)
	tmp := make([]byte, 8)
	for _, f := range fams {
		for _, v := range vecs {
			binary.LittleEndian.PutUint64(tmp, f.Signature(v))
			buf.Write(tmp)
		}
	}
	return buf.Bytes()
}

func build(vecs []vec.Vec, bits, tables int) *search.Index {
	ix := search.New(dims, bits, tables, 1000)
	for _, v := range vecs {
		if err := ix.Add(v); err != nil {
			panic(err)
		}
	}
	return ix
}

// eval returns mean top-10 recall vs brute force and mean candidate count.
func eval(ix *search.Index, vecs, queries []vec.Vec) (recall, cands float64) {
	ix.ResetStats()
	for _, q := range queries {
		approx, err := ix.Query(q, 10)
		if err != nil {
			panic(err)
		}
		exact := search.BruteForce(vecs, q, 10)
		set := make(map[int]bool, len(exact))
		for _, id := range exact {
			set[id] = true
		}
		hit := 0
		for _, id := range approx {
			if set[id] {
				hit++
			}
		}
		recall += float64(hit) / 10
	}
	return recall / float64(len(queries)),
		float64(ix.DistCount()) / float64(len(queries))
}

func main() {
	all := genData(nVec+nQuery, dataSeed)
	vecs, queries := all[:nVec], all[nVec:]

	famsA := []*hyper.Family{hyper.New(42, dims, 8), hyper.New(43, dims, 8)}
	famsB := []*hyper.Family{hyper.New(42, dims, 8), hyper.New(43, dims, 8)}
	famsC := []*hyper.Family{hyper.New(99, dims, 8), hyper.New(98, dims, 8)}
	same := bytes.Equal(sigTable(famsA, vecs), sigTable(famsB, vecs))
	diff := !bytes.Equal(sigTable(famsA, vecs), sigTable(famsC, vecs))
	check("reproducible-signatures", same && diff, "same seed byte-identical, other seed differs")

	ls := []int{1, 2, 4, 8}
	recalls := make([]float64, len(ls))
	var cands8 float64
	for i, l := range ls {
		recalls[i], cands8 = eval(build(vecs, 8, l), vecs, queries)
	}
	check("recall-at-10", recalls[3] >= 0.6, fmt.Sprintf("L=8 recall=%.3f >= 0.6", recalls[3]))
	mono := recalls[1] >= recalls[0] && recalls[2] >= recalls[1] && recalls[3] >= recalls[2]
	check("recall-monotonic-L", mono, fmt.Sprintf("L=1/2/4/8 recall=%.3f/%.3f/%.3f/%.3f",
		recalls[0], recalls[1], recalls[2], recalls[3]))

	c4, c8, c12 := 0.0, 0.0, 0.0
	for i, b := range []int{4, 8, 12} {
		_, c := eval(build(vecs, b, 8), vecs, queries)
		switch i {
		case 0:
			c4 = c
		case 1:
			c8 = c
		case 2:
			c12 = c
		}
	}
	check("candidates-decrease-bits", c4 > c8 && c8 > c12,
		fmt.Sprintf("b=4/8/12 cand=%.0f/%.0f/%.0f", c4, c8, c12))

	check("dist-count-bounded", cands8 <= float64(nVec)/10,
		fmt.Sprintf("avg dist computations=%.0f <= %d", cands8, nVec/10))

	ix := search.New(dims, 8, 8, 1000)
	ix.ResetHashCount()
	for _, v := range vecs {
		if err := ix.Add(v); err != nil {
			panic(err)
		}
	}
	check("hash-count-exact", ix.HashCount() == uint64(nVec*8*8),
		fmt.Sprintf("hashes=%d = %d*%d*%d", ix.HashCount(), nVec, 8, 8))

	same100 := make([]vec.Vec, 100)
	for i := range same100 {
		same100[i] = vec.Vec{1, 2, 3}
	}
	ixSame := search.New(3, 8, 4, 5)
	for _, v := range same100 {
		if err := ixSame.Add(v); err != nil {
			panic(err)
		}
	}
	got, err := ixSame.Query(vec.Vec{1, 2, 3}, 10)
	check("identical-vectors-recall-1", err == nil && len(got) == 10,
		"all in one bucket, candidates=N, recall=1")

	dir, err := os.MkdirTemp("", "lshdemo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	fams, tabs, count := ix.Snapshot()
	path := filepath.Join(dir, "index.bin")
	if err := persist.Save(path, fams, tabs, count); err != nil {
		panic(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	hyperBytes := 8 * 8 * dims * 8
	cuts := []struct {
		cut  int
		want error
	}{
		{10, persist.ErrHeader},
		{20 + 100, persist.ErrHyper},
		{20 + hyperBytes + 5, persist.ErrBuckets},
		{len(raw) - 2, persist.ErrCRC},
	}
	classified := 0
	for _, c := range cuts {
		p := filepath.Join(dir, "trunc.bin")
		if err := os.WriteFile(p, raw[:c.cut], 0o644); err != nil {
			panic(err)
		}
		if _, err := persist.Load(p); errors.Is(err, c.want) {
			classified++
		}
	}
	check("truncation-classes", classified == 4, fmt.Sprintf("%d/4 classified", classified))

	data, rec, err := persist.Recover(path)
	dangling := 0
	if err == nil {
		for _, t := range data.Tabs.Dump() {
			for _, ids := range t {
				for _, id := range ids {
					if id >= data.Count {
						dangling++
					}
				}
			}
		}
	}
	check("recovery-no-dangling", err == nil && dangling == 0 && rec.Buckets == tabs.Buckets(),
		fmt.Sprintf("buckets=%d dangling=%d", rec.Buckets, dangling))

	fmt.Printf("TOTAL %d/%d OK\n", passed, passed+failed)
	if failed > 0 {
		panic("checks failed")
	}
}
