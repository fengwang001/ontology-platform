package replication_test

import (
	"fmt"
	"os"

	"ontology/replication"
)

// 演示带前像校验的复制应用：成功应用、前像不符冲突、整批拒绝。
func Example() {
	store := replication.NewStore(100, replication.NewTextLogger(os.Stdout))

	// 第一批：插入两行，序号从 1 连续开始。
	_, _ = store.Apply([]replication.Event{
		{Seq: 1, Op: replication.OpInsert, Key: "user-1", Before: nil,
			After: replication.Row{"name": "alice", "note": ""}},
		{Seq: 2, Op: replication.OpInsert, Key: "user-2", Before: nil,
			After: replication.Row{"name": "bob"}},
	})

	// 第二批：
	//  seq=3 前像与当前行一致 -> 更新成功；
	//  seq=4 前像缺了 note 列（当前 note=""）-> BEFORE_MISMATCH 冲突，跳过；
	//  seq=5 删除不存在的行 -> ROW_MISSING 冲突，跳过。
	res, err := store.Apply([]replication.Event{
		{Seq: 3, Op: replication.OpUpdate, Key: "user-1",
			Before: replication.Row{"name": "alice", "note": ""},
			After:  replication.Row{"name": "alice2", "note": ""}},
		{Seq: 4, Op: replication.OpUpdate, Key: "user-1",
			Before: replication.Row{"name": "alice2"},
			After:  replication.Row{"name": "alice3"}},
		{Seq: 5, Op: replication.OpDelete, Key: "ghost",
			Before: replication.Row{"name": "nobody"}, After: nil},
	})
	fmt.Printf("err=%v applied=%v conflicts=%d lastSeq=%d rowCount=%d\n",
		err, res.Applied, len(res.Conflicts), res.LastSeq, res.RowCount)
	for _, c := range res.Conflicts {
		fmt.Printf("conflict seq=%d kind=%s key=%s\n", c.Seq, c.Kind, c.Key)
	}

	// 第三批：序号不连续（期望 6，给 9）-> 整批拒绝，状态不变。
	_, err = store.Apply([]replication.Event{
		{Seq: 9, Op: replication.OpDelete, Key: "user-2",
			Before: replication.Row{"name": "bob"}, After: nil},
	})
	if reject, ok := err.(*replication.RejectError); ok {
		fmt.Printf("rejected reason=%s expected_seq=%d\n", reject.Reason, reject.ExpectedSeq)
	}
	fmt.Printf("final rowCount=%d lastSeq=%d\n", store.RowCount(), store.LastSeq())

	// Output:
	// [decision] seq=1 op=INSERT key="user-1" before=null after={"name":"alice","note":""} -> APPLIED: row absent as required by the nil before-image; after-image inserted
	// [decision] seq=2 op=INSERT key="user-2" before=null after={"name":"bob"} -> APPLIED: row absent as required by the nil before-image; after-image inserted
	// [decision] seq=3 op=UPDATE key="user-1" before={"name":"alice","note":""} after={"name":"alice2","note":""} -> APPLIED: before-image equals the current row; after-image applied
	// [decision] seq=4 op=UPDATE key="user-1" before={"name":"alice2"} after={"name":"alice3"} -> CONFLICT BEFORE_MISMATCH: before-image does not equal the current row: column "note" missing (<missing>) in before-image but present in current row with value "" (a missing column is not the empty string)
	// [decision] seq=5 op=DELETE key="ghost" before={"name":"nobody"} after=null -> CONFLICT ROW_MISSING: DELETE requires an existing row matching the before-image, but the row does not exist
	// err=<nil> applied=[3] conflicts=2 lastSeq=5 rowCount=2
	// conflict seq=4 kind=BEFORE_MISMATCH key=user-1
	// conflict seq=5 kind=ROW_MISSING key=ghost
	// [rejection] reason=SEQUENCE_GAP: event seq 9 is not consecutive; expected 6
	// rejected reason=SEQUENCE_GAP expected_seq=6
	// final rowCount=2 lastSeq=5
}
