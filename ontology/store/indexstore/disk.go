package indexstore

import (
	"fmt"
	"strconv"
	"strings"
)

// Disk 是确定性、可注入崩溃的持久层。所有改动只有在“提交步骤”发生时才
// 可见：未提交的写入在崩溃（Fail 或重新打开）后全部丢失。这精确建模
// “主表与日志同步落盘、索引与水位分别落盘、水位可落后但不超前”。
//
// 磁盘文件格式（每个 key 一个文件，仅用于测试与演示）：
//
//	wal        -> 行文本：LSN,op,pk,hasOld,oldSec,hasNew,newSec（字段用 \x1f 分隔）
//	table:<pk> -> \x1f 分隔：sec,hasSec
//	index:<s>  -> 二级键 s 当前指向的主键
//	applied    -> 索引已持久吸收到的 LSN（>= watermark，随索引逐项推进）
//	watermark  -> 水位 LSN
type Disk struct {
	files map[string]string
	// CrashHook 在每次 CommitStep 前被询问；返回非空描述则模拟崩溃：
	// 当前批已暂存的改动全部丢弃。崩溃点只消耗一次。
	crashHook func(step string) bool
}

// NewDisk 创建空磁盘。
func NewDisk() *Disk {
	return &Disk{files: map[string]string{}}
}

// SetCrashHook 安装崩溃注入钩子（nil 表示取消）。
func (d *Disk) SetCrashHook(h func(step string) bool) {
	d.crashHook = h
}

func (d *Disk) crash(step string) bool {
	if d.crashHook != nil && d.crashHook(step) {
		return true
	}
	return false
}

// stagedCommit 暂存一批改动，随后在单个提交步骤原子可见。
// 提交步骤前发生注入崩溃，则改动全部不生效。
func (d *Disk) stagedCommit(step string, changes map[string]string, deletes []string) bool {
	if d.crash(step) {
		return false
	}
	for k, v := range changes {
		d.files[k] = v
	}
	for _, k := range deletes {
		delete(d.files, k)
	}
	return true
}

func (d *Disk) get(k string) (string, bool) {
	v, ok := d.files[k]
	return v, ok
}

// ---- WAL 编解码 ----

const fs = "\x1f"

func encodeEntry(e LogEntry) string {
	op := "u"
	if e.Op == LogDelete {
		op = "d"
	}
	return strings.Join([]string{
		strconv.Itoa(e.LSN), op, e.PK,
		strconv.FormatBool(e.HasOld), e.OldSec,
		strconv.FormatBool(e.HasNew), e.NewSec,
	}, fs)
}

func decodeEntry(line string) (LogEntry, error) {
	p := strings.Split(line, fs)
	if len(p) != 7 {
		return LogEntry{}, fmt.Errorf("corrupt wal record: %q", line)
	}
	lsn, err := strconv.Atoi(p[0])
	if err != nil {
		return LogEntry{}, err
	}
	e := LogEntry{LSN: lsn, PK: p[2], OldSec: p[4], NewSec: p[6]}
	if p[1] == "d" {
		e.Op = LogDelete
	} else {
		e.Op = LogUpsert
	}
	if e.HasOld, err = strconv.ParseBool(p[3]); err != nil {
		return LogEntry{}, err
	}
	if e.HasNew, err = strconv.ParseBool(p[5]); err != nil {
		return LogEntry{}, err
	}
	return e, nil
}
