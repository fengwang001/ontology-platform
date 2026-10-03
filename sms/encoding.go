package sms

import (
	"unicode/utf8"
)

const (
	gsmSingleLimit = 160
	gsmPartUnits   = 153
	ucsSingleLimit = 70
	ucsPartUnits   = 67
)

const euroSign = '\u20AC'

func isBasicRune(r rune) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	switch r {
	case ' ', '\n', '.', ',', '!', '?', ':', ';', '-', '_', '@', '#', '%', '&', '*', '(', ')', '\'', '"', '+', '=', '/', '<', '>', '$':
		return true
	default:
		return false
	}
}

func isExtendedRune(r rune) bool {
	switch r {
	case '{', '}', '[', ']', '~', '^', '|', '\\', euroSign:
		return true
	default:
		return false
	}
}

func runeUnits(r rune, encoding string) int {
	if encoding == "UCS2" {
		if r > 0xFFFF {
			return 2
		}
		return 1
	}
	if isExtendedRune(r) {
		return 2
	}
	return 1
}

func analyzeText(text string) (string, int, error) {
	if text == "" {
		return "", 0, ErrInvalidArgument
	}
	if !utf8.ValidString(text) {
		return "", 0, ErrInvalidArgument
	}

	gsm := true
	for _, r := range text {
		if !isBasicRune(r) && !isExtendedRune(r) {
			gsm = false
		}
	}

	encoding := "UCS2"
	if gsm {
		encoding = "GSM"
	}

	total := 0
	for _, r := range text {
		total += runeUnits(r, encoding)
	}
	return encoding, total, nil
}

func splitText(text string) (SplitResult, error) {
	encoding, totalUnits, err := analyzeText(text)
	if err != nil {
		return SplitResult{}, err
	}
	result := packText(text, encoding, totalUnits)
	if len(result.Intervals) > 10 {
		return SplitResult{}, ErrTooManySegments
	}
	return result, nil
}

func packText(text, encoding string, totalUnits int) SplitResult {
	singleLimit, partUnits := gsmSingleLimit, gsmPartUnits
	if encoding == "UCS2" {
		singleLimit, partUnits = ucsSingleLimit, ucsPartUnits
	}

	intervals := make([][2]int, 0, totalUnits/partUnits+1)
	if totalUnits <= singleLimit {
		runeCount := utf8.RuneCountInString(text)
		intervals = append(intervals, [2]int{0, runeCount})
		return SplitResult{Encoding: encoding, Intervals: intervals}
	}

	segmentStart := 0
	segmentUnits := 0
	runeIndex := 0

	finishPart := func(end int) {
		intervals = append(intervals, [2]int{segmentStart, end})
		segmentStart = end
		segmentUnits = 0
	}

	for _, r := range text {
		units := runeUnits(r, encoding)
		if segmentUnits+units > partUnits {
			finishPart(runeIndex)
		}
		segmentUnits += units
		runeIndex++
	}
	if segmentStart < runeIndex {
		finishPart(runeIndex)
	}

	return SplitResult{Encoding: encoding, Intervals: intervals}
}
