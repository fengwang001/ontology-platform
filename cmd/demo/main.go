package main

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"sync"

	"ontology/api"
	"ontology/dh"
	"ontology/modarith"
)

var failed bool

func judge(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func naive(base, e, p uint64) uint64 {
	r := uint64(1) % p
	for ; e > 0; e-- {
		r = r * base % p
	}
	return r
}

func main() {
	// modarith：2^90 mod 29 的七个平方-乘步骤（高位到低位，90 = 0b1011010）
	p := uint64(29)
	r := uint64(1)
	steps := make([]uint64, 0, 7)
	for i := 6; i >= 0; i-- {
		r = modarith.Mul(r, r, p)
		if (90>>i)&1 == 1 {
			r = modarith.Mul(r, 2, p)
		}
		steps = append(steps, r)
	}
	want := []uint64{2, 4, 3, 18, 5, 21, 6}
	ok := r == modarith.Modpow(2, big.NewInt(90), p)
	for i := range want {
		ok = ok && steps[i] == want[i]
	}
	judge("square-multiply steps", ok, fmt.Sprintf("r=%v final=%d", steps, r))

	// api：交换一致性 a=5 / b=11 → A=3, B=18, S=15
	x, err := api.New(29, 2)
	A, errA := x.PublicKey(5)
	B, errB := x.PublicKey(11)
	sAB, err1 := x.Secret(5, B)
	sBA, err2 := x.Secret(11, A)
	ok = err == nil && errA == nil && errB == nil && err1 == nil && err2 == nil &&
		A == 3 && B == 18 && sAB == 15 && sBA == 15
	judge("exchange consistency", ok, fmt.Sprintf("A=%d B=%d S=%d/%d", A, B, sAB, sBA))

	// api：与朴素参照逐值一致
	ok = true
	for a := uint64(1); a <= 27; a++ {
		pub, e := x.PublicKey(a)
		s, e2 := x.Secret(a, B)
		ok = ok && e == nil && e2 == nil && pub == naive(2, a, p) && s == naive(B, a, p)
	}
	judge("naive reference match", ok, "a=1..27")

	// api：三类可判定错误互不相同
	_, eGroup := api.New(1, 2)
	_, ePriv := x.PublicKey(0)
	_, ePub := x.Secret(1, 1)
	ok = errors.Is(eGroup, dh.ErrBadGroup) && errors.Is(ePriv, dh.ErrBadPriv) &&
		errors.Is(ePub, dh.ErrBadPub) &&
		eGroup != ePriv && ePriv != ePub && eGroup != ePub
	judge("distinct sentinel errors", ok, fmt.Sprintf("%v | %v | %v", eGroup, ePriv, ePub))

	// api：被拒后状态不变
	after, e := x.PublicKey(5)
	sAfter, e2 := x.Secret(5, B)
	judge("state unchanged after rejection", e == nil && e2 == nil && after == A && sAfter == sAB,
		fmt.Sprintf("A=%d S=%d", after, sAfter))

	// modarith：e = 2^10000-1 时快速幂仍可完成（朴素法需 e 次乘法，不可行）
	eHuge := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 10000), big.NewInt(1))
	eMod := new(big.Int).Mod(eHuge, big.NewInt(28)) // 费马：2^28 ≡ 1 (mod 29)
	ok = modarith.Modpow(2, eHuge, p) == modarith.Modpow(2, eMod, p)
	judge("large-k modpow feasible", ok, "e=2^10000-1 mod 29")

	// api：N 个 goroutine 并发算同一组 Secret，结果彼此一致且等于朴素参照
	const n = 32
	results := make([]uint64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := x.Secret(7, B)
			if err == nil {
				results[i] = s
			}
		}(i)
	}
	wg.Wait()
	ok = true
	for _, s := range results {
		ok = ok && s == naive(B, 7, p)
	}
	judge("concurrent secret consistent", ok, fmt.Sprintf("N=%d S=%d", n, results[0]))

	// api：内置参数自检四条不变量
	judge("SelfCheck", x.SelfCheck() == nil, "four invariants")

	if failed {
		os.Exit(1)
	}
}
