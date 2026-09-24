// Package audit 提供翻页结果「不重不漏」的自检。
package audit

import (
	"errors"
	"fmt"

	"ontology/row"
)

// 三类可用 errors.Is 区分的自检失败。
var (
	ErrDuplicate = errors.New("audit: row returned more than once")
	ErrMissing   = errors.New("audit: row missing from pages")
	ErrOrder     = errors.New("audit: page order differs from full sort")
)

// ExactlyOnce 校验 pages 拼接后与 want（一次性全量升序结果）逐行一致：
// 每行恰好出现一次且顺序相同。
func ExactlyOnce(pages [][]row.Row, want []row.Row) error {
	var got []row.Row
	for _, p := range pages {
		got = append(got, p...)
	}
	if err := NoRepeats(pages); err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("%w: got %d rows, want %d", ErrMissing, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			if got[i].ID == want[i].ID {
				return fmt.Errorf("%w: row %s key differs at %d", ErrOrder, got[i].ID, i)
			}
			return fmt.Errorf("%w: at %d got %s, want %s", ErrOrder, i, got[i].ID, want[i].ID)
		}
	}
	return nil
}

// NoRepeats 校验 pages 中没有任何行（按 ID）出现超过一次。
func NoRepeats(pages [][]row.Row) error {
	seen := map[string]bool{}
	for _, p := range pages {
		for _, r := range p {
			if seen[r.ID] {
				return fmt.Errorf("%w: %s", ErrDuplicate, r.ID)
			}
			seen[r.ID] = true
		}
	}
	return nil
}
