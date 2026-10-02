// Package bencode implements a streaming, incremental decoder for the
// bencode format. It turns arbitrarily chunked byte streams into value
// trees and rejects non-canonical input at the exact offset of the first
// offending byte, independently of how the input was split into feeds.
package bencode

import "strconv"

// Kind identifies the concrete type held by a Value.
type Kind int

const (
	KindInt Kind = iota
	KindString
	KindList
	KindDict
)

// DictEntry is a single key/value pair of a dictionary. Keys are kept in
// their original (strictly ascending) order.
type DictEntry struct {
	Key string
	Val *Value
}

// Value is a node of the decoded bencode value tree.
type Value struct {
	Kind Kind
	Int  int64       // KindInt
	Str  string      // KindString, raw bytes
	List []*Value    // KindList
	Dict []DictEntry // KindDict
}

// Int builds an integer value.
func Int(v int64) *Value { return &Value{Kind: KindInt, Int: v} }

// Str builds a byte-string value.
func Str(s string) *Value { return &Value{Kind: KindString, Str: s} }

// String renders the value in its canonical bencode form, which is handy
// for logging and test output.
func (v *Value) String() string {
	switch v.Kind {
	case KindInt:
		return "i" + strconv.FormatInt(v.Int, 10) + "e"
	case KindString:
		return strconv.Itoa(len(v.Str)) + ":" + v.Str
	case KindList:
		out := "l"
		for _, item := range v.List {
			out += item.String()
		}
		return out + "e"
	case KindDict:
		out := "d"
		for _, entry := range v.Dict {
			out += strconv.Itoa(len(entry.Key)) + ":" + entry.Key + entry.Val.String()
		}
		return out + "e"
	default:
		return "<invalid>"
	}
}
