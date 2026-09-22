package main

import (
	"os"
	"path/filepath"

	"ontology/record"
	"ontology/spill"
)

func probeRun(dir string, rs []record.Record, cut int) string {
	must(os.MkdirAll(dir, 0o755))
	w, err := spill.Create(dir, 1, rs)
	must(err)
	must(w.Close(cut))
	return classify(spill.RunPath(dir, 1))
}

func classify(path string) string {
	rc := spill.Recover(path)
	switch {
	case errorIs(rc.HeaderErr, spill.ErrHeaderCRC):
		return "header-crc"
	case errorIs(rc.HeaderErr, spill.ErrHeaderIncomplete), errorIs(rc.HeaderErr, spill.ErrEmptyFile):
		return "header-incomplete"
	case errorIs(rc.TailErr, spill.ErrLengthPrefixIncomplete):
		return "length-incomplete"
	case errorIs(rc.TailErr, spill.ErrRecordIncomplete):
		return "record-incomplete"
	case errorIs(rc.TailErr, spill.ErrCRC):
		return "crc-mismatch"
	default:
		return "clean"
	}
}

func flipByte(path string, off int) {
	data, err := os.ReadFile(path)
	must(err)
	data[off] ^= 0xFF
	must(os.WriteFile(path, data, 0o600))
}

func runBaseline(dir string) []byte {
	p, err := openSort(dir, nil)
	must(err)
	fill(p, 500)
	must(p.Close())
	data, err := os.ReadFile(filepath.Join(dir, "output.dat"))
	must(err)
	return data
}
