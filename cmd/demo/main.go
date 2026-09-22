// demo 逐条演练内存 WAL 的九条语义，每步打印一行 OK/FAIL 判定。
// 不读命令行参数、不联网、不碰真实文件系统。
package main

import (
	"bytes"
	"fmt"
	"sync"

	"ontology/codec"
	"ontology/segment"
	"ontology/wal"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	status := "FAIL"
	if ok {
		status = "OK"
		passed++
	}
	fmt.Printf("%s %s: %s\n", status, name, detail)
}

func mustAppend(w *wal.WAL, p []byte) int {
	off, err := w.Append(p)
	if err != nil {
		panic(err)
	}
	return off
}

func main() {
	// 1. 正常状态：正常写入与全量回放。
	w := wal.Open(segment.NewMemBuffer())
	want := [][]byte{[]byte("alpha"), []byte("beta"), []byte("gamma")}
	for _, p := range want {
		mustAppend(w, p)
	}
	w.Sync()
	recs, stop := w.Recover()
	ok := len(recs) == len(want) && stop.Reason == codec.Complete
	for i := range want {
		ok = ok && bytes.Equal(recs[i].Payload, want[i])
	}
	check("正常写入与全量回放", ok, fmt.Sprintf("回放 %d 条，停在第 %d 字节", len(recs), stop.Offset))

	// 2. 半条记录：截断在第二条中间，恢复停在第二条起点。
	buf := segment.NewMemBuffer()
	w = wal.Open(buf)
	mustAppend(w, []byte("rec-1"))
	off2 := mustAppend(w, []byte("rec-2-partial"))
	w.Sync()
	buf.Truncate(off2 + 3)
	recs, stop = w.Recover()
	ok = len(recs) == 1 && stop.Reason == codec.Truncated && stop.Offset == off2
	check("半条记录停止", ok, fmt.Sprintf("回放 %d 条，停在第 %d 字节（%s）", len(recs), stop.Offset, stop.Reason))

	// 3. 校验不符：翻坏第二条的负载，恢复停在第二条起点，不捡第三条。
	buf = segment.NewMemBuffer()
	w = wal.Open(buf)
	mustAppend(w, []byte("good-1"))
	off2 = mustAppend(w, []byte("good-2"))
	mustAppend(w, []byte("good-3"))
	w.Sync()
	buf.Bytes()[off2+codec.LenSize] ^= 0xFF
	recs, stop = w.Recover()
	ok = len(recs) == 1 && stop.Reason == codec.Corrupt && stop.Offset == off2
	check("校验不符停止", ok, fmt.Sprintf("回放 %d 条，停在第 %d 字节（%s）", len(recs), stop.Offset, stop.Reason))

	// 4. 截断边界：恰好截在第二条最后一个字节上，两条都完整回放。
	buf = segment.NewMemBuffer()
	w = wal.Open(buf)
	mustAppend(w, []byte("edge-1"))
	mustAppend(w, []byte("edge-2"))
	boundary := mustAppend(w, []byte("edge-3"))
	w.Sync()
	buf.Truncate(boundary)
	recs, stop = w.Recover()
	ok = len(recs) == 2 && stop.Reason == codec.Complete && stop.Offset == boundary
	check("截断在记录最后一字节", ok, fmt.Sprintf("回放 %d 条，停在第 %d 字节", len(recs), stop.Offset))

	// 5. 单字节翻转：负载每个字节逐一翻转，每次都报校验不符。
	payload := []byte("flip-any-byte")
	detected := 0
	for i := range payload {
		b := segment.NewMemBuffer()
		wf := wal.Open(b)
		mustAppend(wf, payload)
		wf.Sync()
		b.Bytes()[codec.LenSize+i] ^= 0x01
		_, st := wf.Recover()
		if st.Reason == codec.Corrupt {
			detected++
		}
	}
	check("单字节翻转被发现", detected == len(payload),
		fmt.Sprintf("%d/%d 处翻转均报校验不符", detected, len(payload)))

	// 6. 空负载记录：回放为一条"负载为空"的记录，不与无记录混淆。
	buf = segment.NewMemBuffer()
	w = wal.Open(buf)
	mustAppend(w, []byte("non-empty"))
	mustAppend(w, []byte{})
	w.Sync()
	recs, _ = w.Recover()
	ok = len(recs) == 2 && recs[1].Payload != nil && len(recs[1].Payload) == 0
	check("空负载记录", ok, fmt.Sprintf("回放 %d 条，第 2 条负载长度 %d", len(recs), len(recs[1].Payload)))

	// 7. 同步点：同步点之后的完整记录不回放。
	buf = segment.NewMemBuffer()
	w = wal.Open(buf)
	mustAppend(w, []byte("synced-1"))
	mustAppend(w, []byte("synced-2"))
	w.Sync()
	mustAppend(w, []byte("not-yet-synced"))
	recs, stop = w.Recover()
	ok = len(recs) == 2 && stop.Offset == w.Synced() && w.Written() > w.Synced()
	check("同步点限制回放", ok, fmt.Sprintf("写入点 %d，同步点 %d，回放 %d 条",
		w.Written(), w.Synced(), len(recs)))

	// 8. 追加不改历史：每次追加后历史前缀逐字节不变。
	w = wal.Open(segment.NewMemBuffer())
	mustAppend(w, []byte("base"))
	prev := w.Snapshot()
	stable := true
	for i := 0; i < 10; i++ {
		mustAppend(w, []byte{byte(i)})
		now := w.Snapshot()
		stable = stable && bytes.Equal(now[:len(prev)], prev)
		prev = now
	}
	check("追加不改历史", stable, "10 次追加后前缀逐字节不变")

	// 9. 并发追加：全部记录完整不交错，偏移与返回值一致。
	w = wal.Open(segment.NewMemBuffer())
	const writers, perWriter = 8, 50
	var mu sync.Mutex
	sent := make(map[int][]byte, writers*perWriter)
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				p := []byte(fmt.Sprintf("w%02d-%04d", id, i))
				off := mustAppend(w, p)
				mu.Lock()
				sent[off] = p
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	w.Sync()
	recs, stop = w.Recover()
	ok = len(recs) == writers*perWriter && stop.Reason == codec.Complete
	for _, r := range recs {
		ok = ok && bytes.Equal(r.Payload, sent[r.Offset])
	}
	check("并发追加全部可回放", ok, fmt.Sprintf("回放 %d/%d 条", len(recs), writers*perWriter))

	// 总计。
	summary := "FAIL"
	if passed == total {
		summary = "OK"
	}
	fmt.Printf("%s 总计: %d/%d 项通过\n", summary, passed, total)
}
