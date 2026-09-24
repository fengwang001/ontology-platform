package runs

import (
	"errors"
	"math"
	"strconv"
)

type Run struct {
	Symbol rune
	Count  int
}

var ErrCountTooLarge = errors.New("runs: count is larger than math.MaxInt")

type CountReader struct{}

func (CountReader) Digit(c byte) bool { return c >= '0' && c <= '9' }

func (CountReader) AddDigit(n int, c byte) (int, error) {
	d := int(c - '0')
	if n > (math.MaxInt-d)/10 {
		return 0, ErrCountTooLarge
	}
	return n*10 + d, nil
}

func AppendCount(b []byte, n int) []byte { return strconv.AppendInt(b, int64(n), 10) }

func Split(s string) []Run {
	var out []Run
	for _, symbol := range s {
		if len(out) > 0 && out[len(out)-1].Symbol == symbol {
			out[len(out)-1].Count++
			continue
		}
		out = append(out, Run{Symbol: symbol, Count: 1})
	}
	return out
}
