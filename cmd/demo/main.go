package main

import (
	"errors"
	"fmt"
	"sort"

	"ontology/cursor"
	"ontology/row"
)

type check struct {
	name string
	run  func() bool
}

var checks []check

func add(name string, run func() bool) { checks = append(checks, check{name, run}) }

// sameScoreDataset 构造十行：六行 score 完全相同，其余四行分布两侧。
func sameScoreDataset() []row.Row {
	raw := []row.Row{
		{Score: 1, ID: "d"}, {Score: 2, ID: "g2"}, {Score: 1, ID: "b"}, {Score: 1, ID: "a"}, {Score: 0, ID: "z0"},
		{Score: -1, ID: "y-"}, {Score: 1, ID: "c"}, {Score: 3, ID: "h3"}, {Score: 1, ID: "e"}, {Score: 1, ID: "f"},
	}
	sort.Slice(raw, func(i, j int) bool { return row.Less(raw[i], raw[j]) })
	return raw
}

func init() {
	add("十行(六同值)经复合键全序排序后唯一且顺序确定", func() bool {
		rows := sameScoreDataset()
		if len(rows) != 10 {
			return false
		}
		seen := map[string]bool{}
		for i, r := range rows {
			if seen[r.ID] {
				return false
			}
			seen[r.ID] = true
			if i > 0 && !row.Less(rows[i-1], r) {
				return false
			}
		}
		return true
	})
	add("游标逐比特翻转全部被拒且归类正确", func() bool {
		base, err := cursor.Encode(cursor.Cursor{
			Dir:   cursor.Forward,
			Last:  row.Row{Score: 2.5, ID: "node-9"},
			First: row.Row{Score: 2.5, ID: "node-1"},
		})
		if err != nil {
			return false
		}
		classified := map[error]int{}
		for i := 0; i < len(base); i++ {
			for bit := uint(0); bit < 8; bit++ {
				v := append([]byte(nil), base...)
				v[i] ^= 1 << bit
				if _, err := cursor.Decode(v); err == nil {
					return false
				} else if errors.Is(err, cursor.ErrChecksum) {
					classified[cursor.ErrChecksum]++
				} else if errors.Is(err, cursor.ErrIncomplete) {
					classified[cursor.ErrIncomplete]++
				} else if errors.Is(err, cursor.ErrDirection) {
					classified[cursor.ErrDirection]++
				} else {
					return false
				}
			}
		}
		total := len(base) * 8
		sum := 0
		for _, n := range classified {
			sum += n
		}
		return sum == total && classified[cursor.ErrDirection] > 0 && classified[cursor.ErrChecksum] > 0
	})
	add("正向游标反向翻页被拒(ErrMismatch)", func() bool {
		b, err := cursor.Encode(cursor.Cursor{
			Dir: cursor.Forward, Last: row.Row{Score: 1, ID: "a"}, First: row.Row{Score: 0, ID: "b"},
		})
		if err != nil {
			return false
		}
		c, err := cursor.Decode(b)
		if err != nil {
			return false
		}
		return errors.Is(cursor.EnsureDir(c, cursor.Backward), cursor.ErrMismatch)
	})
}

func main() {
	pass := 0
	for _, c := range checks {
		if c.run() {
			pass++
			fmt.Printf("OK %s\n", c.name)
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		fmt.Println("FAIL")
	}
}
