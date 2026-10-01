package ontology

import (
	"strconv"
	"unicode/utf8"
)

const (
	startPadding rune = '\u0002'
	endPadding   rune = '\u0003'
)

type trigramSet map[string]int

func validWord(word string) bool {
	if word == "" || !utf8.ValidString(word) {
		return false
	}
	for _, r := range word {
		if r == startPadding || r == endPadding {
			return false
		}
	}
	return true
}

func trigrams(word string) trigramSet {
	runes := make([]rune, 0, utf8.RuneCountInString(word)+4)
	runes = append(runes, startPadding, startPadding)
	for _, r := range word {
		runes = append(runes, r)
	}
	runes = append(runes, endPadding, endPadding)

	result := make(trigramSet, len(runes)-2)
	for i := 0; i+2 < len(runes); i++ {
		result[string(runes[i:i+3])]++
	}
	return result
}

func trigramSize(word string) int {
	return utf8.RuneCountInString(word) + 2
}

func intersectionCount(a, b trigramSet) int {
	if len(a) > len(b) {
		a, b = b, a
	}

	count := 0
	for trigram, leftCount := range a {
		rightCount := b[trigram]
		if leftCount < rightCount {
			count += leftCount
		} else {
			count += rightCount
		}
	}
	return count
}

func properRunePrefix(a, b string) bool {
	ar, br := []rune(a), []rune(b)
	if len(ar) >= len(br) {
		return false
	}
	for i, r := range ar {
		if br[i] != r {
			return false
		}
	}
	return true
}

func reducedScore(numerator, denominator int) string {
	divisor := gcd(numerator, denominator)
	return strconv.Itoa(numerator/divisor) + "/" + strconv.Itoa(denominator/divisor)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
