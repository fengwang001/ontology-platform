// Command demo runs the LSH approximate-nearest-neighbor checks end to end.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math/rand"
	"os"
	"path/filepath"
	"slices"

	"ontology/hyper"
	"ontology/persist"
	"ontology/search"
	"ontology/vec"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	if ok {
		passed++
	}
	status := map[bool]string{true: "OK  ", false: "FAIL"}[ok]
	fmt.Printf("%s %s: %s\n", status, name, detail)
}

func must(err error) {
	if err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
}

// genClustered makes n points around random centers plus nq held-out queries.
func genClustered(n, dim, clusters, nq int, seed int64) ([]vec.Vector, []vec.Vector) {
	rng := rand.New(rand.NewSource(seed))
	centers := make([]vec.Vector, clusters)
	for i := range centers {
		c := make(vec.Vector, dim)
		for j := range c {
			c[j] = 4 * rng.NormFloat64()
		}
		centers[i] = c
	}
	gen := func(m int) []vec.Vector {
		out := make([]vec.Vector, m)
		for i := range out {
			c := centers[rng.Intn(clusters)]
			v := make(vec.Vector, dim)
			for j := range v {
				v[j] = c[j] + rng.NormFloat64()
			}
			out[i] = v
		}
		return out
	}
	return gen(n), gen(nq)
}

func build(vecs []vec.Vector, dim, bits, tables int) *search.Index {
	ix := search.New(dim, bits, tables, 42)
	for _, v := range vecs {
		_ = ix.Add(v)
	}
	return ix
}

func recall(ix *search.Index, exact [][]int, qs []vec.Vector, k int) float64 {
	sum := 0.0
	for i, q := range qs {
		got, _ := ix.Query(q, k)
		hit := 0
		for _, g := range got {
			for _, e := range exact[i] {
				if g == e {
					hit++
				}
			}
		}
		sum += float64(hit) / float64(k)
	}
	return sum / float64(len(qs))
}

func main() {
	const dim, n, nq, k = 32, 5000, 100, 10
	vecs, queries := genClustered(n, dim, 100, nq, 7)
	exact := make([][]int, nq)
	for i, q := range queries {
		exact[i] = search.BruteForce(vecs, q, k)
	}

	// hyper: same seed rebuilds byte-identical signatures; different seed differs.
	fa, fb := hyper.New(42, dim, 8, 4), hyper.New(42, dim, 8, 4)
	fc := hyper.New(43, dim, 8, 4)
	same, diff := true, false
	for _, v := range vecs[:200] {
		sa, _ := fa.Sign(v)
		sb, _ := fb.Sign(v)
		sc, _ := fc.Sign(v)
		same = same && slices.Equal(sa, sb)
		diff = diff || !slices.Equal(sa, sc)
	}
	check("same-seed signatures identical", same && diff,
		fmt.Sprintf("same=%v diff-seed-differs=%v", same, diff))

	// search: recall vs table count L (bits=8), hash ops, dist ops per query.
	ls := []int{1, 2, 4, 8}
	recalls, candL := make([]float64, len(ls)), make([]float64, len(ls))
	var hashOps int64
	for i, l := range ls {
		ix := build(vecs, dim, 8, l)
		if l == 8 {
			hashOps = ix.Family().HashOps() // before any query signs
		}
		ix.ResetDistOps()
		recalls[i] = recall(ix, exact, queries, k)
		candL[i] = float64(ix.DistOps()) / nq
	}
	check("hash ops exact", hashOps == int64(n*8*8), fmt.Sprintf("got=%d want=%d", hashOps, n*8*8))
	check("top10 recall >= 0.6", recalls[3] >= 0.6, fmt.Sprintf("recall@L=8 %.3f", recalls[3]))
	check("recall monotone in L", slices.IsSorted(recalls), fmt.Sprintf("L=1/2/4/8 %.3f/%.3f/%.3f/%.3f",
		recalls[0], recalls[1], recalls[2], recalls[3]))
	check("dist ops <= 10% of n", candL[3] <= n/10, fmt.Sprintf("avg per query %.0f <= %d", candL[3], n/10))

	// search: candidate count decreases with bits b (L=4).
	cands := make([]float64, 3)
	for i, b := range []int{4, 8, 12} {
		ix := build(vecs, dim, b, 4)
		ix.ResetDistOps()
		for _, q := range queries {
			_, _ = ix.Query(q, k)
		}
		cands[i] = float64(ix.DistOps()) / nq
	}
	check("candidates decrease in b", cands[0] > cands[1] && cands[1] > cands[2],
		fmt.Sprintf("b=4/8/12 %.0f/%.0f/%.0f", cands[0], cands[1], cands[2]))

	// edge: all-identical vectors land in one bucket, recall must be 1.
	ixSame := search.New(3, 8, 8, 1)
	for i := 0; i < 200; i++ {
		_ = ixSame.Add(vec.Vector{1, 2, 3})
	}
	got, _ := ixSame.Query(vec.Vector{1, 2, 3}, 10)
	check("identical vectors recall 1", len(got) == 10, fmt.Sprintf("candidates=200 top10=%d", len(got)))

	// persist: one truncation example per class, classified by errors.Is.
	dir, err := os.MkdirTemp("", "lshdemo")
	must(err)
	defer os.RemoveAll(dir)
	small := build(vecs[:60], dim, 4, 2)
	path := filepath.Join(dir, "index.olsh")
	must(persist.Save(path, small.Family(), small.Buckets(), small.Len()))
	data, err := os.ReadFile(path)
	must(err)
	hyperEnd := persist.HeaderLen + 2*4*dim*8
	cuts := []struct {
		at   int
		want error
	}{
		{10, persist.ErrHeader},
		{persist.HeaderLen + 8, persist.ErrHyperplanes},
		{hyperEnd + 5, persist.ErrBuckets},
		{len(data) - 1, persist.ErrCRC},
	}
	classesOK := true
	for _, c := range cuts {
		_, _, _, err := persist.Load(data[:c.at])
		classesOK = classesOK && errors.Is(err, c.want)
	}
	check("truncation classes x4", classesOK, fmt.Sprintf("cuts at %d/%d/%d/%d",
		cuts[0].at, cuts[1].at, cuts[2].at, cuts[3].at))

	// persist: recovery keeps a self-consistent prefix, no dangling IDs.
	mid := hyperEnd + (len(data)-4-hyperEnd)/2
	rec, err := persist.Recover(data[:mid])
	noDangle := err == nil && rec.BucketsKept > 0
	for _, tbl := range rec.Buckets.Snapshot() {
		for _, ids := range tbl {
			noDangle = noDangle && (len(ids) == 0 || slices.Max(ids) < rec.NVec)
		}
	}
	bad := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(bad[hyperEnd+16:], 9999) // table0 bucket0 id0
	binary.LittleEndian.PutUint32(bad[len(bad)-4:], crc32.ChecksumIEEE(bad[:len(bad)-4]))
	rec2, err2 := persist.Recover(bad)
	check("recovery no dangling ids", noDangle && err2 == nil && rec2.DroppedIDs >= 1,
		fmt.Sprintf("kept=%d dropped=%d", rec.BucketsKept, rec2.DroppedIDs))

	fmt.Printf("SUMMARY %d/%d checks passed\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
