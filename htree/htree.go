// Package htree builds Huffman code lengths from symbol frequencies.
package htree

import (
	"errors"
	"sort"
)

// ErrInvalidFreq reports an empty, all-zero, or negative frequency table.
var ErrInvalidFreq = errors.New("htree: invalid frequency table")

// node is a subtree: total frequency, its name (lexicographically smallest
// symbol inside, used for deterministic tie-breaking) and member symbols.
type node struct {
	freq int
	name byte
	syms []byte
}

// Lengths returns each symbol's Huffman code length (tree depth).
// Each round merges the two smallest nodes ordered by (freq, name).
// Zero-frequency symbols are excluded; a single-symbol alphabet gets
// length 1 so its canonical code is the single bit 0.
func Lengths(freq map[byte]int) (map[byte]int, error) {
	total := 0
	for _, f := range freq {
		if f < 0 {
			return nil, ErrInvalidFreq
		}
		total += f
	}
	if len(freq) == 0 || total == 0 {
		return nil, ErrInvalidFreq
	}
	var nodes []node
	for s, f := range freq {
		if f > 0 {
			nodes = append(nodes, node{f, s, []byte{s}})
		}
	}
	depth := map[byte]int{}
	for len(nodes) > 1 {
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].freq != nodes[j].freq {
				return nodes[i].freq < nodes[j].freq
			}
			return nodes[i].name < nodes[j].name
		})
		a, b := nodes[0], nodes[1]
		nodes = nodes[2:]
		for _, s := range a.syms {
			depth[s]++
		}
		for _, s := range b.syms {
			depth[s]++
		}
		name := a.name
		if b.name < name {
			name = b.name
		}
		nodes = append(nodes, node{a.freq + b.freq, name, append(a.syms, b.syms...)})
	}
	if len(nodes) == 1 && len(nodes[0].syms) == 1 {
		depth[nodes[0].syms[0]] = 1
	}
	return depth, nil
}
