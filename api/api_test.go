package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/dh"
)

func naiveRef(base, e, p uint64) uint64 {
	r := uint64(1) % p
	for ; e > 0; e-- {
		r = r * base % p
	}
	return r
}

var groups = []struct{ p, g uint64 }{{29, 2}, {29, 3}, {101, 7}, {7919, 5}, {104729, 12}}

// 循环生成确定性的"随机"私钥对
func privPairs(p uint64, n int) [][2]uint64 {
	rng := rand.New(rand.NewSource(int64(p)*1000 + int64(n)))
	out := make([][2]uint64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, [2]uint64{1 + rng.Uint64()%(p-2), 1 + rng.Uint64()%(p-2)})
	}
	return out
}

func validPub(pub, p uint64) bool { return pub >= 2 && pub <= p-2 }
func TestExchangeConsistency(t *testing.T) {
	for _, gp := range groups {
		x, _ := New(gp.p, gp.g)
		for _, ab := range privPairs(gp.p, 50) {
			pubA, _ := x.PublicKey(ab[0])
			pubB, _ := x.PublicKey(ab[1])
			// 合法私钥可能产出 1 或 p-1（如 a=(p-1)/2），按规则必须拒收
			if !validPub(pubA, gp.p) || !validPub(pubB, gp.p) {
				if _, e := x.Secret(ab[0], pubB); !validPub(pubB, gp.p) && !errors.Is(e, dh.ErrBadPub) {
					t.Fatalf("p=%d: invalid pubB=%d not rejected: %v", gp.p, pubB, e)
				}
				continue
			}
			sAB, err1 := x.Secret(ab[0], pubB)
			sBA, err2 := x.Secret(ab[1], pubA)
			if err1 != nil || err2 != nil || sAB != sBA {
				t.Fatalf("p=%d a=%d b=%d: %d vs %d (err %v/%v)", gp.p, ab[0], ab[1], sAB, sBA, err1, err2)
			}
		}
	}
}

func TestNaiveConsistency(t *testing.T) {
	for _, gp := range groups {
		x, _ := New(gp.p, gp.g)
		var pubB uint64
		for b := uint64(1); ; b++ { // 找一个公钥合法的对端
			if pub, _ := x.PublicKey(b); validPub(pub, gp.p) {
				pubB = pub
				break
			}
		}
		for _, ab := range privPairs(gp.p, 20) {
			pub, err := x.PublicKey(ab[0])
			if err != nil || pub != naiveRef(gp.g, ab[0], gp.p) {
				t.Fatalf("p=%d a=%d: pub=%d naive=%d err=%v", gp.p, ab[0], pub, naiveRef(gp.g, ab[0], gp.p), err)
			}
			if s, err := x.Secret(ab[0], pubB); err != nil || s != naiveRef(pubB, ab[0], gp.p) {
				t.Fatalf("p=%d a=%d: secret=%d naive=%d err=%v", gp.p, ab[0], s, naiveRef(pubB, ab[0], gp.p), err)
			}
		}
	}
}

func TestRejections(t *testing.T) {
	x, _ := New(29, 2)
	pubB, _ := x.PublicKey(11)
	beforePub, _ := x.PublicKey(5)
	// 三类故障注入（表驱动）：错误可判定且互不相同
	_, eG1 := New(1, 2)
	_, eG2 := New(29, 1)
	_, eG3 := New(29, 29)
	_, eP1 := x.PublicKey(0)
	_, eP2 := x.PublicKey(28)
	_, eU1 := x.Secret(5, 1)
	_, eU2 := x.Secret(5, 28)
	cases := []struct {
		name      string
		got, want error
	}{
		{"p<2", eG1, dh.ErrBadGroup},
		{"g<2", eG2, dh.ErrBadGroup},
		{"g>p-1", eG3, dh.ErrBadGroup},
		{"priv=0", eP1, dh.ErrBadPriv},
		{"priv=p-1", eP2, dh.ErrBadPriv},
		{"peerPub=1", eU1, dh.ErrBadPub},
		{"peerPub=p-1", eU2, dh.ErrBadPub},
	}
	for _, c := range cases {
		if !errors.Is(c.got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
	if dh.ErrBadGroup == dh.ErrBadPriv || dh.ErrBadPriv == dh.ErrBadPub || dh.ErrBadGroup == dh.ErrBadPub {
		t.Error("sentinel errors must be distinct")
	}
	// 失败不留痕：被拒后状态不变，仍可正常使用
	afterPub, err1 := x.PublicKey(5)
	afterSec, err2 := x.Secret(5, pubB)
	if err1 != nil || err2 != nil || afterPub != beforePub || afterSec != naiveRef(pubB, 5, 29) {
		t.Errorf("state changed after rejection: pub %d->%d, sec=%d", beforePub, afterPub, afterSec)
	}
}

func TestConcurrentSecret(t *testing.T) {
	x, _ := New(104729, 12)
	pubB, _ := x.PublicKey(11)
	want := naiveRef(pubB, 12345, 104729)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ { // 并发 Secret：结果与朴素参照逐值相同则彼此一致
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, err := x.Secret(12345, pubB); err != nil || s != want {
				t.Errorf("secret=%d err=%v, want %d", s, err, want)
			}
		}()
	}
	for i := 0; i < 8; i++ { // SelfCheck 并发调用，-race 必须干净
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := x.SelfCheck(); err != nil {
				t.Errorf("SelfCheck: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestSelfCheck(t *testing.T) {
	for _, gp := range groups {
		x, _ := New(gp.p, gp.g)
		if err := x.SelfCheck(); err != nil {
			t.Errorf("p=%d g=%d: %v", gp.p, gp.g, err)
		}
	}
}
