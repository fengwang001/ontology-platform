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

## 多分区拉取响应组装器

实现位于 `ontology/assembler.go`，使用泛型参数 `K` 表示载荷标识类型。

- 构造：`New[K](N, W, P, logger)` 要求分区数 `N >= 1`、总字节预算 `W >= 1`、单分区预算 `P >= 1`；非法参数分别返回 `ErrInvalidPartitions`、`ErrInvalidTotalBudget`、`ErrInvalidPartitionBudget`。
- 追加：`Append(partition, size, payload)` 返回该分区从 0 起递增的位点；分区越界返回 `ErrPartitionOutOfRange`，`size < 1` 返回 `ErrInvalidMessageSize`，两类错误同时成立时优先报分区越界。
- 查询：`Snapshot()` 返回当前轮转起点、各分区消费位点、剩余条数和剩余字节。
- 拉取：`Fetch()` 从当前轮转起点开始，按环形顺序对每个分区访问一次；每个分区从其消费位点开始连续读取。
- 预算判定：取某条消息前，若“本分区本次已取字节 + 当前消息大小 > P”或“本次响应已取总字节 + 当前消息大小 > W”，立即停止该分区并保留该消息；恰等号可以取。
- 不跳过消息：某个分区一旦在当前消息处停止，不会跳过它去读取后面的更小消息，因此分区内位点始终连续。
- 首条例外：仅当本次响应总字节仍为 0 时，无条件取当前消息一条；它可能超过 `P` 或 `W`，之后所有消息仍按普通预算规则判定。
- 轮转推进：成功拉取后，消费位点推进到已取消息之后；下次起点是本次最后一个取到消息的分区的下一个分区，按 `N` 取模回绕。
- 无数据：所有分区在访问时都没有消息时返回 `ErrNoData`，且不改变任何内容、消费位点或轮转起点；被拒绝的追加同样保持状态不变。
- 并发性：追加、拉取和查询由同一把互斥锁保护，结果等价于某个合法的串行顺序；每条消息最多被取走一次。
- 可复现日志：日志包含构造、追加、查询的输入与输出；拉取日志记录起始分区、每条消息的取/不取依据、字节累计、停止原因和下次起点。

### 本地验证

```bash
go test -v ./...
go test -race -v ./...
go test -cover ./...
```

如果当前 shell 找不到 `go`，可使用安装路径例如 `/usr/local/go/bin/go`；若默认构建缓存不可写，可设置 `GOCACHE=/tmp/ontology-go-cache`。
