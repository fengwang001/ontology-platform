// 演示内存预写日志的九条语义：正常回放、半条记录、校验不符、
// 截断边界、位翻转、空负载、同步点、追加不改历史、并发追加。
package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"sync"

	"ontology/codec"
	"ontology/segment"
	"ontology/wal"
)

var failures int

func check(name string, ok bool, detail string) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %s\n", verdict, name, detail)
}

func replay(l *wal.Log) ([]string, wal.Recovery) {
	rec := l.Recover()
	out := make([]string, 0, len(rec.Entries))
	for _, e := range rec.Entries {
		out = append(out, string(e.Payload))
	}
	return out, rec
}

func main() {
	// 1. 正常写入与全量回放
	dev := segment.NewDevice()
	l := wal.Open(dev)
	l.Append([]byte("alpha"))
	l.Append([]byte("beta"))
	l.Sync()
	got, rec := replay(l)
	check("全量回放", rec.Report.Reason == segment.StopEOF && len(got) == 2 &&
		got[0] == "alpha" && got[1] == "beta",
		fmt.Sprintf("replayed=%v stop=%d", got, rec.Report.StopAt))

	// 2. 半条记录：截在第二条中间，恢复停在第二条起点
	dev2 := segment.NewDevice()
	l2 := wal.Open(dev2)
	l2.Append([]byte("full"))
	half := l2.Append([]byte("half-record"))
	l2.Sync()
	dev2.Truncate(half + 5)
	got2, rec2 := replay(l2)
	check("半条记录", rec2.Report.Reason == segment.StopTruncated &&
		rec2.Report.StopAt == half && len(got2) == 1,
		fmt.Sprintf("stop=%d reason=truncated replayed=%v", rec2.Report.StopAt, got2))

	// 3. 校验不符：翻转第二条，后面的完整记录也不许捡回来
	dev3 := segment.NewDevice()
	l3 := wal.Open(dev3)
	l3.Append([]byte("good-1"))
	bad := l3.Append([]byte("bad-2"))
	l3.Append([]byte("good-3"))
	l3.Sync()
	dev3.Flip(bad+codec.PrefixLen, 0xFF)
	got3, rec3 := replay(l3)
	check("校验不符", rec3.Report.Reason == segment.StopCorrupt &&
		rec3.Report.StopAt == bad && len(got3) == 1,
		fmt.Sprintf("stop=%d reason=corrupt replayed=%v", rec3.Report.StopAt, got3))

	// 4. 截断边界：恰好截在记录最后一个字节上，三条全在；少一字节则丢尾
	dev4 := segment.NewDevice()
	l4 := wal.Open(dev4)
	for _, s := range []string{"one", "two", "three"} {
		l4.Append([]byte(s))
	}
	l4.Sync()
	full := dev4.Len()
	dev4.Truncate(full)
	exact, _ := replay(l4)
	dev4.Truncate(full - 1)
	minus1, rec4 := replay(l4)
	check("截断边界", len(exact) == 3 && len(minus1) == 2 &&
		rec4.Report.Reason == segment.StopTruncated,
		fmt.Sprintf("at-last-byte=%d条 minus-one=%d条", len(exact), len(minus1)))

	// 5. 单字节翻转必被发现
	dev5 := segment.NewDevice()
	l5 := wal.Open(dev5)
	off5 := l5.Append([]byte("flip me"))
	l5.Sync()
	dev5.Flip(off5+codec.PrefixLen+3, 0x01)
	_, rec5 := replay(l5)
	check("位翻转", rec5.Report.Reason == segment.StopCorrupt && rec5.Report.StopAt == off5,
		fmt.Sprintf("stop=%d 坏数据未被回放", rec5.Report.StopAt))

	// 6. 空负载记录：回放成一条负载为空的记录，不与无记录混淆
	dev6 := segment.NewDevice()
	l6 := wal.Open(dev6)
	l6.Append([]byte{})
	l6.Sync()
	rec6 := l6.Recover()
	emptyOK := len(rec6.Entries) == 1 && rec6.Entries[0].Payload != nil &&
		len(rec6.Entries[0].Payload) == 0
	check("空负载", emptyOK, fmt.Sprintf("entries=%d payload=%q",
		len(rec6.Entries), rec6.Entries[0].Payload))

	// 7. 同步点限制回放：同步点之后的完整记录不回放
	dev7 := segment.NewDevice()
	l7 := wal.Open(dev7)
	l7.Append([]byte("synced"))
	l7.Sync()
	l7.Append([]byte("unsynced"))
	got7, rec7 := replay(l7)
	check("同步点", len(got7) == 1 && got7[0] == "synced" &&
		rec7.Report.StopAt == l7.SyncedPosition(),
		fmt.Sprintf("synced=%d write=%d replayed=%v",
			l7.SyncedPosition(), l7.WritePosition(), got7))

	// 8. 追加不改历史：每次追加后的快照都是最终缓冲的逐字节前缀
	dev8 := segment.NewDevice()
	l8 := wal.Open(dev8)
	var snaps [][]byte
	for i := 0; i < 4; i++ {
		l8.Append([]byte("r" + strconv.Itoa(i)))
		snaps = append(snaps, dev8.Snapshot())
	}
	final := dev8.Snapshot()
	prefixOK := true
	for _, s := range snaps {
		if len(final) < len(s) || !bytes.Equal(final[:len(s)], s) {
			prefixOK = false
		}
	}
	check("历史不变", prefixOK, fmt.Sprintf("snapshots=%d final=%d字节", len(snaps), len(final)))

	// 9. 并发追加：全部记录完整不交错，恢复全部回放
	dev9 := segment.NewDevice()
	l9 := wal.Open(dev9)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				l9.Append([]byte("w" + strconv.Itoa(id) + "-" + strconv.Itoa(i)))
			}
		}(w)
	}
	wg.Wait()
	l9.Sync()
	got9, rec9 := replay(l9)
	check("并发追加", rec9.Report.Reason == segment.StopEOF && len(got9) == 400,
		fmt.Sprintf("replayed=%d/400 stop=%d", len(got9), rec9.Report.StopAt))

	fmt.Printf("TOTAL %d/9 通过, %d 失败\n", 9-failures, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
