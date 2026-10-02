package epoll

import (
	"sync"
	"testing"
)

func TestConcurrentOperations(t *testing.T) {
	const instances = 4
	const files = 24
	p, err := New(files, instances, files*instances)
	if err != nil {
		t.Fatal(err)
	}

	for ep := 0; ep < instances; ep++ {
		for fd := 0; fd < files; fd++ {
			flags := 0
			if fd%4 == 0 {
				flags = ET
			}
			if err := p.Add(ep, fd, IN|OUT, flags); err != nil {
				t.Fatal(err)
			}
		}
	}

	var wg sync.WaitGroup
	for ep := 0; ep < instances; ep++ {
		ep := ep
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				fd := i % files
				flags := ET * (i % 2)
				if err := p.Mod(ep, fd, IN|OUT, flags); err != nil {
					t.Errorf("Mod: %v", err)
					return
				}
				if _, err := p.Wait(ep, 8); err != nil {
					t.Errorf("Wait: %v", err)
					return
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			fd := i % files
			state := []int{0, IN, OUT, IN | OUT, ERR, HUP, IN | ERR}[i%7]
			if err := p.SetState(fd, state); err != nil {
				t.Errorf("SetState: %v", err)
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			for ep := 0; ep < instances; ep++ {
				queue, err := p.Queue(ep)
				if err != nil {
					t.Errorf("Queue: %v", err)
					return
				}
				seen := make(map[int]bool, len(queue))
				for _, fd := range queue {
					if seen[fd] {
						t.Errorf("duplicate fd %d in queue %d", fd, ep)
						return
					}
					seen[fd] = true
				}
				if _, err := p.Watches(ep); err != nil {
					t.Errorf("Watches: %v", err)
					return
				}
			}
		}
	}()

	wg.Wait()

	for ep := range p.instances {
		queue, err := p.Queue(ep)
		if err != nil {
			t.Fatal(err)
		}
		seen := make(map[int]bool, len(queue))
		for _, fd := range queue {
			if seen[fd] {
				t.Fatalf("duplicate fd %d in queue %d", fd, ep)
			}
			seen[fd] = true
		}

		p.mu.RLock()
		for fd, w := range p.instances[ep].watches {
			if w.queued != seen[fd] {
				t.Fatalf("ep %d fd %d queued flag = %v, queue membership = %v", ep, fd, w.queued, seen[fd])
			}
			if w.disabled {
				t.Fatalf("ep %d fd %d remained disabled", ep, fd)
			}
			if w.eff&(ERR|HUP) != ERR|HUP {
				t.Fatalf("ep %d fd %d eff = %d", ep, fd, w.eff)
			}
		}
		p.mu.RUnlock()
	}
}
