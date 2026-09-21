// demo 逐条判定 upload 包的 8 项语义并打印 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/upload"
)

var failed bool

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failed = true
	}
	fmt.Printf("%-28s %s\n", name, verdict)
}

func main() {
	// 1. 分片号边界
	u := upload.New(3, 10)
	bad := errors.Is(u.Put(0, 10, "e"), upload.ErrBadPart) &&
		errors.Is(u.Put(4, 10, "e"), upload.ErrBadPart)
	r := u.Stat()
	check("1 part-number bounds", bad &&
		r.Received == 0 && r.Bytes == 0 &&
		u.Put(1, 10, "a") == nil && u.Put(3, 1, "c") == nil)

	// 2. 最小片大小
	u = upload.New(3, 10)
	check("2 min part size", errors.Is(u.Put(1, 9, "a"), upload.ErrBadPart) &&
		errors.Is(u.Put(2, 0, "b"), upload.ErrBadPart) &&
		errors.Is(u.Put(3, 0, "c"), upload.ErrBadPart) &&
		u.Put(3, 1, "c") == nil && u.Put(1, 10, "a") == nil)

	// 3. 覆盖的账目
	u = upload.New(2, 5)
	_ = u.Put(1, 5, "a")
	_ = u.Put(2, 7, "b")
	_ = u.Put(1, 20, "a2")
	_ = u.Put(1, 8, "a3")
	r = u.Stat()
	check("3 replace accounting", r.Received == 2 && r.Bytes == 15 && r.Replaced == 2)

	// 4. 缺片报告
	u = upload.New(5, 1)
	_ = u.Put(2, 1, "b")
	_ = u.Put(4, 1, "d")
	err := u.Complete([]string{"a", "b", "c", "d", "e"})
	m := u.Stat().Missing
	check("4 missing parts report", errors.Is(err, upload.ErrMissingPart) &&
		fmt.Sprint(m) == "[1 3 5]" &&
		u.Put(1, 1, "a") == nil) // 未被标记为完成

	// 5. 校验值比对
	u = upload.New(2, 1)
	_ = u.Put(1, 3, "a")
	_ = u.Put(2, 4, "b")
	bad = errors.Is(u.Complete([]string{"a"}), upload.ErrEtagMismatch) &&
		errors.Is(u.Complete([]string{"a", "x"}), upload.ErrEtagMismatch)
	r = u.Stat()
	check("5 etag mismatch", bad && r.Received == 2 && r.Bytes == 7 &&
		u.Complete([]string{"a", "b"}) == nil)

	// 6. 完成后冻结
	u = upload.New(1, 1)
	_ = u.Put(1, 5, "a")
	_ = u.Complete([]string{"a"})
	r = u.Stat()
	check("6 frozen after complete",
		errors.Is(u.Put(1, 9, "z"), upload.ErrCompleted) &&
			errors.Is(u.Complete([]string{"a"}), upload.ErrCompleted) &&
			r.Received == 1 && r.Bytes == 5)

	// 7. 错误优先级：缺片先于 etag 数量
	u = upload.New(3, 1)
	_ = u.Put(1, 1, "a")
	check("7 error priority", errors.Is(u.Complete([]string{"a", "b"}), upload.ErrMissingPart))

	// 8. 并发守恒
	u = upload.New(16, 1)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = u.Put((w*50+i)%16+1, int64((w+1)*1000+i), "e")
			}
		}(w)
	}
	wg.Wait()
	r = u.Stat()
	check("8 concurrent conservation", r.Received == 16 &&
		r.Bytes > 0 && r.Replaced == 8*50-r.Received)

	if failed {
		os.Exit(1)
	}
}
