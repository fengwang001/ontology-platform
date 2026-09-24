// Package rle implements the escaped run-length encoding described in
// DESIGN.md: canonical Encode, strict Decode, and a streaming Decoder.
package rle

import (
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// DefaultMaxOut caps decoded output bytes when a non-positive limit is
// given to NewDecoder; Decode always uses it.
const DefaultMaxOut = 1 << 30

// "placeholder"
