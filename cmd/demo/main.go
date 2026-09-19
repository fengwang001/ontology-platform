// Command demo exercises the snapshot package entirely in memory.
// Run with: go run ./cmd/demo
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"ontology"
)

const headerSize = 18

var pass, fail int

func line(ok bool, format string, args ...any) {
	if ok {
		pass++
		fmt.Print("OK   ")
	} else {
		fail++
		fmt.Print("FAIL ")
	}
	fmt.Printf(format+"\n", args...)
}

func encodeV1(recs []snapshot.Record) []byte {
	var region bytes.Buffer
	for _, r := range recs {
		key := []byte(r.Key)
		body := make([]byte, 4+len(key)+8)
		binary.LittleEndian.PutUint32(body, uint32(len(key)))
		copy(body[4:], key)
		binary.LittleEndian.PutUint64(body[4+len(key):], uint64(r.Value))
		frame := make([]byte, 8+len(body))
		binary.LittleEndian.PutUint32(frame, uint32(len(body)))
		copy(frame[4:], body)
		binary.LittleEndian.PutUint32(frame[4+len(body):], crc32.ChecksumIEEE(body))
		region.Write(frame)
	}
	out := make([]byte, headerSize)
	copy(out, "ONTSNAP1")
	binary.LittleEndian.PutUint16(out[8:], 1)
	binary.LittleEndian.PutUint32(out[10:], uint32(len(recs)))
	crc := crc32.ChecksumIEEE(region.Bytes())
	binary.LittleEndian.PutUint32(out[14:], crc)
	return append(out, region.Bytes()...)
}

func main() {
	recs := []snapshot.Record{
		{Key: "gamma", Value: 3, Note: "three"},
		{Key: "alpha", Value: 1, Note: "one"},
		{Key: "beta", Value: 2, Note: "two"},
	}

	var good bytes.Buffer
	_ = snapshot.Write(&good, recs)
	goodBytes := bytes.Clone(good.Bytes())
	round, err := snapshot.Read(bytes.NewReader(goodBytes))
	line(err == nil && len(round) == 3 && round[0].Key == "alpha",
		"normal round trip: %d records sorted, first key %q", len(round), round[0].Key)

	old := encodeV1([]snapshot.Record{{Key: "old", Value: 99}})
	oldRecs, err := snapshot.Read(bytes.NewReader(old))
	var upgraded bytes.Buffer
	_ = snapshot.Write(&upgraded, oldRecs)
	upVer := uint16(upgraded.Bytes()[8]) | uint16(upgraded.Bytes()[9])<<8
	line(err == nil && oldRecs[0].Note == "" && upVer == snapshot.CurrentVersion,
		"v1 file read with Note=%q and rewritten as version %d", oldRecs[0].Note, upVer)

	tampered := bytes.Clone(goodBytes)
	tampered[18+4] ^= 0xFF
	_, strictErr := snapshot.Read(bytes.NewReader(tampered))
	rep := snapshot.Inspect(bytes.NewReader(tampered))
	line(errors.Is(strictErr, snapshot.ErrRecordChecksum) && len(rep.Records) == 0 &&
		rep.BadIndex == 0 && rep.Skipped == 2,
		"tampered byte: Read strict fail, Inspect prefix=%d bad=#%d skipped=%d",
		len(rep.Records), rep.BadIndex, rep.Skipped)

	cut := goodBytes[:headerSize+4+2]
	_, cutErr := snapshot.Read(bytes.NewReader(cut))
	line(errors.Is(cutErr, snapshot.ErrRecordTruncated),
		"mid-record truncation classified as %v", cutErr)

	bigger := bytes.Clone(goodBytes)
	binary.LittleEndian.PutUint32(bigger[10:], 9)
	_, errMore := snapshot.Read(bytes.NewReader(bigger))
	var cmMore *snapshot.CountMismatchError
	errors.As(errMore, &cmMore)

	smaller := bytes.Clone(goodBytes)
	binary.LittleEndian.PutUint32(smaller[10:], 2)
	_, errFewer := snapshot.Read(bytes.NewReader(smaller))
	var cmFewer *snapshot.CountMismatchError
	errors.As(errFewer, &cmFewer)
	line(cmMore != nil && cmMore.ClaimedMore() && cmFewer != nil && !cmFewer.ClaimedMore(),
		"header count bigger (%d>%d) and smaller (%d<%d) are distinct errors",
		cmMore.Claimed, cmMore.Actual, cmFewer.Claimed, cmFewer.Actual)

	var a, b bytes.Buffer
	_ = snapshot.Write(&a, recs)
	_ = snapshot.Write(&b, []snapshot.Record{recs[1], recs[2], recs[0]})
	line(bytes.Equal(a.Bytes(), b.Bytes()),
		"shuffled records serialize to identical bytes (%d bytes)", a.Len())

	fmt.Printf("TOTAL %d OK, %d FAIL\n", pass, fail)
}
