package session_test

import (
	"sync"
	"testing"

	"ontology/caps"
	"ontology/session"
)

// 并发混合调用：结果须等价于某串行顺序，不变量恒成立。
func TestConcurrent(t *testing.T) {
	newCapture()
	tab, err := caps.NewTable(buildFeatures(nil))
	if err != nil {
		t.Fatal(err)
	}
	m := session.NewManager(tab)
	if err := m.SetServer(2, caps.Hello{Lo: 1, Hi: 10, Sup: 0xffffffff}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				sid, info, err := m.Negotiate(
					caps.Hello{Lo: 1, Hi: 10, Sup: 0xffffffff}, uint8(id%3))
				if err != nil {
					t.Errorf("Negotiate: %v", err)
					return
				}
				if info.Using != 0 || info.Using&^info.Enabled != 0 {
					t.Errorf("bad session info: %+v", info)
					return
				}
				for f := uint8(0); f < caps.FeatureCount; f++ {
					_ = m.Use(sid, f)
				}
				got, e := m.Info(sid)
				if e != nil {
					t.Errorf("Info: %v", e)
					return
				}
				if got.Using&^got.Enabled != 0 {
					t.Errorf("U not subset of E: %+v", got)
					return
				}
				if _, e := m.Renegotiate(sid); e != nil {
					t.Errorf("Renegotiate: %v", e)
					return
				}
				if e := m.Close(sid); e != nil {
					t.Errorf("Close: %v", e)
					return
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = m.SetServer(2, caps.Hello{Lo: 1, Hi: 10, Sup: 0xffffffff})
			_ = m.SetServer(1, caps.Hello{Lo: 1, Hi: 10, Sup: 0xffffffff})
		}
	}()
	wg.Wait()
}
