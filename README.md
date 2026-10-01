# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 容器镜像制品回收器

`registry.Reclaimer` 管理层、清单、索引和标签，保留期单位为毫秒。

### 活死定义

- 被标签直接指向的清单或索引是活对象。
- 活索引引用的清单是活对象。
- 活清单引用的层是活对象。
- 不满足上述任一可达条件的对象是死对象。

每次 `PutLayer`、`PutManifest`、`PutIndex`、`Tag`、`Untag` 成功后，都从全部标签根全量重算活死。活清单和活索引可以引用不存在摘要的情况不会发生，因为创建时已校验引用。

### `deadSince` 语义

- 新建且不可达的对象，其 `deadSince` 为本次操作的 `now`。
- 对象由活变死时，`deadSince` 记为本次操作的 `now`。
- 对象由死变活时清除 `deadSince`；之后再次失活，会从新的失活时刻重新起算。
- 已死且仍为死的对象保持原 `deadSince` 不变。
- 同摘要、同字节数的重复 `PutLayer` 是幂等成功操作，只推进最大 `now`，不改变对象、活死状态或 `deadSince`。

因此，被一个活清单和一个死清单共享的层，只有在最后一个活引用者失活时才开始计算保留期，而不是从死清单创建时起算。

### GC

`GC(now)` 删除同时满足以下条件的对象：

- 当前为死对象。
- `now-deadSince >= R`，边界值等于 `R` 时立即回收。
- 不存在任何仍存在的清单或索引引用它，引用者本身为死也会阻止删除。

删除顺序固定为：

1. 全部索引。
2. 全部清单。
3. 全部层。

每个类别内部按摘要字节序升序。前一阶段的删除会立即影响后续引用判断，所以一次 `GC` 可以从死索引级联到死清单，再级联到不再被引用的层。返回值严格按删除先后排列。

### 错误优先级

所有带 `now` 的成功操作都会推进已接受的最大 `now`；若 `now` 小于该值，首先返回时钟回拨错误。被拒绝的操作不会改变对象、标签、`deadSince` 或最大 `now`。

时钟检查之后按以下顺序只返回第一个错误：

- `PutLayer`：摘要为空；字节数不大于 0；同摘要已存在但种类或字节数不同。
- `PutManifest` / `PutIndex`：摘要为空；摘要已存在；引用列表为空；列表含重复；按列表序第一个引用摘要不存在；按列表序第一个引用种类不符。
- `Tag`：名字为空；目标不存在；目标是层。
- `Untag`：名字为空；标签不存在。
- 构造器：保留期 `R < 0`。

并发调用通过单一互斥保护，结果等价于某个串行顺序；固定操作序列和时间戳会得到相同的删除序列。

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

### 回收器验证

```bash
# 固定场景、错误优先级、并发与 2000 组随机朴素对拍
go test -v ./registry

# 竞态检测
go test -race ./registry

# 2000 组对拍会逐条打印输入、输出与判定依据
go test -v ./registry -run TestRandomOperationsAgainstNaiveModel
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
