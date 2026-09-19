// Command demo exercises the snapshot package end to end, entirely
// in memory: normal round trip, legacy upgrade, strict vs lenient
// corruption handling, truncation, count mismatches, determinism.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"reflect"

	"ontology/snapshot"
)

var passed, total int

func check(ok bool, label string) {
	total++
	status := "OK  "
	if ok {
		passed++
	} else {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, label)
}

func main() {
	recs := []snapshot.Record{
		{Key: "alpha", Num: 1, Value: "one"},
		{Key: "bravo", Num: 2, Value: "two"},
		{Key: "charlie", Num: 3, Value: "three"},
	}
	var buf bytes.Buffer
	werr := snapshot.Write(&buf, recs)
	good := buf.Bytes()
	back, rerr := snapshot.Read(bytes.NewReader(good))
	check(werr == nil && rerr == nil && reflect.DeepEqual(back, recs),
		"roundtrip: 3 records written and read back intact")

	v1 := buildV1File([]snapshot.Record{{Key: "legacy", Num: 7, Value: "dropped"}})
	old, lerr := snapshot.Read(bytes.NewReader(v1))
	var upgraded bytes.Buffer
	var uerr error
	if lerr == nil {
		uerr = snapshot.Write(&upgraded, old)
	}
	upgradedOK := uerr == nil && upgraded.Len() >= 18 &&
		binary.BigEndian.Uint16(upgraded.Bytes()[len(snapshot.Magic):]) == snapshot.CurrentVersion
	check(lerr == nil && len(old) == 1 && old[0].Value == "" && upgradedOK,
		"legacy v1 file read with defaults, re-written as current version")

	bad := bytes.Clone(good)
	badOff := recordOffset(good, 1)
	bad[badOff+4+1] ^= 0xFF
	_, cerr := snapshot.Read(bytes.NewReader(bad))
	var recErr *snapshot.RecordError
	strict := errors.As(cerr, &recErr) && errors.Is(cerr, snapshot.ErrRecordChecksum) &&
		recErr.Index == 1
	rep := snapshot.Inspect(bytes.NewReader(bad))
	loose := len(rep.Records) == 1 && rep.BadIndex == 1 && rep.RecoverableAfter == 1
	idx, off := -1, int64(-1)
	if recErr != nil {
		idx, off = recErr.Index, recErr.Offset
	}
	check(strict && loose, fmt.Sprintf(
		"corrupt record: strict Read fails (record %d, offset %d); Inspect prefix=%d after=%d",
		idx, off, len(rep.Records), rep.RecoverableAfter))

	trunc := good[:recordOffset(good, 2)+6]
	_, terr := snapshot.Read(bytes.NewReader(trunc))
	check(errors.Is(terr, snapshot.ErrTruncated),
		"truncated mid-record: Read reports the truncation category")

	more := bytes.Clone(good)
	binary.BigEndian.PutUint32(more[len(snapshot.Magic)+2:], 5)
	_, merr := snapshot.Read(bytes.NewReader(more))
	var countErr *snapshot.CountError
	moreOK := errors.As(merr, &countErr) && countErr.Excess &&
		countErr.Claimed == 5 && countErr.Actual == 3
	check(moreOK, "header count too large: count-mismatch error (claims more)")

	less := bytes.Clone(good)
	binary.BigEndian.PutUint32(less[len(snapshot.Magic)+2:], 1)
	_, lerr2 := snapshot.Read(bytes.NewReader(less))
	var countErr2 *snapshot.CountError
	lessOK := errors.As(lerr2, &countErr2) && !countErr2.Excess &&
		countErr2.Claimed == 1 && countErr2.Actual == 3
	check(lessOK, "header count too small: count-mismatch error (claims fewer)")

	shuffled := []snapshot.Record{recs[2], recs[0], recs[1]}
	var buf2 bytes.Buffer
	derr := snapshot.Write(&buf2, shuffled)
	check(derr == nil && bytes.Equal(buf2.Bytes(), good),
		"deterministic: shuffled input yields identical bytes")

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, total)
}

// recordOffset returns the absolute offset of the idx-th record.
func recordOffset(data []byte, idx int) int {
	off := len(snapshot.Magic) + 2 + 4 + 4
	for i := 0; i < idx; i++ {
		n := int(binary.BigEndian.Uint32(data[off:]))
		off += 4 + n + 4
	}
	return off
}

// buildV1File hand-builds a legacy version-1 file (no Value field).
func buildV1File(recs []snapshot.Record) []byte {
	var region []byte
	for _, r := range recs {
		body := binary.BigEndian.AppendUint16(nil, uint16(len(r.Key)))
		body = append(body, r.Key...)
		body = binary.BigEndian.AppendUint64(body, uint64(r.Num))
		frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
		frame = append(frame, body...)
		frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(body))
		region = append(region, frame...)
	}
	out := append([]byte(nil), snapshot.Magic...)
	out = binary.BigEndian.AppendUint16(out, 1)
	out = binary.BigEndian.AppendUint32(out, uint32(len(recs)))
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(region))
	return append(out, region...)
}
