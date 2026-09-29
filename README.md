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

## 窗口函数取前驱值（LAG）增量维护

`ontology/lag` 包对按分区排序的行变更流增量维护每行的前驱值（LAG），
并输出确定顺序的变更日志。

- 排序：不同分区互不影响；同分区按 `SortKey` 升序、并列按 `ID` 升序，
  与插入顺序无关、可复现。
- 前驱值：同分区紧邻其前一行的 `Value`；分区首行前驱为空，`HasPrev=false`，
  与前驱取值为零值字符串 `""`（`HasPrev=true`）严格区分。
- 变更日志：插入先输出新行 `insert`（带前驱值），再按需输出紧邻后继的
  `update`；删除先输出被删行 `delete`，再按需修正后继；前驱值未变的行
  不输出任何条目；同一次输入的多条输出顺序固定。
- 下游 `lag.Materializer` 按序应用日志后的视图，与引擎视图及
  `lag.Recompute` 批量重算结果逐分区、逐标识一致。

错误类别（互不相同、可区分，均整体拒绝且失败不留痕）：

| Reason | 触发条件 |
| --- | --- |
| `empty_partition` | 分区名为空字符串 |
| `empty_id` | 行标识为空字符串 |
| `duplicate_id` | 插入的标识在该分区已存在 |
| `missing_id` | 删除的标识在该分区不存在 |
| `row_limit_exceeded` | 插入后行数超过上限（`WithMaxRows`，默认 1,000,000） |

并发：提交（`Insert`/`Delete`）与视图/自检（`View`/`Snapshot`/`Verify`）
通过读写锁隔离，可被多个执行体并发调用；`go test -race` 可验证。

本地验证：

```bash
# 本包全部用例（含逐步输入/输出/判定日志，-v 查看）
go test -race -v ./ontology/lag

# 仅并发用例并重复多次
go test -race -count=10 -run TestConcurrent ./ontology/lag

# 全量测试、格式化与静态检查
go test ./...
gofmt -l .
go vet ./...
```
