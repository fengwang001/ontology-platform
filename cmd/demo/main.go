package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/field"
	"ontology/poly"
)

var fails int

func check(ok bool, format string, args ...any) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Printf(format+" "+tag+"\n", args...)
}

// naiveInv 用朴素归约器暴力找逆元（仅供演示对照）。
func naiveInv(r *poly.Reducer, a uint8) uint8 {
	for x := 1; x < 256; x++ {
		if r.Mul(a, uint8(x)) == 1 {
			return uint8(x)
		}
	}
	return 0
}

func main() {
	aes, err := poly.NewReducer(0x1B)
	check(err == nil, "poly: NewReducer(0x1B)")
	if _, err := poly.NewReducer(0x00); err != poly.ErrNotIrreducible {
		check(false, "poly: NewReducer(0x00) should fail")
	}

	// 第三节八行表（朴素移位+归约计算，Inv 暴力验证）
	var ops [8]uint8
	ops[0] = aes.Mul(0x57, 0x83)
	ops[1] = aes.Mul(0x02, 0x80)
	ops[2] = aes.Mul(0x53, 0x02)
	ops[3] = aes.Mul(0x0E, 0x0E)
	ops[4] = aes.Mul(0x9A, 0x9A)
	ops[5] = naiveInv(aes, 0x53)
	ops[6] = naiveInv(aes, 0x02)
	ops[7] = naiveInv(aes, 0x03)
	want := [8]uint8{0xC1, 0x1B, 0xA6, 0x54, 0xC5, 0xCA, 0x8D, 0xF6}
	check(ops == want, "ops(0x11B): %02X %02X %02X %02X %02X %02X %02X %02X",
		ops[0], ops[1], ops[2], ops[3], ops[4], ops[5], ops[6], ops[7])

	// (甲) 只左移不归约 vs 归约（uint8 移位自然截断，模拟错误实现）
	sh := uint8(0x80)
	trunc := sh << 1
	check(trunc == 0x00 && ops[1] == 0x1B, "(甲) 02*80: trunc=%02X reduced=%02X", trunc, ops[1])

	// (乙) 误用 0x11D（归约字节 0x1D）
	wrong, _ := poly.NewReducer(0x1D)
	wInv, wMul := naiveInv(wrong, 0x53), wrong.Mul(0x57, 0x83)
	check(wInv == 0x8C && wMul == 0x31, "(乙) 0x1D: Inv(53)=%02X Mul(57,83)=%02X (正确 CA/C1)", wInv, wMul)

	// (丙) log/exp 表对零元素的特判
	z := field.Mul(0x00, 0x53)
	check(z == 0x00, "(丙) zero-guard: Mul(0,53)=%02X (log[0] 不特判会错成 53)", z)

	// 费马小定理（群阶 255：a^255==1，故 a^256==a；Inv==Pow(a,254)）
	fermat := true
	for a := 1; a < 256; a++ {
		p255, _ := field.Pow(uint8(a), 255)
		p256, _ := field.Pow(uint8(a), 256)
		iv, _ := field.Inv(uint8(a))
		p254, _ := field.Pow(uint8(a), 254)
		if p255 != 1 || p256 != uint8(a) || iv != p254 {
			fermat = false
		}
	}
	check(fermat, "fermat(群阶255): Pow(a,255)==1, Pow(a,256)==a, Inv==Pow(a,254)")

	// 三类可判定错误互不相同
	_, e1 := field.Inv(0x00)
	_, e2 := field.Pow(0x02, 1<<63+1)
	e3 := poly.ErrNotIrreducible
	check(e1 == field.ErrZeroInverse && e2 == field.ErrExponentRange &&
		e1 != e2 && e1 != e3 && e2 != e3, "errors: 3 sentinels distinct")

	// Pow 乘法次数上界 2*ceil(log2 e)+1（实际计数断言在 field 包测试内，此处对照值正确性）
	powOK, bounds := true, [3]int{15, 21, 29}
	for i, e := range []uint64{100, 1000, 10000} {
		got, _ := field.Pow(0x53, e)
		ref := uint8(1)
		for j := uint64(0); j < e; j++ {
			ref = field.Mul(ref, 0x53)
		}
		ceil := 0
		for n := uint64(1); n < e; n <<= 1 {
			ceil++
		}
		powOK = powOK && got == ref && bounds[i] == 2*ceil+1
	}
	check(powOK, "pow: e=100/1000/10000 乘法上界=%d/%d/%d (对数增长)", bounds[0], bounds[1], bounds[2])

	// 并发求逆与串行一致
	var serial, parallel [256]uint8
	for a := 1; a < 256; a++ {
		serial[a], _ = field.Inv(uint8(a))
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for a := g; a < 256; a += 8 {
				if a > 0 {
					parallel[a], _ = field.Inv(uint8(a))
				}
			}
		}(g)
	}
	wg.Wait()
	check(serial == parallel, "concurrent Inv (8 goroutines) == serial")

	check(api.SelfCheck() == nil, "api.SelfCheck (四条不变量)")

	if fails > 0 {
		os.Exit(1)
	}
}
