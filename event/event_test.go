package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		name string
		e    Event
	}{
		{"empty payload", Event{Seq: 1, Payload: nil}},
		{"empty bytes", Event{Seq: 2, Payload: []byte{}}},
		{"single byte", Event{Seq: 3, Payload: []byte{0xAB}}},
		{"text payload", Event{Seq: 1 << 40, Payload: []byte("hello log")}},
		{"binary payload", Event{Seq: 0, Payload: []byte{0, 1, 2, 3, 255, 0, 128}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := tc.e.Encode()
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if got := RecordLen(len(tc.e.Payload)); got != len(rec) {
				t.Fatalf("RecordLen=%d want %d", got, len(rec))
			}
			out, err := DecodeRecord(rec)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if out.Seq != tc.e.Seq || !bytes.Equal(out.Payload, tc.e.Payload) {
				t.Fatalf("roundtrip=%+v want %+v", out, tc.e)
			}
		})
	}
}

func TestCorruptRecord(t *testing.T) {
	good, err := Event{Seq: 7, Payload: []byte("abc")}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	badLen := append([]byte{}, good...)
	badLen[3] = 0xFF // 长度前缀声明过大，与记录实际长度不符
	badCRC := append([]byte{}, good...)
	badCRC[len(badCRC)-1] ^= 0xFF
	cases := []struct {
		name string
		rec  []byte
		want error
	}{
		{"short", good[:MinRecordLen-1], ErrShortRecord},
		{"declared too large", badLen, ErrLenMismatch},
		{"crc mismatch", badCRC, ErrCRCMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeRecord(tc.rec); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}
