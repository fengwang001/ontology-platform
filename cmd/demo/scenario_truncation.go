package main

import (
	"fmt"
	"os"
	"path/filepath"

	"ontology/journal"
)

// scenarioTruncation writes a journal and probes four representative byte
// positions: inside the header, inside a length prefix, inside a record
// body, and at the checksum (a truncated or corrupted CRC). It returns
// the set of classified categories observed.
func scenarioTruncation(tmp string) map[string]bool {
	dir := filepath.Join(tmp, "trunc")
	must(os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "log")

	w, err := journal.Create(path)
	must(err)
	for i := 1; i <= 50; i++ {
		must(w.Append(ins(uint64(i),
			fmt.Sprintf("k%03d", i), "g", float64(i))))
	}
	must(w.Close())

	full, err := os.ReadFile(path)
	must(err)

	cats := map[string]bool{}
	probe := func(cut int) {
		p := filepath.Join(dir, "probe")
		must(os.WriteFile(p, full[:cut], 0o600))
		_, e := journal.ReadAll(p)
		cats[journal.Classify(e)] = true
	}
	// Header interior, length-prefix interior, body interior.
	probe(7)
	probe(journal.HeaderLen + 2)
	probe(journal.HeaderLen + 6) // inside the first record body

	// CRC mismatch: full frame present with a flipped body byte.
	bad := append([]byte(nil), full...)
	bad[journal.HeaderLen+4] ^= 0xFF
	p := filepath.Join(dir, "badcrc")
	must(os.WriteFile(p, bad, 0o600))
	_, e := journal.ReadAll(p)
	cats[journal.Classify(e)] = true
	return cats
}
