package runs

import "math/big"

type Run struct {
	Symbol rune
	Count  *big.Int
}

func Split(s string) []Run { return nil }

func AppendCount(buf []byte, count *big.Int) []byte { return buf }

func AppendSymbol(buf []byte, symbol rune) []byte { return buf }

func AppendRun(buf []byte, r Run) []byte { return buf }

func CountBytes(count *big.Int, runeSize int, limit int64) *big.Int { return nil }
