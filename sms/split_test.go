package sms

import (
	"errors"
	"strings"
	"testing"
)

func repeatRune(r rune, count int) string {
	return strings.Repeat(string(r), count)
}

func TestSplitGSMThresholdsAndBoundaries(t *testing.T) {
	meter, err := New(100, 3, 10, 6, 200)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		text      string
		encoding  string
		intervals [][2]int
	}{
		{name: "160", text: repeatRune('a', 160), encoding: "GSM", intervals: [][2]int{{0, 160}}},
		{name: "161", text: repeatRune('a', 161), encoding: "GSM", intervals: [][2]int{{0, 153}, {153, 161}}},
		{name: "x moves to next", text: repeatRune('a', 152) + "{" + repeatRune('b', 10), encoding: "GSM", intervals: [][2]int{{0, 152}, {152, 163}}},
		{name: "repeated x shifts", text: repeatRune('a', 152) + "{" + repeatRune('a', 151) + "{", encoding: "GSM", intervals: [][2]int{{0, 152}, {152, 304}, {304, 305}}},
		{name: "306", text: repeatRune('a', 306), encoding: "GSM", intervals: [][2]int{{0, 153}, {153, 306}}},
		{name: "307", text: repeatRune('a', 307), encoding: "GSM", intervals: [][2]int{{0, 153}, {153, 306}, {306, 307}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := meter.Split(tt.text)
			if err != nil {
				t.Fatalf("Split() error = %v", err)
			}
			if result.Encoding != tt.encoding {
				t.Fatalf("Encoding = %q, want %q", result.Encoding, tt.encoding)
			}
			if !sameIntervals(result.Intervals, tt.intervals) {
				t.Fatalf("Intervals = %v, want %v", result.Intervals, tt.intervals)
			}
		})
	}
}

func TestSplitUCS2AndDoubleWidth(t *testing.T) {
	meter, err := New(100, 3, 10, 6, 200)
	if err != nil {
		t.Fatal(err)
	}

	text := repeatRune('汉', 66) + "😀" + repeatRune('汉', 5)
	result, err := meter.Split(text)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{0, 66}, {66, 72}}
	if result.Encoding != "UCS2" || !sameIntervals(result.Intervals, want) {
		t.Fatalf("result = %+v, want UCS2 %v", result, want)
	}

	ucsText := repeatRune('汉', 66) + "{" + repeatRune('汉', 3)
	result, err = meter.Split(ucsText)
	if err != nil {
		t.Fatal(err)
	}
	if result.Encoding != "UCS2" || !sameIntervals(result.Intervals, [][2]int{{0, 70}}) {
		t.Fatalf("X in UCS2 must cost one unit, got %+v", result)
	}
}

func TestSplitTooManySegmentsAndInvalidText(t *testing.T) {
	meter, err := New(100, 3, 10, 6, 200)
	if err != nil {
		t.Fatal(err)
	}

	ten, err := meter.Split(repeatRune('a', 10*gsmPartUnits))
	if err != nil || len(ten.Intervals) != 10 {
		t.Fatalf("ten segments result = %+v, error = %v", ten, err)
	}
	_, err = meter.Split(repeatRune('a', 10*gsmPartUnits+1))
	if !errors.Is(err, ErrTooManySegments) {
		t.Fatalf("11 segments error = %v, want ErrTooManySegments", err)
	}
	_, err = meter.Split("")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty error = %v", err)
	}
	_, err = meter.Split("a\xff")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid utf8 error = %v", err)
	}
}

func sameIntervals(got, want [][2]int) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
