# 使用说明

## 构造输入

裁决输入是一个只读的 `restore.Snapshot`：

```go
snap := &restore.Snapshot{
    Classes: map[restore.Class]restore.ClassBackup{
        restore.ClassType:   {}, // 正常存放；整体缺失填 {Missing: true}，整体损坏填 {CorruptAll: true}
        restore.ClassObject: {},
        restore.ClassLink:   {},
        restore.ClassAction: {},
    },
    Types: []restore.TypeDef{
        {Key: "person", State: restore.StateIntact},
    },
    Objects: []restore.ObjectInstance{
        {Key: "alice", TypeKey: "person", State: restore.StateIntact},
        {Key: "bob", TypeKey: "person", State: restore.StateCorrupt}, // 部分损坏
    },
    Links: []restore.LinkInstance{
        {Key: "knows", SourceObject: "alice", TargetObject: "bob", State: restore.StateIntact},
    },
    Actions: []restore.ActionRecord{
        {Key: "act1", Objects: []string{"alice"}, Links: nil, State: restore.StateIntact},
    },
}
```

## 一次性裁决

```go
v := restore.New().Adjudicate(snap)

for rid, rv := range v.Records {
    fmt.Println(rid, rv.Recoverable, rv.Detail) // 逐条结论与依据
}
for _, step := range v.Plan { // 唯一确定的重建计划
    if step.Kind == restore.StepBarrier {
        fmt.Println("class completed:", step.CompletedClass.String())
    } else {
        fmt.Println("rebuild:", step.Record.String())
    }
}
for _, e := range v.Errors { // 已按固定优先级排序
    fmt.Println(e.Code, e.Message)
}
_ = v.WriteAuditJSON(os.Stdout) // JSON Lines 审计轨迹
```

## 中途新发现损坏

```go
progress := restore.Progress{Completed: map[restore.RecordID]bool{
    {Class: restore.ClassType, Key: "person"}: true,
    {Class: restore.ClassObject, Key: "alice"}: true,
}}
snap.Objects[1].State = restore.StateCorrupt // 反映新发现后的最新快照
v2 := restore.New().Reassess(v1, snap, progress, restore.DamageReport{
    NewlyCorrupt: []restore.RecordID{{Class: restore.ClassObject, Key: "bob"}},
})
```

已完成记录保持 `ReasonAlreadyCompleted` 不撤销；依赖新损坏点的未完成记录
立即停止并归入优先级4错误。

## 单条记录复核

```go
sess := restore.New().NewCheckSession(snap)
r := sess.Check(restore.RecordID{Class: restore.ClassAction, Key: "act1"})
fmt.Println(r.Recoverable, r.Reason, r.EdgesLooked) // EdgesLooked 即实际查看边数
```

## 错误优先级

| `ErrorCode` | 含义 |
| --- | --- |
| `ErrClassUnavailable` | 某类备份整体缺失/整体损坏 |
| `ErrCascade` | 部分不可用导致的级联不可重建 |
| `ErrCycle` | 循环依赖，重建顺序无法确定 |
| `ErrNewDamage` | 重建中途新发现的损坏 |
