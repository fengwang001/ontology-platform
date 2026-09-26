// Command demo runs the consistency-exporter acceptance checks.
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/export"
	"ontology/snapshot"
	"ontology/store"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func fill(st *store.Store, n int, val string) {
	for i := 0; i < n; i++ {
		st.Set(fmt.Sprintf("key-%06d", i), []byte(fmt.Sprintf("%s-%06d", val, i)))
	}
}

func main() {
	st := store.New()
	mgr := snapshot.NewManager(st)

	// store: concurrent writers stay consistent with the version watermark.
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				st.Set(fmt.Sprintf("w%d-%03d", w, i), []byte("x"))
			}
		}(w)
	}
	wg.Wait()
	check("store: concurrent writes, version watermark", st.Version() == 1600)

	// snapshot: a key overwritten after the snapshot still reads the old value.
	st.Set("cow", []byte("old"))
	snap := mgr.Open()
	st.Set("cow", []byte("new"))
	v, ok, _ := snap.Get("cow")
	check("snapshot: overwritten key keeps old value", ok && string(v) == "old")
	snap.Close()

	// two overlapping snapshots: preserved values freed only after both close.
	s1, s2 := mgr.Open(), mgr.Open()
	for i := 0; i < 10; i++ {
		st.Set(fmt.Sprintf("w0-%03d", i), []byte("y"))
	}
	both := s1.PreservedCount() > 0 && s2.PreservedCount() > 0
	s1.Close()
	held := s2.PreservedCount() > 0
	s2.Close()
	check("overlap: released only after both close", both && held && s2.PreservedCount() == 0)

	// COW memory: preserved values bounded by overwritten keys, not total keys.
	big := store.New()
	bigMgr := snapshot.NewManager(big)
	fill(big, 100000, "v")
	bs := bigMgr.Open()
	for i := 0; i < 100; i++ {
		big.Set(fmt.Sprintf("key-%06d", i*1000), []byte("changed"))
	}
	check("preserved <= overwritten keys (100k keys, 100 writes)", bs.PreservedCount() == 100)
	bs.Close()

	dir, err := os.MkdirTemp("", "ontology-demo")
	if err != nil {
		fmt.Println("FAIL tempdir", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	// export: key overwritten mid-export keeps its snapshot-time old value.
	est := store.New()
	emgr := snapshot.NewManager(est)
	fill(est, 100, "one")
	es := emgr.Open()
	keys := es.Keys()
	est.Set(keys[79], []byte("NEW")) // key #80 rewritten while snapshot open
	p1 := dir + "/full.bin"
	r1, err := export.Export(es, p1, keys, 256)
	oldOK := err == nil
	v80, _, _ := es.Get(keys[79])
	check("export: key rewritten mid-export stays old value", oldOK && string(v80) == "one-000079")

	// export order does not matter: asc vs desc give the same total checksum.
	rev := make([]string, len(keys))
	for i, k := range keys {
		rev[len(keys)-1-i] = k
	}
	r2, err2 := export.Export(es, dir+"/desc.bin", rev, 256)
	check("order-independent: same total checksum", err2 == nil && r1.Manifest.TotalCRC == r2.Manifest.TotalCRC)

	// exactly one read per key.
	es2 := emgr.Open()
	keys2 := es2.Keys()
	_, err3 := export.Export(es2, dir+"/reads.bin", keys2, 256)
	check("reads == number of keys", err3 == nil && es2.ReadCount() == int64(len(keys2)))
	es2.Close()

	// resume from every chunk boundary reproduces the full file byte-for-byte.
	full, _ := os.ReadFile(p1)
	nchunks := len(r1.Manifest.Chunks)
	resumeOK := true
	for k := 1; k <= nchunks; k++ {
		pp := fmt.Sprintf("%s/part-%d.bin", dir, k)
		if _, err := export.ExportPartial(es, pp, keys, 256, k-1); err != nil {
			resumeOK = false
			break
		}
		if _, err := export.Resume(es, pp, keys, 256); err != nil {
			resumeOK = false
			break
		}
		got, _ := os.ReadFile(pp)
		if string(got) != string(full) {
			resumeOK = false
			break
		}
	}
	check("resume from every chunk k: byte-identical", resumeOK)

	// chunk memory residency stays within one chunk plus one entry.
	peakOK := r1.Stats.PeakChunkBytes <= 256+64
	check("peak chunk memory bounded", peakOK)

	// truncation classes: manifest / chunk header / chunk body / CRC.
	full, _ = os.ReadFile(p1)
	mLen := int(full[4]) | int(full[5])<<8 | int(full[6])<<16 | int(full[7])<<24
	headEnd := 8 + mLen
	body0 := headEnd + 16
	classOK := true
	for _, tc := range []struct {
		n   int
		err error
	}{
		{headEnd - 1, manifest.ErrManifestIncomplete},
		{headEnd + 5, manifest.ErrChunkHeaderIncomplete},
		{body0 + 3, manifest.ErrChunkBodyIncomplete},
	} {
		if _, err := verify.VerifyBytes(full[:tc.n]); !errors.Is(err, tc.err) {
			classOK = false
		}
	}
	flipped := append([]byte(nil), full...)
	flipped[body0+1] ^= 0xff
	if _, err := verify.VerifyBytes(flipped); !errors.Is(err, manifest.ErrCRCMismatch) {
		classOK = false
	}
	check("truncation classes + CRC mismatch", classOK)

	// shuffled or missing chunks are named by number.
	c1 := manifest.ChunksBytes(r1.Manifest.Chunks, 1)
	c2 := manifest.ChunksBytes(r1.Manifest.Chunks, 2)
	shuf := append([]byte(nil), full[:headEnd]...)
	shuf = append(shuf, full[headEnd+c1:headEnd+c2]...)
	shuf = append(shuf, full[headEnd:headEnd+c1]...)
	shuf = append(shuf, full[headEnd+c2:]...)
	_, shufErr := verify.VerifyBytes(shuf)
	missing := append([]byte(nil), full[:headEnd+c1]...)
	missing = append(missing, full[headEnd+c2:]...)
	_, missErr := verify.VerifyBytes(missing)
	named := errors.Is(shufErr, manifest.ErrChunkMissing) && strings.Contains(shufErr.Error(), "1") &&
		errors.Is(missErr, manifest.ErrChunkMissing) && strings.Contains(missErr.Error(), "2")
	check("shuffled/missing chunks named by number", named)

	// expired snapshot: resume refused, partial file untouched.
	pp := dir + "/expired.bin"
	if _, err := export.ExportPartial(es, pp, keys, 256, 2); err != nil {
		check("expired snapshot setup", false)
	}
	before, _ := os.ReadFile(pp)
	es.Close()
	_, expErr := export.Resume(es, pp, keys, 256)
	after, _ := os.ReadFile(pp)
	check("expired snapshot: resume refused, partial kept",
		errors.Is(expErr, export.ErrSnapshotExpired) && string(before) == string(after))

	fmt.Printf("TOTAL %d checks, %d failed\n", 12, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
