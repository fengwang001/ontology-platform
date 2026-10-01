package dnsname

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestBuilderCompressionAndCase(t *testing.T) {
	b := NewBuilder()
	first := labels("mixed", "example")
	if err := b.WriteName(first); err != nil {
		t.Fatalf("write first: %v", err)
	}

	tests := []struct {
		name   string
		labels [][]byte
		want   []byte
	}{
		{"full-name hit", labels("Mixed", "Example"), []byte{0xc0, 0x0c}},
		{"suffix hit", labels("WWW", "Mixed", "Example"), []byte{0x03, 'W', 'W', 'W', 0xc0, 0x0c}},
		{"root", nil, []byte{0}},
	}
	offsets := []int{27, 29, 35}
	for _, tc := range tests {
		before := len(b.Bytes())
		if err := b.WriteName(tc.labels); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := b.Bytes()[before:]
		decoded, consumed, err := Decode(b.Bytes(), before, 12)
		if err != nil {
			t.Fatalf("%s decode: %v", tc.name, err)
		}
		t.Logf("input=%q output=% x decoded=%q consumed=%d basis=%s", tc.labels, got, decoded, consumed, tc.name)
		if !bytes.Equal(got, tc.want) || !sameASCIINames(decoded, tc.labels) || consumed != len(tc.want) {
			t.Fatalf("%s mismatch", tc.name)
		}
	}

	decoded, consumed, err := Decode(b.Bytes(), offsets[0], 12)
	wantCase := labels("mixed", "example")
	if err != nil || !sameLabels(decoded, wantCase) || consumed != 2 {
		t.Fatalf("full hit case/consumed: %q %d %v", decoded, consumed, err)
	}
	t.Logf("input=%q output=% x decoded=%q basis=pointer preserves first registration case", labels("Mixed", "Example"), b.Bytes()[offsets[0]:offsets[0]+2], decoded)
}

func TestRegistrationOffsetThreshold(t *testing.T) {
	for _, start := range []int{16383, 16384} {
		name := fmt.Sprintf("start %d", start)
		b, err := NewBuilderAt(12)
		if err != nil {
			t.Fatal(err)
		}
		fillerBytes := start - 12
		filler := bytes.Repeat([]byte{1, 'x'}, fillerBytes/2)
		if fillerBytes%2 == 1 {
			filler = append(filler, 0)
		}
		b.message = append(b.message, filler...)
		if len(b.Bytes()) != start {
			t.Fatalf("%s: start = %d", name, len(b.Bytes()))
		}

		if err := b.WriteName(labels("alpha")); err != nil {
			t.Fatal(err)
		}
		secondStart := len(b.Bytes())
		second := labels("beta", "alpha")
		if err := b.WriteName(second); err != nil {
			t.Fatal(err)
		}
		wire := b.Bytes()[secondStart:]
		decoded, consumed, err := Decode(b.Bytes(), secondStart, 12)
		if err != nil || !sameLabels(decoded, second) {
			t.Fatalf("%s decode: %q %v", name, decoded, err)
		}
		t.Logf("input=%q output=% x decoded=%q consumed=%d basis=%s", second, wire, decoded, consumed, name)
		if start == 16383 && (consumed != 7 || !bytes.Equal(wire, []byte{4, 'b', 'e', 't', 'a', 0xff, 0xff})) {
			t.Fatalf("%s should register and compress: % x/%d", name, wire, consumed)
		}
		if start == 16384 && (consumed != 12 || wire[len(wire)-1] != 0) {
			t.Fatalf("%s should not register: % x/%d", name, wire, consumed)
		}
	}
}

func TestDecodePointerChainAndInvalidPointers(t *testing.T) {
	chain := append(bytes.Repeat([]byte{0}, 12),
		1, 'a', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0,
		0xc0, 0x0c,
		0xc0, 0x17)
	got, consumed, err := Decode(chain, 25, 12)
	want := labels("a", "example")
	if err != nil || !sameLabels(got, want) || consumed != 2 {
		t.Fatalf("pointer chain: %q %d %v", got, consumed, err)
	}
	t.Logf("input=% x output=%q consumed=%d basis=pointer to pointer", chain[25:], got, consumed)

	tests := []struct {
		name   string
		wire   []byte
		target error
	}{
		{"self pointer", []byte{0xc0, 0x0c}, ErrForwardPointer},
		{"forward pointer", []byte{0xc0, 0x0e, 0, 0}, ErrForwardPointer},
		{"pointer before base", []byte{0xc0, 0x0b}, ErrPointerBeforeBase},
		{"truncated label", []byte{3, 'a', 'b'}, ErrTruncated},
		{"truncated pointer", []byte{0xc0}, ErrTruncated},
		{"reserved 01", []byte{0x40, 0}, ErrReservedLabel},
		{"reserved 10", []byte{0x80, 0}, ErrReservedLabel},
	}
	for _, tc := range tests {
		message := append(bytes.Repeat([]byte{0}, 12), tc.wire...)
		_, _, err := Decode(message, 12, 12)
		t.Logf("input=% x output_error=%q basis=%s", tc.wire, err, tc.name)
		if !errors.Is(err, tc.target) {
			t.Fatalf("%s = %v, want %v", tc.name, err, tc.target)
		}
	}
}

func TestNameLengthBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		labels [][]byte
		want   error
	}{
		{"wire length 255", repeatedLabels(62, 62, 63, 63), nil},
		{"wire length 256", repeatedLabels(63, 63, 63, 62), ErrNameTooLong},
		{"label 64", [][]byte{bytes.Repeat([]byte{'a'}, 64)}, ErrLabelTooLong},
	}
	for _, tc := range tests {
		b := NewBuilder()
		err := b.WriteName(tc.labels)
		t.Logf("lengths=%v output_len=%d error=%q basis=%s", labelLengths(tc.labels), len(b.Bytes()), err, tc.name)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.want == nil {
			decoded, _, err := Decode(b.Bytes(), 12, 12)
			if err != nil || !sameLabels(decoded, tc.labels) {
				t.Fatalf("%s decode: %q %v", tc.name, decoded, err)
			}
		}
	}
}

func TestNaiveEncodingDecodesEquivalently(t *testing.T) {
	sequence := [][][]byte{
		labels("www", "example", "org"),
		labels("api", "Example", "ORG"),
		labels("example", "org"),
		labels("org"),
		nil,
		labels("a", "b", "c"),
	}
	compressed := NewBuilder()
	naive, err := NewBuilderAt(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range sequence {
		if err := compressed.WriteName(input); err != nil {
			t.Fatal(err)
		}
		writeNaive(t, naive, input)
	}

	compressedOffset, naiveOffset, baseOffset := 12, 0, 0
	for _, input := range sequence {
		fromCompressed, cConsumed, err := Decode(compressed.Bytes(), compressedOffset, 12)
		if err != nil {
			t.Fatal(err)
		}
		fromNaive, nConsumed, err := Decode(naive.Bytes(), naiveOffset, baseOffset)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("input=%q compressed=% x decoded=%q consumed=%d; naive=% x decoded=%q consumed=%d; basis=same labels",
			input, compressed.Bytes()[compressedOffset:compressedOffset+cConsumed], fromCompressed, cConsumed,
			naive.Bytes()[naiveOffset:naiveOffset+nConsumed], fromNaive, nConsumed)
		if !sameASCIINames(fromCompressed, input) || !sameASCIINames(fromNaive, input) {
			t.Fatalf("decoded mismatch for %q", input)
		}
		compressedOffset += cConsumed
		naiveOffset += nConsumed
	}
}

func TestReplayRejectionAndConcurrency(t *testing.T) {
	sequence := [][][]byte{labels("alpha", "example"), labels("beta", "example"), labels("example"), nil}
	first := NewBuilder()
	second := NewBuilder()
	for _, input := range sequence {
		if err := first.WriteName(input); err != nil {
			t.Fatal(err)
		}
		if err := second.WriteName(input); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("replay differs")
	}
	t.Logf("input=%v output=% x basis=identical replay", sequence, first.Bytes())

	before := first.Bytes()
	err := first.WriteName([][]byte{bytes.Repeat([]byte{'a'}, 64)})
	if !errors.Is(err, ErrLabelTooLong) || !bytes.Equal(first.Bytes(), before) {
		t.Fatalf("rejected write was not atomic: %v", err)
	}
	t.Logf("input=label-length-64 output=% x basis=rejection appends and registers nothing", first.Bytes())

	if err := first.WriteName([][]byte{[]byte("ok"), {}}); !errors.Is(err, ErrEmptyLabel) {
		t.Fatalf("empty label error = %v", err)
	}
	if !bytes.Equal(first.Bytes(), before) {
		t.Fatalf("empty-label rejection changed message")
	}

	full, err := NewBuilderAt(65535)
	if err != nil {
		t.Fatal(err)
	}
	if err := full.WriteName(nil); !errors.Is(err, ErrMessageTooLong) || len(full.Bytes()) != 65535 {
		t.Fatalf("message boundary error = %v, len = %d", err, len(full.Bytes()))
	}

	concurrent := NewBuilder()
	var wait sync.WaitGroup
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := concurrent.WriteName(nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if len(concurrent.Bytes()) != 76 {
		t.Fatalf("concurrent length = %d", len(concurrent.Bytes()))
	}
	for offset := 12; offset < 76; offset++ {
		decoded, consumed, err := Decode(concurrent.Bytes(), offset, 12)
		if err != nil || len(decoded) != 0 || consumed != 1 {
			t.Fatalf("concurrent decode at %d: %q/%d/%v", offset, decoded, consumed, err)
		}
	}
	t.Logf("input=64 concurrent roots output_len=%d basis=safe concurrent writes and independent decoders", len(concurrent.Bytes()))
}

func writeNaive(t *testing.T, b *Builder, input [][]byte) {
	t.Helper()
	start := len(b.Bytes())
	for _, label := range input {
		b.message = append(b.message, byte(len(label)))
		b.message = append(b.message, label...)
	}
	b.message = append(b.message, 0)
	if len(b.Bytes())-start > 255 {
		t.Fatal("naive name too long")
	}
}

func labels(values ...string) [][]byte {
	result := make([][]byte, len(values))
	for index, value := range values {
		result[index] = []byte(value)
	}
	return result
}

func repeatedLabels(lengths ...int) [][]byte {
	result := make([][]byte, len(lengths))
	for index, length := range lengths {
		result[index] = bytes.Repeat([]byte{byte('a' + index)}, length)
	}
	return result
}

func sameLabels(got [][]byte, want [][]byte) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if !bytes.Equal(got[index], want[index]) {
			return false
		}
	}
	return true
}

func sameASCIINames(got [][]byte, want [][]byte) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if len(got[index]) != len(want[index]) {
			return false
		}
		for position := range got[index] {
			left := got[index][position]
			right := want[index][position]
			if left >= 'A' && left <= 'Z' {
				left += 'a' - 'A'
			}
			if right >= 'A' && right <= 'Z' {
				right += 'a' - 'A'
			}
			if left != right {
				return false
			}
		}
	}
	return true
}

func labelLengths(input [][]byte) []int {
	result := make([]int, len(input))
	for index, label := range input {
		result[index] = len(label)
	}
	return result
}
