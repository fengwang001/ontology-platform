# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 主键变更拆分与分区投递组件（cdc 包）

`cdc.Pipeline` 把源表的一批变更拆分为下游可识别的事件，按主键哈希
分区投递，保证下游视图与源表始终一致。

### 拆分规则

- 插入（`Insert`）映射为一条写入（`OpWrite`）。
- 删除（`Delete`）映射为一条删除（`OpDelete`）。
- 主键不变的更新（`Update` 且 `OldKey` 为空或等于 `Key`）只产生一次写入。
- 主键变化的更新（`OldKey != Key`）先删除旧键、再写入新键，两条事件相邻有序。

### 合并规则

一批变更先逐条校验（在源表副本上预演，支持批内链式换键），再整体拆分。
合并时同一个主键只保留它在拆分序列中的最后一条事件，其余丢弃；
保留下来的事件维持原先后顺序，各分区按拆分序号有序输出。

### 分区规则

分区下标 = `FNV-1a(主键) % 分区数`。哈希函数与分区数固定，因此同一
主键永远落入同一分区，同一输入序列反复计算得到完全相同的输出。

### 批校验与拒绝

以下情况整批拒绝，返回 `*cdc.RejectError`（可用 `errors.Is` 区分原因），
且源表、下游视图与已产出事件均不发生任何变化：

- `ErrInvalidPrimaryKey`：主键为空或超过长度上限（128 字节）。
- `ErrKeyAlreadyExists`：插入的键已存在，或主键变更后的新键已存在。
- `ErrKeyNotFound`：更新/删除的键（或旧主键）不存在。
- `ErrTooManyRows`：批内行数超过 `maxBatchRows` 上限。

### 并发与一致性

所有状态由读写锁保护：`Apply` 持写锁完成「校验-拆分-合并-分区-提交」
全过程，`Snapshot` 持读锁返回深拷贝快照。任意并发度下读到的下游视图
都与源表快照相等。

### 本地验证

```bash
# 全部测试（含竞态检测）
go test -race ./cdc/

# 查看输入、拆分、分区输出及判定依据日志
go test -race -v ./cdc/
```

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
