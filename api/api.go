// Package api 是 DH 密钥交换的对外门面。依赖 dh。
package api

import (
	"errors"
	"fmt"

	"ontology/dh"
)

// Exchange 包装一个校验过的 DH 群，方法可并发调用。
type Exchange struct {
	gr *dh.Group
}

// New 建立群参数为 (p, g) 的交换实例；参数非法则整体失败。
func New(p, g uint64) (*Exchange, error) {
	gr, err := dh.New(p, g)
	if err != nil {
		return nil, err
	}
	return &Exchange{gr: gr}, nil
}

// PublicKey 计算公钥 g^priv mod p。
func (x *Exchange) PublicKey(priv uint64) (uint64, error) {
	return x.gr.PublicKey(priv)
}

// Secret 用私钥 priv 与对端公钥 peerPub 派生共享密钥。
func (x *Exchange) Secret(priv, peerPub uint64) (uint64, error) {
	return x.gr.Secret(priv, peerPub)
}

// naive 是朴素参照：循环 e 次逐个乘 base 再 mod p。
func naive(base, e, p uint64) uint64 {
	r := uint64(1) % p
	for ; e > 0; e-- {
		r = r * base % p
	}
	return r
}

// SelfCheck 对内置参数核验四条不变量，全部通过返回 nil。
func (x *Exchange) SelfCheck() error {
	groups := [][2]uint64{{29, 2}, {29, 3}, {101, 7}, {7919, 5}}
	privs := []uint64{1, 2, 5, 11, 23, 100}
	for _, gp := range groups {
		ex, err := New(gp[0], gp[1])
		if err != nil {
			return err
		}
		for _, a := range privs {
			if a > gp[0]-2 {
				continue
			}
			// 不变量 2/3：快速幂结果与朴素参照逐值相同
			pub, err := ex.PublicKey(a)
			if err != nil || pub != naive(gp[1], a, gp[0]) {
				return fmt.Errorf("selfcheck: public key mismatch: %w", err)
			}
			for _, b := range privs {
				if b > gp[0]-2 {
					continue
				}
				pubB, _ := ex.PublicKey(b)
				sAB, err1 := ex.Secret(a, pubB)
				sBA, err2 := ex.Secret(b, pub)
				// 不变量 1：交换一致，且等于朴素参照 g^(ab)
				if err1 != nil || err2 != nil || sAB != sBA {
					return errors.New("selfcheck: exchange inconsistency")
				}
				if sAB != naive(pubB, a, gp[0]) {
					return errors.New("selfcheck: secret mismatches naive reference")
				}
			}
		}
	}
	// 不变量 4：三类拒绝互不相同，被拒后状态不变、仍可正常使用
	before, err := x.PublicKey(5)
	if err != nil {
		return err
	}
	if _, e1 := New(1, 2); !errors.Is(e1, dh.ErrBadGroup) {
		return errors.New("selfcheck: bad group not rejected")
	}
	if _, e2 := x.PublicKey(0); !errors.Is(e2, dh.ErrBadPriv) {
		return errors.New("selfcheck: bad priv not rejected")
	}
	if _, e3 := x.Secret(1, 1); !errors.Is(e3, dh.ErrBadPub) {
		return errors.New("selfcheck: bad pub not rejected")
	}
	if dh.ErrBadGroup == dh.ErrBadPriv || dh.ErrBadPriv == dh.ErrBadPub || dh.ErrBadGroup == dh.ErrBadPub {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	after, err := x.PublicKey(5)
	if err != nil || after != before {
		return errors.New("selfcheck: state changed after rejection")
	}
	return nil
}
