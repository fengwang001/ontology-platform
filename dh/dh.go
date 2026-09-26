// Package dh 在模素数 p、生成元 g 的循环群上实现 Diffie-Hellman：
// 群参数校验、公钥生成、共享密钥派生、公钥/私钥校验。依赖 modarith。
package dh

import (
	"errors"
	"math/big"

	"ontology/modarith"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrBadGroup = errors.New("dh: invalid group parameters")
	ErrBadPriv  = errors.New("dh: invalid private key")
	ErrBadPub   = errors.New("dh: invalid peer public key")
)

// Group 是校验过的群参数，构造后不可变，方法可并发调用。
type Group struct {
	p uint64 // 素数模数，p >= 2
	g uint64 // 生成元，2 <= g <= p-1
}

// New 校验群参数：p >= 2 且 2 <= g <= p-1；非法则整体失败，不留任何状态。
func New(p, g uint64) (*Group, error) {
	if p < 2 || g < 2 || g > p-1 {
		return nil, ErrBadGroup
	}
	return &Group{p: p, g: g}, nil
}

// CheckPriv 校验私钥：1 <= priv <= p-2。
func (gr *Group) CheckPriv(priv uint64) error {
	if priv < 1 || priv > gr.p-2 {
		return ErrBadPriv
	}
	return nil
}

// CheckPub 校验对端公钥：2 <= pub <= p-2（拒绝 1 与 p-1）。
func (gr *Group) CheckPub(pub uint64) error {
	if pub < 2 || pub > gr.p-2 {
		return ErrBadPub
	}
	return nil
}

// PublicKey 计算 A = g^priv mod p。私钥非法则整体失败。
func (gr *Group) PublicKey(priv uint64) (uint64, error) {
	if err := gr.CheckPriv(priv); err != nil {
		return 0, err
	}
	return modarith.Modpow(gr.g, new(big.Int).SetUint64(priv), gr.p), nil
}

// Secret 计算共享密钥 S = peerPub^priv mod p。任一输入非法则整体失败。
func (gr *Group) Secret(priv, peerPub uint64) (uint64, error) {
	if err := gr.CheckPriv(priv); err != nil {
		return 0, err
	}
	if err := gr.CheckPub(peerPub); err != nil {
		return 0, err
	}
	return modarith.Modpow(peerPub, new(big.Int).SetUint64(priv), gr.p), nil
}
