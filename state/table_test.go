package state

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock 是可手动推进的墙钟，保证测试可复现。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const testTTL = 10 * time.Second

func newTestTable(t *testing.T, c *fakeClock) *Table {
	t.Helper()
	tbl, err := NewTable(testTTL, c.Now)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return tbl
}

// 边界：now - lastActivity >= ttl 即过期，恰好等于也算过期。
func TestBoundaryExactlyTTL(t *testing.T) {
	c := &fakeClock{now: base}
	tbl := newTestTable(t, c)

	if err := tbl.Put("k", "v", base); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Logf("输入: Put(k, v, eventTime=%v), ttl=%v", base, testTTL)

	c.Set(base.Add(testTTL - time.Nanosecond))
	if v, ok := tbl.Get("k"); !ok {
		t.Fatalf("now-lastActivity=%v < ttl, 应命中", testTTL-time.Nanosecond)
	} else {
		t.Logf("判定: now-lastActivity=%v < ttl=%v => 未过期, Get=%v", testTTL-time.Nanosecond, testTTL, v)
	}

	c.Set(base.Add(testTTL))
	if _, ok := tbl.Get("k"); ok {
		t.Fatalf("now-lastActivity=%v == ttl, 应判定过期", testTTL)
	}
	t.Logf("判定: now-lastActivity=%v >= ttl=%v => 已过期, Get 未命中并惰性清除", testTTL, testTTL)

	if n := tbl.Len(); n != 0 {
		t.Fatalf("惰性清除后 Len=%d, 期望 0", n)
	}
	t.Logf("结果: 惰性清除生效, Len=0")
}

// 乱序与迟到事件：最后活动时间只进不退。
func TestOutOfOrderEvents(t *testing.T) {
	c := &fakeClock{now: base}
	tbl := newTestTable(t, c)

	t2 := base.Add(8 * time.Second)
	t1 := base.Add(2 * time.Second)

	if err := tbl.Put("k", "new", t2); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := tbl.Put("k", "late", t1); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Logf("输入: 先写 eventTime=%v, 再写迟到事件 eventTime=%v", t2, t1)

	// 若最后活动时间回退到 t1，则在 t1+ttl 时刻会误判过期；
	// 正确行为是保持 t2，t1+ttl 时距 t2 仅 6s < ttl，仍应命中。
	c.Set(t1.Add(testTTL))
	v, ok := tbl.Get("k")
	if !ok || v != "late" {
		t.Fatalf("lastActivity 不应回退: Get=(%v,%v), 期望 (late,true)", v, ok)
	}
	t.Logf("判定: lastActivity 保持 %v, now-lastActivity=%v < ttl=%v => 命中", t2, c.Now().Sub(t2), testTTL)

	// 推进到 t2+ttl，恰好过期。
	c.Set(t2.Add(testTTL))
	if _, ok := tbl.Get("k"); ok {
		t.Fatalf("now-lastActivity=ttl, 应过期")
	}
	t.Logf("结果: now-lastActivity=%v >= ttl => 过期并惰性清除", testTTL)
}

// 主动清理只清除已过期条目。
func TestCleanupOnlyExpired(t *testing.T) {
	c := &fakeClock{now: base}
	tbl := newTestTable(t, c)

	mustPut := func(k string, et time.Time) {
		if err := tbl.Put(k, k+"-v", et); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}
	mustPut("old1", base)
	mustPut("old2", base.Add(time.Second))
	mustPut("fresh", base.Add(9*time.Second))
	t.Logf("输入: old1@%v old2@%v fresh@%v, ttl=%v", base, base.Add(time.Second), base.Add(9*time.Second), testTTL)

	c.Set(base.Add(11 * time.Second)) // old1 差 11s 过期; old2 恰好等于 ttl 过期; fresh 差 2s 未过期
	removed := tbl.Cleanup()
	if removed != 2 {
		t.Fatalf("Cleanup 清除 %d 个, 期望 2", removed)
	}
	t.Logf("判定: old1 差 %v(>=ttl), old2 差 %v(==ttl 边界), fresh 差 %v(<ttl) => 清除 2 个",
		11*time.Second, testTTL, 2*time.Second)

	if n := tbl.Len(); n != 1 {
		t.Fatalf("Cleanup 后 Len=%d, 期望 1", n)
	}
	if _, ok := tbl.Get("fresh"); !ok {
		t.Fatalf("fresh 未过期, 应保留")
	}
	t.Logf("结果: 仅过期条目被清除, fresh 保留, Len=1")
}

