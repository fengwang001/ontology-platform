# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## recovery 包：ARIES 风格回滚与崩溃重启撤销模型

`recovery/` 实现了一个 ARIES 风格的事务回滚与崩溃重启撤销（undo）阶段模型。
日志容量上限为 `Lmax`（1 到 10^6），LSN 从 1 起每追加一条记录加一。
记录类型：`U`（更新）、`C`（补偿记录 CLR）、`K`（提交）、`E`（结束）。

### prevLSN 与 undoNext

- `prevLSN`：追加记录时该事务的 `lastLSN`（该事务最近一条记录的 LSN，无则 0），
  把同一事务的记录串成一条链。
- `undoNext`：仅补偿记录携带，等于被它撤销的那条 `U` 的 `prevLSN`，
  指向撤销进度应继续的位置；`undoNext` 严格小于该 C 自己的 LSN。

### next(t) 与跳转规则

`next(t)` 定义为：若 `lastLSN` 指向一条 C，取该 C 的 `undoNext`，否则取 `lastLSN`。
回滚（`Rollback(t, s)`）从 `q = next(t)` 起循环：`q <= s` 则停；
`q` 是 `U` 时追加一条 C（增量取反、`undoNext` 为该 U 的 `prevLSN`）并把相反数加到页上，
`q` 前移为该 U 的 `prevLSN`；`q` 是 C 时只沿 `undoNext` 跳转、不写记录——
已补偿区间永远不会被再次撤销。`Save(t)` 返回当前 `lastLSN` 作为保存点（可为 0），
保存点恰等于某条记录的 LSN 时该记录不被撤销。`Abort(t)` 等于 `Rollback(t,0)` 后追加 `E`。

### Restart 的全局次序与中断续做

`Crash()` 使所有活跃事务成为败者。撤销阶段：

1. 先按事务号升序为 `next` 已为 0 且尚无 `E` 的败者各追加一条 `E`；
2. 循环在 `next > 0` 的败者中取 `next` 最大者处理（跨事务按 LSN 从大到小），
   规则同上（U 写 C 并前移，C 只跳转）；任一败者 `next` 归零时立即为其追加 `E`
   （`E` 紧跟使它归零的那条 C；若归零来自跳转，则就在跳转之时追加）。

`RestartStep(n)` 每追加 n 条记录（C 或 E）即返回，状态在引擎内保留；
任意拆分多次 `RestartStep` 再以 `Restart` 收尾，所得日志与一次 `Restart` 逐条相同。
使 `next` 归零的那条 C 之后的 `E` 即使跨调用，也必须先于任何其它记录追加。
`Restart`/`RestartStep` 不受 `Lmax` 限制。

### 本地验证

```bash
# 全部测试（含题目示例的精确 LSN 序列、RestartStep 全断点续做对照、
# 以及与朴素模拟对照的 2000 组随机操作序列）
go test ./recovery/

# 打印随机测试的输入、输出与判定依据
go test -v -run TestRandomSequencesAgainstNaiveModel ./recovery/

# 竞态检测
go test -race ./recovery/
```
