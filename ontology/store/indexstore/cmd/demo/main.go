// Command demo 演示带唯一二级索引的主表写入、崩溃与从水位追赶恢复，
// 并逐条打印操作的输入、输出与判定依据。
package main

import (
	"fmt"

	"ontology/ontology/store/indexstore"
)

func main() {
	disk := indexstore.NewDisk()

	// 第一阶段：正常写入（主表+WAL 同步，索引尚未追赶）。
	run(disk, func(s *indexstore.Store) {
		put(s, "alice", "email:alice@x")
		put(s, "bob", "email:bob@x")
		put(s, "carol", "email:carol@x")
		// 唯一冲突：alice 已占用该邮箱。
		put(s, "mallory", "email:alice@x")
		// alice 改邮箱 -> 旧键释放。
		put(s, "alice", "email:alice2@x")
		// 旧邮箱被 mallory 占用成功。
		put(s, "mallory", "email:alice@x")
		// alice 置空二级键。
		put(s, "alice", "")
		// 删除不存在主键。
		del(s, "ghost")
		printRecords(s.OpLog())
	})

	// 模拟崩溃重启：索引与水位可能落后（这里构造水位=0、索引=0 的现场
	// 由“从未追赶”自然形成）。用每批 2 条追赶，并展示中途查询报 stale。
	run(disk, func(s *indexstore.Store) {
		_, _, err := s.Lookup("email:bob@x")
		fmt.Printf("lookup while stale -> %v\n\n", err)
		for s.Stale() {
			reached, done, err := s.CatchUp(2)
			if err != nil {
				panic(err)
			}
			fmt.Printf("catch-up batch reached=%d done=%v\n", reached, done)
		}
		pk, ok, err := s.Lookup("email:alice@x")
		fmt.Printf("lookup after caught up -> pk=%s found=%v err=%v\n", pk, ok, err)
		diffs, err := s.Verify()
		fmt.Printf("verify diffs=%v err=%v\n", diffs, err)
		printRecords(s.OpLog())
	})
}

func run(disk *indexstore.Disk, fn func(s *indexstore.Store)) {
	crashed := indexstore.Run(disk, fn)
	fmt.Printf("(phase crashed=%v)\n", crashed)
}

func printRecords(recs []indexstore.OpRecord) {
	for _, r := range recs {
		line := fmt.Sprintf("  %-10s pk=%-8q sec=%-18q", r.Kind, r.PK, r.Sec)
		if r.OK {
			line += " OK"
		}
		if r.ErrCode != "" {
			line += " ERR=" + r.ErrCode
		}
		if r.LSN > 0 {
			line += fmt.Sprintf(" tail=%d", r.LSN)
		}
		if r.Reached > 0 {
			line += fmt.Sprintf(" reached=%d", r.Reached)
		}
		if r.Reason != "" {
			line += "  // " + r.Reason
		}
		fmt.Println(line)
	}
	fmt.Println()
}

func put(s *indexstore.Store, pk, sec string) {
	var err error
	if sec == "" {
		_, err = s.Put(pk, "", false)
	} else {
		_, err = s.Put(pk, sec, true)
	}
	if err != nil {
		fmt.Printf("put(%s,%s) rejected: %v\n", pk, sec, err)
	}
}

func del(s *indexstore.Store, pk string) {
	if _, err := s.Delete(pk); err != nil {
		fmt.Printf("delete(%s) rejected: %v\n", pk, err)
	}
}
