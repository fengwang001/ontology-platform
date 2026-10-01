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

## 块级增量备份链管理器（`backup` 包）

`backup.Manager` 管理一个由定长块组成的卷及其备份链。链首为全量备份，
其余为增量备份，父为链上紧邻的前一个备份。所有方法可并发调用。

### 增量判定

- 链为空时 `Backup()` 创建全量备份，存卷的全部块；否则创建增量备份。
- 增量备份逐块比较卷当前内容与**父备份的还原结果**，只存不同的块；
  写过又写回原值的块与父还原结果相同，因此不存。
- 备份是时间点一致的：整个快照在同一把互斥锁内完成，
  并发写入要么全部落在该备份内、要么全部不在。

### 还原回溯

还原某备份的第 `i` 块时，从该备份沿父指针回溯到链首，
取遇到的第一份内容；链首是全量，保证必然命中。
`OpenRestore` 打开的还原会话在关闭前受保护（见删除约束），
会话内读取结果与打开时一致。

### 合并与剔除（删除中间备份）

删除中间备份时：

1. 其存储的块并入直接后继，后继已有的块以后继为准；
2. 后继的父指针改指被删备份的父；
3. 并入后逐块剔除后继中与**新父还原结果**相同的块。

合并只迁移或剔除块，全链存储块总数不增，因此删除不会触发存储上限。
删除前后，每个保留备份的还原结果逐字节不变。

### 删除约束

- 删除链尾：直接丢弃，不影响其他备份。
- 删除全量（链首）：后继变为全量，保留并入后的全部块，不再剔除。
- 目标备份处于某个未关闭还原会话的回溯路径（会话目标到链首）上时，
  删除整体拒绝，返回 `ErrDeleteBlockedByRestore`。

### 可区分的拒绝原因

- `ErrBlockOutOfRange`：块号越界或写入数据长度与块长不符；
- `ErrBackupNotFound`：指定的备份不存在；
- `ErrStorageLimitExceeded`：备份将使全链存储块总数超过上限；
- `ErrDeleteBlockedByRestore`：删除被未关闭的还原会话阻塞。

被拒绝的操作整体回滚，不改变任何备份与卷。

### 本地验证

```bash
# 全部测试（含竞态检测）
go test -race ./backup/

# 详细日志：每个用例打印输入、输出与判定依据
go test -race -v ./backup/

# 单个用例，例如“写回原值不入增量”
go test -v -run TestWriteBackToOriginalNotStored ./backup/
```
