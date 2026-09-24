package runs

import (
	"errors"
	"strconv"
	"unicode/utf8"
)

var ErrCountTooLarge = errors.New("runs: repeat count exceeds uint64")

type Run struct {
	Symbol rune
	Count  uint64
}

func Split(s string) []Run {
	if s == "" {
		return nil
	}
	var result []Run
	current := rune(0)
	var count uint64
	for _, symbol := range s {
		if count > 0 && symbol == current {
			count++
			continue
		}
		if count > 0 {
			result = append(result, Run{current, count})
		}
		current = symbol
		count = 1
	}
	if count > 0 {
		result = append(result, Run{current, count})
	}
	return result
}

func AppendCount(buf []byte, count uint64) []byte {
	if count < 2 {
		return buf
	}
	return strconv.AppendUint(buf, count, 10)
}

func AddDigit(count uint64, digit byte) (uint64, error) {
	value := uint64(digit - '0')
	const max = ^uint64(0)
	if count > (max-value)/10 {
		return 0, ErrCountTooLarge
	}
	return count*10 + value, nil
}

func RuneLen(symbol rune) int {
	return utf8.RuneLen(symbol)
}
