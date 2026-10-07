# 使用指南

## 纯事件重放

```go
events := []snapshot.Event{
    {Type: snapshot.EventPrepare, TxID: "tx1", Record: &snapshot.Record{
        Kind: snapshot.KindObject, Operation: snapshot.OpUpsert, ID: "o1",
    }},
    {Type: snapshot.EventCommit, TxID: "tx1", CommitLSN: 1},
}

frames, err := snapshot.NewExporter(events).Export(1, snapshot.NoLimits)
```

边界 `1` 使用左闭规则，因此 `tx1` 在快照内；请求自动选择最新边界使用 `snapshot.AutoBoundary`。

## 在线并发写入与读取

```go
coordinator := snapshot.NewCoordinator()
_ = coordinator.Begin("tx1",
    snapshot.Record{Kind: snapshot.KindObject, Operation: snapshot.OpUpsert, ID: "o1"},
    snapshot.Record{Kind: snapshot.KindLink, Operation: snapshot.OpUpsert, ID: "l1", SourceID: "o1", TargetID: "o1"},
)
lsn, err := coordinator.Commit("tx1")

stream := coordinator.Export(snapshot.AutoBoundary, snapshot.NoLimits)
snapshotFrame := <-stream
_ = lsn
_ = err
```

流中的第一帧是快照；之后每个帧携带一条事务增量。同一个事务的所有记录在同一个 `Delta` 中，不会拆成多个可见前缀。

## 错误处理

将错误断言为 `*snapshot.ExportError` 后读取：

- `Class`：四类固定错误之一；
- `Rule`：稳定规则名；
- `CommitLSN`、`TxID`、`RecordID`：尽可能指向冲突位置。

收到错误后，已读到的帧仍然有效；组件不会再继续输出错误位点之后的内容。
