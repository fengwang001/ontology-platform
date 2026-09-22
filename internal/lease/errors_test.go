package lease

import (
	"errors"
	"math"
	"testing"
)

// 本文件钉住三类哨兵错误必须两两可区分。实现注释原话：
//   - manager.go Renew: "holder 不是当前记录的持有者（被抢占/从未持有/已释放）：
//     ErrNotHolder；holder 匹配但 token 不符：ErrTokenMismatch；
//     holder 与 token 都匹配但租约已过期：ErrLeaseExpired"
//   - errors.go ErrNotHolder: "被抢占后调用 Renew/Release 返回 ErrNotHolder，
//     与'自己持有但已过期'返回的 ErrLeaseExpired 是不同类别"
//   - errors.go ErrStaleToken: "token 是'当前 token 的前一个值'与'从未发出过的
//     巨大值'归为同一类错误 ErrStaleToken"
//
// 判别力（改坏方式 -> 失败的测试）：
//   - Renew 把 ErrLeaseExpired 换成 ErrNotHolder（或三者合并为同一哨兵）
//     -> TestRenewThreeSentinelsDistinct 失败。
//   - Renew 把 token 检查挪到 holder 检查之前（被抢占者报 ErrTokenMismatch）
//     -> TestRenewThreeSentinelsDistinct 的"被抢占"用例失败。
//   - Write 把"从未发出的巨大 token"与"旧 token"拆成两种错误
//     -> TestWriteStaleTokenSameClass 失败。

// TestRenewThreeSentinelsDistinct 分别构造三种失败场景，断言拿到的
// 错误能被 errors.Is 判为对应哨兵，且与其余两个哨兵全部判否。
func TestRenewThreeSentinelsDistinct(t *testing.T) {
	cases := []struct {
		name string
		run  func(m *Manager, c *testClock) error
		want error
	}{
		{"被抢占者续约", func(m *Manager, c *testClock) error {
			tokA, _ := m.Acquire("A", 100)
			c.Set(100) // A 过期，B 接管
			if _, err := m.Acquire("B", 100); err != nil {
				t.Fatalf("B Acquire: %v", err)
			}
			return m.Renew("A", tokA, 100)
		}, ErrNotHolder},
		{"持有者 token 不符", func(m *Manager, c *testClock) error {
			tok, _ := m.Acquire("A", 100)
			return m.Renew("A", tok+1, 100) // 未过期但 token 错
		}, ErrTokenMismatch},
		{"持有者租约已过期", func(m *Manager, c *testClock) error {
			tok, _ := m.Acquire("A", 100)
			c.Set(100) // 到期，记录仍在
			return m.Renew("A", tok, 100)
		}, ErrLeaseExpired},
	}
	sentinels := []error{ErrNotHolder, ErrTokenMismatch, ErrLeaseExpired}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, c := newTestManager()
			err := tc.run(m, c)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, 期望 errors.Is(%v)", err, tc.want)
			}
			for _, s := range sentinels {
				if s != tc.want && errors.Is(err, s) {
					t.Fatalf("err = %v 不应同时被判为 %v：哨兵必须两两区分", err, s)
				}
			}
		})
	}
}

// TestReleaseSentinelsDistinct 钉住 Release 注释"holder 不匹配（已被他人
// 抢占）仍返回 ErrNotHolder，token 不符仍返回 ErrTokenMismatch"。
func TestReleaseSentinelsDistinct(t *testing.T) {
	m, c := newTestManager()
	tokA, _ := m.Acquire("A", 100)

	// token 不符（holder 匹配）：ErrTokenMismatch，且不是 ErrNotHolder。
	err := m.Release("A", tokA+1)
	if !errors.Is(err, ErrTokenMismatch) || errors.Is(err, ErrNotHolder) {
		t.Fatalf("token 不符的 Release = %v, 期望 ErrTokenMismatch", err)
	}
	// 失败的 Release 不得清除记录：A 仍可写。
	if err := m.Write(tokA, "k", "v"); err != nil {
		t.Fatalf("失败的 Release 不得影响租约, Write = %v", err)
	}

	// 被抢占后：ErrNotHolder，且不是 ErrTokenMismatch。
	c.Set(100)
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("B Acquire: %v", err)
	}
	err = m.Release("A", tokA)
	if !errors.Is(err, ErrNotHolder) || errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("被抢占者的 Release = %v, 期望 ErrNotHolder", err)
	}
}

// TestWriteStaleTokenSameClass 钉住 ErrStaleToken 注释的取舍：
// "当前 token 的前一个值"与"从未发出过的巨大值"必须归为同一类错误。
func TestWriteStaleTokenSameClass(t *testing.T) {
	m, _ := newTestManager()
	tok1, _ := m.Acquire("A", 100)
	tok2, err := m.Acquire("A", 100) // 同人重复获取，tok1 过时
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}

	errPrev := m.Write(tok1, "k", "x")           // 当前 token 的前一个值
	errHuge := m.Write(math.MaxUint64, "k", "x") // 从未发出过的巨大值

	if !errors.Is(errPrev, ErrStaleToken) || !errors.Is(errHuge, ErrStaleToken) {
		t.Fatalf("prev=%v huge=%v, 两者都必须是 ErrStaleToken", errPrev, errHuge)
	}
	if errPrev != errHuge {
		t.Fatalf("prev=%v 与 huge=%v 必须是同一个哨兵", errPrev, errHuge)
	}
	for _, s := range []error{ErrNotHolder, ErrTokenMismatch, ErrLeaseExpired} {
		if errors.Is(errPrev, s) || errors.Is(errHuge, s) {
			t.Fatalf("ErrStaleToken 不应被判为 %v", s)
		}
	}
	// 当前有效 token 对照：必须能写成功。
	if err := m.Write(tok2, "k", "ok"); err != nil {
		t.Fatalf("当前 token 写应成功, got %v", err)
	}
}
