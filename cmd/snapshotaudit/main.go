// snapshotaudit 生成随机快照序列（可选随机注入一处损坏），逐条记录
// 每次校验的输入、输出、最大可恢复前缀与损坏原因分类，以 JSONL 落盘。
//
// 用法：
//
//	go run ./cmd/snapshotaudit -trials 200 -out audit.jsonl
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"

	"ontology/snapshot"
)

func sp(s string) *string { return &s }

type auditEntry struct {
	Trial      int               `json:"trial"`
	Seed       int64             `json:"seed"`
	Length     int               `json:"length"`
	InjectAt   int               `json:"inject_at"`
	InjectMode int               `json:"inject_mode"`
	Input      []snapshot.Record `json:"input"`
	Status     string            `json:"status"`
	PrefixLen  int               `json:"max_recoverable_prefix"`
	BadIndex   int               `json:"bad_index"`
	Reason     string            `json:"reason"`
}

func main() {
	trials := flag.Int("trials", 100, "number of generated trials")
	seed := flag.Int64("seed", 42, "base random seed")
	outPath := flag.String("out", "audit.jsonl", "JSONL audit output path ('-' for stdout)")
	flag.Parse()

	out := os.Stdout
	if *outPath != "-" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	enc := json.NewEncoder(out)
	var truncated, complete int
	for trial := 0; trial < *trials; trial++ {
		trialSeed := *seed + int64(trial)*7919
		rng := rand.New(rand.NewSource(trialSeed))
		records := generate(rng)
		injectAt, injectMode := -1, -1
		if rng.Intn(5) != 0 {
			injectAt = rng.Intn(len(records))
			injectMode = rng.Intn(6)
			records = inject(rng, records, injectAt, injectMode)
		}

		result := snapshot.ValidateSlice(records)
		status := "complete"
		if result.Status == snapshot.StatusTruncated {
			status = "truncated"
			truncated++
		} else {
			complete++
		}

		entry := auditEntry{
			Trial: trial, Seed: trialSeed, Length: len(records),
			InjectAt: injectAt, InjectMode: injectMode, Input: records,
			Status: status, PrefixLen: result.PrefixLen,
			BadIndex: result.BadIndex, Reason: result.Reason.String(),
		}
		if err := enc.Encode(entry); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "audit: %d trials (%d complete, %d truncated) -> %s\n",
		*trials, complete, truncated, *outPath)
}

func generate(rng *rand.Rand) []snapshot.Record {
	n := 1 + rng.Intn(15)
	records := make([]snapshot.Record, 0, n)
	var ids []string
	types := []string{"Person", "Org", "Device"}
	links := []string{"knows", "owns", "partOf"}

	for i := 0; i < n; i++ {
		if len(ids) < 2 || rng.Intn(2) == 0 {
			id := fmt.Sprintf("o%d", rng.Intn(n+1))
			records = append(records, snapshot.Record{
				Kind: snapshot.KindObject, ID: sp(id), Type: sp(types[rng.Intn(len(types))]),
			})
			fresh := true
			for _, x := range ids {
				if x == id {
					fresh = false
				}
			}
			if fresh {
				ids = append(ids, id)
			}
			continue
		}
		dir := snapshot.DirForward
		if rng.Intn(2) == 1 {
			dir = snapshot.DirReverse
		}
		records = append(records, snapshot.Record{
			Kind:      snapshot.KindLink,
			SourceID:  sp(ids[rng.Intn(len(ids))]),
			TargetID:  sp(ids[rng.Intn(len(ids))]),
			LinkType:  sp(links[rng.Intn(len(links))]),
			Direction: dir,
		})
	}
	return records
}

func inject(rng *rand.Rand, records []snapshot.Record, idx, mode int) []snapshot.Record {
	out := make([]snapshot.Record, len(records))
	copy(out, records)
	r := out[idx]
	switch mode {
	case 0:
		r.Kind, r.ID, r.Type = snapshot.KindObject, sp(fmt.Sprintf("x%d", idx)), nil
	case 1:
		r.Kind = snapshot.KindLink
		r.SourceID, r.TargetID, r.LinkType = sp("a"), sp("b"), sp("lt")
		r.Direction = snapshot.DirUnknown
	case 2:
		r.Kind = snapshot.KindLink
		r.SourceID, r.TargetID, r.LinkType = nil, sp("b"), sp("lt")
		r.Direction = snapshot.DirForward
	case 3:
		r.Kind = snapshot.KindLink
		r.SourceID = sp(fmt.Sprintf("ghost_%d", rng.Intn(1e6)))
		r.TargetID = sp(fmt.Sprintf("ghost2_%d", rng.Intn(1e6)))
		r.LinkType, r.Direction = sp("lt"), snapshot.DirForward
	case 4:
		r.Kind = snapshot.KindUnknown
	case 5:
		id := "o0"
		for j := idx - 1; j >= 0; j-- {
			if out[j].Kind == snapshot.KindObject && out[j].ID != nil {
				id = *out[j].ID
				break
			}
		}
		r.Kind, r.ID, r.Type = snapshot.KindObject, sp(id), sp("ConflictingType")
	}
	out[idx] = r
	return out
}
