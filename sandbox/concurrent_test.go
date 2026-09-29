package sandbox

import (
	"errors"
	"sync"
	"testing"
)

// TestConcurrentRenameResolution runs many renames concurrently with many
// resolutions. Because every operation is serialized by the tree lock and
// resolution observes only one locked snapshot, every observed result must
// equal the serial result immediately before or after some single rename:
// the target resolves at its old path or its new path (or ErrNotExist on
// the opposite name), never a hybrid.
func TestConcurrentRenameResolution(t *testing.T) {
	tree := New()
	mustOp(t, "mkdir d1", tree.Mkdir("d1"))
	mustOp(t, "mkdir d2", tree.Mkdir("d2"))
	mustOp(t, "create d1/file", tree.CreateFile("d1/file"))
	mustOp(t, "symlink link -> /d1/file", tree.Symlink("/d1/file", "link"))

	const iterations = 200
	var wg sync.WaitGroup

	// Renamer toggles the file between d1/file and d2/file.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				_ = tree.Rename("d1/file", "d2/file")
			} else {
				_ = tree.Rename("d2/file", "d1/file")
			}
		}
	}()

	// Resolvers must only ever see one of the two serial states.
	results := make(chan string, 16)
	errs := make(chan error, 16)
	const resolvers = 4
	for r := 0; r < resolvers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				res, err := tree.Resolve("link", true)
				switch {
				case err == nil:
					if res.Path != "/d1/file" && res.Path != "/d2/file" {
						results <- "INVALID:" + res.Path
						return
					}
					results <- res.Path
				case errors.Is(err, ErrNotExist):
					// Valid serial state: the rename committed, so the
					// link's old target is temporarily dangling.
					results <- "ErrNotExist"
				default:
					errs <- err
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
		close(errs)
	}()

	counts := map[string]int{}
	for p := range results {
		if len(p) >= 7 && p[:7] == "INVALID" {
			t.Fatalf("resolution matched no serial state: %s", p)
		}
		counts[p]++
	}
	for err := range errs {
		t.Fatalf("unexpected resolution error (rename must be atomic): %v", err)
	}
	t.Logf("并发改名下解析 link: /d1/file=%d /d2/file=%d ErrNotExist=%d 依据: 每次结果都等于某次改名前或改名后的串行结果",
		counts["/d1/file"], counts["/d2/file"], counts["ErrNotExist"])

	// One more concurrent pattern: rename a directory that another goroutine
	// walks through. Results must be one of the serial states or ErrNotExist,
	// never a path that existed at no point in time.
	tree2 := New()
	mustOp(t, "mkdir p", tree2.Mkdir("p"))
	mustOp(t, "mkdir p/q", tree2.Mkdir("p/q"))
	mustOp(t, "create p/q/f", tree2.CreateFile("p/q/f"))

	var wg2 sync.WaitGroup
	invalid := make(chan string, 8)
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				_ = tree2.Rename("p/q", "r")
			} else {
				_ = tree2.Rename("r", "p/q")
			}
		}
	}()
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		for i := 0; i < iterations; i++ {
			res, err := tree2.Resolve("p/q/f", true)
			switch {
			case err == nil && res.Path == "/p/q/f":
			case errors.Is(err, ErrNotExist):
			default:
				if err == nil {
					invalid <- res.Path
				} else {
					invalid <- err.Error()
				}
			}
		}
	}()
	wg2.Wait()
	close(invalid)
	for bad := range invalid {
		t.Fatalf("directory rename interleaving produced impossible result: %s", bad)
	}
	t.Logf("并发目录改名期间解析 p/q/f 仅出现 /p/q/f 或 ErrNotExist 依据: 改名在写锁内原子完成，不会出现中间态")
}
