package main

import (
	"errors"
	"fmt"

	"ontology/cursor"
	"ontology/row"
)

func main() {
	pass, total := 0, 0
	report := func(name string, ok bool) {
		total++
		if ok {
			pass++
		}
		status := "OK"
		if !ok {
			status = "FAIL"
		}
		fmt.Printf("%s %s\n", status, name)
	}

	report("游标逐比特翻转全部被拒且分类正确", checkBitFlip())
	report("跨方向复用游标被拒", checkCrossDirection())

	fmt.Printf("总计 %d/%d 通过\n", pass, total)
	if pass != total {
		panic("demo failed")
	}
}

func checkBitFlip() bool {
	raw := cursor.Encode(row.Key{V: 2.5, ID: "row-1"}, cursor.Forward)
	accepted, classified := 0, 0
	for bit := 0; bit < len(raw)*8; bit++ {
		mut := append([]byte(nil), raw...)
		mut[bit/8] ^= 1 << uint(7-bit%8)
		_, _, err := cursor.Decode(mut, cursor.Forward)
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, cursor.ErrChecksum), errors.Is(err, cursor.ErrIncomplete),
			errors.Is(err, cursor.ErrDirection):
			classified++
		default:
			return false
		}
	}
	return accepted == 0 && classified == len(raw)*8
}

func checkCrossDirection() bool {
	raw := cursor.Encode(row.Key{V: 1, ID: "a"}, cursor.Forward)
	_, _, err := cursor.Decode(raw, cursor.Backward)
	return errors.Is(err, cursor.ErrWrongDirection)
}