// 非法输入：空键与非正过期时长必须被拒，且状态不变、可继续使用。
func TestInvalidInputs(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		_, err := NewTable(ttl, nil)
		if !errors.Is(err, ErrInvalidTTL) {
			t.Fatalf("ttl=%v 应返回 ErrInvalidTTL, 实际 %v", ttl, err)
		}
		t.Logf("输入: ttl=%v => 拒绝, err=%v (errors.Is ErrInvalidTTL=true)", ttl, err)
	}

	c := &fakeClock{now: base}
	tbl := newTestTable(t, c)
	if err := tbl.Put("k", "v", base); err != nil {
		t.Fatalf("Put: %v", err)
	}

	err := tbl.Put("", "bad", base)
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空键应返回 ErrEmptyKey, 实际 %v", err)
	}
	t.Logf("输入: Put(\"\", bad) => 拒绝, err=%v (errors.Is ErrEmptyKey=true)", err)

	if n := tbl.Len(); n != 1 {
		t.Fatalf("拒绝后状态不应改变, Len=%d, 期望 1", n)
	}
	if v, ok := tbl.Get("k"); !ok || v != "v" {
		t.Fatalf("拒绝后已有条目不应受影响, Get=(%v,%v)", v, ok)
	}
	if err := tbl.Put("k2", "v2", base); err != nil {
		t.Fatalf("拒绝后表应可继续使用: %v", err)
	}
	t.Logf("结果: 状态不变(Len=1, k 可读), 拒绝后 Put(k2) 正常")
}

// 并发读写清理自检：任何 Get 命中都必须与冻结墙钟下的过期判定一致，
// 不允许出现已过期却返回旧值的中间态。
func TestConcurrentNoIntermediateState(t *testing.T) {
	c := &fakeClock{now: base}
	tbl := newTestTable(t, c)

	const keys = 16
	// 每个键的事件时间确定：一半在冻结墙钟下已过期，一半未过期。
	frozen := base.Add(testTTL)
	eventTime := func(i int) time.Time {
		if i%2 == 0 {
			return base // 冻结时刻 now-lastActivity = ttl => 已过期
		}
		return base.Add(2 * time.Second) // 差 ttl-2s => 未过期
	}

	c.Set(frozen)
	var wg sync.WaitGroup
	errCh := make(chan error, keys*64)

	for i := 0; i < keys; i++ {
		i := i
		key := fmt.Sprintf("k%d", i)
		// 写入者：乱序写同一键，最后活动时间不得回退。
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := tbl.Put(key, i, eventTime(i)); err != nil {
				errCh <- err
			}
			if err := tbl.Put(key, i, eventTime(i).Add(-time.Hour)); err != nil { // 迟到事件
				errCh <- err
			}
		}()
		// 读取者：命中时必须未过期。
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 32; n++ {
				v, ok := tbl.Get(key)
				if !ok {
					continue
				}
				if c.Now().Sub(eventTime(i)) >= tbl.TTL() {
					errCh <- fmt.Errorf("中间态: key=%s 已过期却返回值 %v", key, v)
				}
			}
		}()
	}
	// 清理者与自检者。
	wg.Add(2)
	go func() {
		defer wg.Done()
		for n := 0; n < 32; n++ {
			tbl.Cleanup()
		}
	}()
	go func() {
		defer wg.Done()
		for n := 0; n < 32; n++ {
			if got := tbl.Check(); got < 0 || got > keys {
				errCh <- fmt.Errorf("Check 返回非法值 %d", got)
			}
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	// 冻结墙钟下最终状态确定：偶数键过期，奇数键存活。
	tbl.Cleanup()
	alive, dead := 0, 0
	for i := 0; i < keys; i++ {
		if _, ok := tbl.Get(fmt.Sprintf("k%d", i)); ok {
			alive++
		} else {
			dead++
		}
	}
	t.Logf("判定: 冻结墙钟 now=%v, ttl=%v; 偶数键 eventTime=%v(过期), 奇数键 eventTime=%v(存活)",
		frozen, testTTL, base, base.Add(2*time.Second))
	t.Logf("结果: 并发后存活 %d, 过期 %d", alive, dead)
	if alive != keys/2 || dead != keys/2 {
		t.Fatalf("最终状态不一致: alive=%d dead=%d, 期望各 %d", alive, dead, keys/2)
	}
}
