package paywall

import "testing"

func TestConcurrentOperations(t *testing.T) {
	t.Parallel()
	p := New(20, 1000, 20, 5, 100)
	for i := 0; i < 20; i++ {
		article := articleName(i)
		if err := p.AddArticle(article, i%7 == 0); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			now := int64(i)
			device := "d" + string(rune('a'+i%4))
			article := articleName(i % 20)
			_, _ = p.Read(now, device, article, nil)
			_ = p.Login(now, device, "u"+string(rune('a'+i%3)))
			_ = p.Logout(now, device)
			_ = p.Subscribe(now, "u"+string(rune('a'+i%3)), now+10)
			_, _ = p.Gift(now, "u"+string(rune('a'+i%3)), article)
		}
	}()

	for i := 0; i < 200; i++ {
		now := int64(i)
		device := "p" + string(rune('a'+i%4))
		article := articleName((i + 3) % 20)
		_, _ = p.Read(now, device, article, nil)
		_ = p.Login(now, device, "p"+string(rune('a'+i%2)))
		_ = p.Logout(now, device)
	}
	<-done
}

func articleName(i int) string {
	if i < 10 {
		return "a0" + string(rune('0'+i))
	}
	return "a" + string(rune('0'+i/10)) + string(rune('0'+i%10))
}
