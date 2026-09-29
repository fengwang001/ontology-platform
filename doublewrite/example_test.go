package doublewrite_test

import (
	"fmt"
	"io"
	"log"

	"ontology/doublewrite"
)

// Example 演示一次“提交后、原位写回途中掉电”的恢复：整批要么全新、要么全旧。
func ExampleManager_basic() {
	const (
		pageCount = 3
		capPages  = 2
		pageSize  = 4 * doublewrite.SectorSize
	)
	totalSectors := pageCount*pageSize/doublewrite.SectorSize +
		capPages*pageSize/doublewrite.SectorSize + 1 // +1 完成标记扇区
	disk := doublewrite.NewSectorDisk(totalSectors)

	mgr, err := doublewrite.NewManager(disk, doublewrite.Config{
		PageCount: pageCount, CapacityPages: capPages, PageSize: pageSize,
	}, io.Discard)
	if err != nil {
		log.Fatal(err)
	}

	// 初始：三页均为版本 1。
	for id := uint32(0); id < pageCount; id++ {
		seed := doublewrite.PendingPage{PageID: id, Version: 1, Payload: []byte(fmt.Sprintf("v1-%d", id))}
		if err := mgr.FlushBatch(uint64(id+1), []doublewrite.PendingPage{seed}); err != nil {
			log.Fatal(err)
		}
	}

	// 整批提交页 0、2 升到版本 2，然后在原位写回第一页后掉电（共 11 扇区已落盘）。
	disk.ArmCrash(11, 0)
	batch := []doublewrite.PendingPage{
		{PageID: 2, Version: 2, Payload: []byte("v2-2")},
		{PageID: 0, Version: 2, Payload: []byte("v2-0")},
	}
	if err := mgr.FlushBatch(99, batch); err != nil {
		fmt.Println("flush interrupted:", err)
	}

	// 恢复：完成标记有效 -> 整批前滚。
	report, err := mgr.Recover()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("marker valid:", report.MarkerValid, "seq:", report.BatchSeq,
		"unrecoverable:", report.Unrecoverable)
	for id := uint32(0); id < pageCount; id++ {
		ver, payload, ok, _ := mgr.ReadPage(id)
		fmt.Printf("page %d -> version=%d payload=%s crc-ok=%v\n", id, ver, payload, ok)
	}
	// Output:
	// flush interrupted: doublewrite: simulated power loss
	// marker valid: true seq: 99 unrecoverable: []
	// page 0 -> version=2 payload=v2-0 crc-ok=true
	// page 1 -> version=1 payload=v1-1 crc-ok=true
	// page 2 -> version=2 payload=v2-2 crc-ok=true
}
