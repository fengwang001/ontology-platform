# indexstore

带唯一二级索引的主表存储 + 崩溃后从水位分批追赶的索引恢复服务。
详细设计见 [DESIGN.md](DESIGN.md)。

## 快速开始

```go
d := indexstore.NewDisk()
s, err := indexstore.Open(d, indexstore.Options{})

lsn, err := s.Put("alice", "email:a@x", true)   // 写入（hasSec=true）
s.Put("alice", "", false)                       // 置空二级键
s.Delete("bob")                                  // 删除
row, ok, err := s.Get("alice")                   // 按主键读，永不受追赶影响

reached, done, err := s.CatchUp(100)             // 每批最多 100 条，返回已到 LSN
pk, found, err := s.Lookup("email:a@x")          // 追赶完成前返回 index-stale
diffs, err := s.Verify()                         // 自检：extra/missing/wrong-owner
```

错误按优先级区分：`invalid-argument`、`log-gap`、`index-stale`、
`primary-not-found`、`unique-conflict`（`*indexstore.Error` 的 `Code`）。

## 崩溃与重启

`Options.CrashHook(step)` 在每个持久化提交步骤前被调用，返回 `true`
即模拟进程崩溃（当前步骤整体丢失，此前提交保留）。用同一 `Disk`
再次 `Open` 即重启，随后继续 `CatchUp`。也可用 `indexstore.Run`
自动把注入崩溃转成 `crash=true`。

## 操作日志

`s.OpLog()` 返回每条对外操作的输入、输出、错误类别与判定依据
（例如冲突时记录“`sec=x owned by pk=y in table`”），可渲染为文本，
用于随机测试对照与问题复现。

## 测试

```bash
go test -race -v ./ontology/store/indexstore/
```

- 场景：键改走后被他人占用、同键多主键链式更迭再释放、删除后重写、
  置空/重设、被拒绝写入不留痕。
- 恢复：前缀长度与水位位置全组合、每个批边界/每条提交点崩溃、
  缺口在每个位置、追赶期间的冲突与查询、追赶中写入不遗漏不重复。
- 差分：400 个随机种子对照独立朴素模型；确定性重放逐字节一致。
- 基准：冲突判定不随落后条数增长、单条重放不随索引规模增长。

演示：

```bash
go run ./ontology/store/indexstore/cmd/demo
```
