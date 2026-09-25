// Package hunk groups an edit script into unified-diff hunks with a
// configurable context radius and GNU-compatible merging (gap <= 2C).
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Item is one body line of a hunk: an Equal context, Delete or Insert.
type Item struct {
	Op edit.Op
	Line lines.Line
}

// Hunk is one contiguous @@ section. AO/AN and BO/BN are the GNU-style
// one-based header values (AO may be 0 when AN is 0, likewise BO/BN).
type Hunk struct {
	AO, AN int
	BO, BN int
	Body   []Item
}

// Group partitions the script into hunks using ctx context lines. Two
// change groups with g unchanged lines between them merge iff g <= 2*ctx.
func Group(script []edit.Item, ctx int) []Hunk {
	var hunks []Hunk
	i := 0
	for i < len(script) {
		if script[i].Op == edit.Equal {
			i++
			continue
		}
		start := i - ctx
		if start < 0 {
			start = 0
		}
		lastChange := i
		i++
		for i < len(script) {
			if script[i].Op != edit.Equal {
				if i-lastChange-1 <= 2*ctx {
					lastChange = i
					i++
					continue
				}
				break
			}
			i++
		}
		end := lastChange + 1 + ctx
		if end > len(script) {
			end = len(script)
		}
		body := make([]Item, 0, end-start)
		preA, preB := 0, 0
		for j := 0; j < start; j++ {
			if script[j].Op != edit.Insert {
				preA++
			}
			if script[j].Op != edit.Delete {
				preB++
			}
		}
		an, bn := 0, 0
		for j := start; j < end; j++ {
			it := script[j]
			body = append(body, Item{Op: it.Op, Line: it.Line})
			if it.Op != edit.Insert {
				an++
			}
			if it.Op != edit.Delete {
				bn++
			}
		}
		ao, bo := preA, preB
		if an > 0 {
			ao = preA + 1
		}
		if bn > 0 {
			bo = preB + 1
		}
		hunks = append(hunks, Hunk{AO: ao, AN: an, BO: bo, BN: bn, Body: body})
	}
	return hunks
}
