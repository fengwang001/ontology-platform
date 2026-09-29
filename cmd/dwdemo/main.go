// Command dwdemo drives a two-page batch flush, injects a power cut at a
// caller-chosen sector boundary, runs recovery and prints the input, output
// and every decision. It is a local, dependency-free way to observe the
// doublewrite protocol; the authoritative specification lives in README.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	dw "ontology"
)

func main() {
	numPages := flag.Int("pages", 4, "number of in-place pages")
	sectorsPerPage := flag.Int("sectors", 4, "sectors per page")
	sectorSize := flag.Int("sector", 64, "bytes per sector")
	cut := flag.Int("cut", -1, "sector-write index at which to cut power (-1 = no cut)")
	corrupt := flag.Int("corrupt-page", -1, "silently corrupt this in-place page before recovery (-1 = none)")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags)
	total := (*numPages+2)*(*sectorsPerPage) + 1
	disk := dw.NewDisk(total, *sectorSize)
	store := dw.NewStore(disk, dw.Config{
		NumPages:       *numPages,
		SectorsPerPage: *sectorsPerPage,
		BatchCapacity:  2,
	}, logger)

	pageSize := *sectorsPerPage * *sectorSize
	mkPage := func(pageNo, version int) []byte {
		payload := make([]byte, pageSize-20)
		for i := range payload {
			payload[i] = byte(version*7 + pageNo*13 + i)
		}
		img, err := dw.EncodePage(uint32(pageNo), uint64(version), payload, pageSize)
		if err != nil {
			panic(err)
		}
		return img
	}

	for p := 0; p < *numPages; p++ {
		if err := store.Flush([][]byte{mkPage(p, 1)}); err != nil {
			panic(err)
		}
	}

	if *cut >= 0 {
		n := 0
		disk.BeforeSectorWrite = func(idx int) (bool, int) {
			step := n
			n++
			if step == *cut {
				fmt.Printf(">>> power cut at sector write #%d (sector %d), sector torn in half\n", step, idx)
				return true, *sectorSize / 2
			}
			return false, *sectorSize
		}
	}

	batch := [][]byte{mkPage(0, 2), mkPage(1, 2)}
	if err := store.Flush(batch); err != nil {
		if errors.Is(err, dw.ErrPowerCut) {
			fmt.Printf(">>> flush interrupted: %v\n", err)
		} else {
			panic(err)
		}
	}

	if *corrupt >= 0 {
		if err := store.CorruptInPlacePage(*corrupt, 0, 0); err != nil {
			panic(err)
		}
		fmt.Printf(">>> injected silent corruption into in-place page %d\n", *corrupt)
	}

	report, err := store.Recover()
	if err != nil {
		panic(err)
	}
	fmt.Printf(">>> marker valid: %v\n", report.MarkerValid)
	for _, d := range report.Decisions {
		fmt.Printf(">>> page %d: in-place(valid=%v ver=%d) copy(valid=%v ver=%d) => %s%s\n",
			d.PageNo, d.InPlaceValid, d.InPlaceVer, d.CopyValid, d.CopyVer, d.Action,
			map[bool]string{true: " [UNRECOVERABLE]", false: ""}[d.Unrecoverable])
	}

	for p := 0; p < *numPages; p++ {
		_, info, err := store.ReadPage(p)
		if err != nil {
			fmt.Printf(">>> final page %d: corrupt (version counts as 0): %v\n", p, err)
			continue
		}
		fmt.Printf(">>> final page %d: version %d\n", p, info.Version)
	}
}
