package ontology

import (
	"errors"
	"strings"
	"testing"
)

func TestCafeDistanceIsOneInCodePoints(t *testing.T) {
	// "café" with composed é (U+00E9, 2 bytes) vs "cafe": one rune
	// difference, not two bytes.
	res := mustDistance(t, "café", "cafe", 1)
	if res.Exceeded || res.Distance != 1 {
		t.Fatalf("café vs cafe: got %+v, want distance 1", res)
	}
	n, err := RuneLen("café")
	if err != nil || n != 4 {
		t.Fatalf("RuneLen(café) = %d, %v; want 4, nil", n, err)
	}
}

func TestEmojiDistanceIsOneInCodePoints(t *testing.T) {
	// U+1F600 is 4 bytes in UTF-8 but a single code point.
	res := mustDistance(t, "a\U0001F600b", "ab", 1)
	if res.Exceeded || res.Distance != 1 {
		t.Fatalf("emoji insertion: got %+v, want distance 1", res)
	}
	res = mustDistance(t, "\U0001F600\U0001F601", "\U0001F600", 1)
	if res.Exceeded || res.Distance != 1 {
		t.Fatalf("emoji deletion: got %+v, want distance 1", res)
	}
	n, err := RuneLen("\U0001F600x")
	if err != nil || n != 2 {
		t.Fatalf("RuneLen(emoji+x) = %d, %v; want 2, nil", n, err)
	}
}

func TestInvalidUTF8ReportsSideAndByteOffset(t *testing.T) {
	_, err := Distance("ok", "ab\xffcd", 3)
	var uerr *UTF8Error
	if !errors.As(err, &uerr) {
		t.Fatalf("got %v, want *UTF8Error", err)
	}
	if uerr.Side != SideRight || uerr.ByteOffset != 2 {
		t.Fatalf("got side %v offset %d, want right offset 2", uerr.Side, uerr.ByteOffset)
	}

	_, err = Distance("\xc3\x28", "ok", 3) // invalid 2-byte sequence at 0
	if !errors.As(err, &uerr) || uerr.Side != SideLeft || uerr.ByteOffset != 0 {
		t.Fatalf("got %v, want left side offset 0", err)
	}

	_, err = RuneLen("xy\xff")
	if !errors.As(err, &uerr) || uerr.ByteOffset != 2 {
		t.Fatalf("RuneLen: got %v, want offset 2", err)
	}
}

func TestFoldCaseOption(t *testing.T) {
	fold := Options{FoldCase: true}
	res, err := DistanceWithOptions("Hello", "hELLO", 0, fold)
	if err != nil || res.Exceeded || res.Distance != 0 {
		t.Fatalf("Hello vs hELLO folded: got %+v, %v; want distance 0", res, err)
	}
	// Without folding the same pair is far apart.
	res = mustDistance(t, "Hello", "hELLO", 0)
	if !res.Exceeded {
		t.Fatalf("Hello vs hELLO unfolded: got %+v, want exceeded at k=0", res)
	}
	// Simple folding equates ß with ẞ (its fold orbit)...
	res, err = DistanceWithOptions("straße", "STRAẞE", 0, fold)
	if err != nil || res.Exceeded {
		t.Fatalf("straße vs STRAẞE folded: got %+v, %v; want distance 0", res, err)
	}
	// ...but NOT ß with "ss": that needs full case folding, which is
	// explicitly out of scope (see package doc).
	res, err = DistanceWithOptions("Straße", "STRASSE", 0, fold)
	if err != nil || !res.Exceeded {
		t.Fatalf("Straße vs STRASSE folded: got %+v, %v; want exceeded (ß != ss)", res, err)
	}
	res, err = DistanceWithOptions("Straße", "STRASSE", 1, fold)
	if err != nil || !res.Exceeded {
		t.Fatalf("Straße vs STRASSE folded k=1: got %+v, %v; want exceeded", res, err)
	}
	// As rune sequences the gap is exactly 2: substitute ß->S, insert S.
	res, err = DistanceWithOptions("Straße", "STRASSE", 2, fold)
	if err != nil || res.Exceeded || res.Distance != 2 {
		t.Fatalf("Straße vs STRASSE folded k=2: got %+v, %v; want distance 2", res, err)
	}
}

func TestRuneLenHandlesLongInputs(t *testing.T) {
	s := strings.Repeat("é\U0001F600", 500)
	n, err := RuneLen(s)
	if err != nil || n != 1000 {
		t.Fatalf("RuneLen = %d, %v; want 1000, nil", n, err)
	}
}
