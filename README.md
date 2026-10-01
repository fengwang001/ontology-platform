# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## fetch：多分区拉取响应组装器

`fetch` 包（`fetch/assembler.go`）在总字节预算 `W` 与单分区字节上限 `P` 下，
按轮转起点从 `N` 个分区环形读出消息并组装一次拉取响应。

### 预算判定

- 拉取从轮转起点分区（初始为 0 号）起，按环形顺序把全部 N 个分区各访问一次。
- 访问分区时从其消费位置起逐条取，取一条前先判定：
  - 本分区本次已取字节 + 该条大小 > `P`，或本响应已取总字节 + 该条大小 > `W`，
    则该条不取且本分区结束，不得跳过它去取其后更小的消息；
  - 恰等于 `P` 或 `W` 可取。
- 分区读到末尾也结束；某分区因总预算不足而结束，不影响继续访问其后的分区。

### 首条例外

若本响应已取总字节仍为 0，则无视 `P` 与 `W` 取下一条（只此一条），其后照常判定。
这保证首条超限消息也能返回，避免大消息饿死。

### 轮转起点

拉取成功后消费位置推进到取走之后；下次轮转起点为本次最后一个取到消息的分区的
下一个分区（环形）。被拒绝的操作（参数非法、追加越界、无数据）不改变分区内容、
消费位置与轮转起点。追加、拉取与查询（`Snapshot`）均可并发调用，结果等价于某个
串行顺序；相同调用序列重放得到完全相同的响应序列与轮转起点。

### 本地验证

```bash
# 规则覆盖测试（恰等 P/W、首条例外、受阻不跳过、预算不足继续、轮转回绕、无数据）
go test ./fetch -v

# 与逐条朴素模拟的随机对照（日志打印输入、输出与判定依据）
go test ./fetch -run TestAgainstNaiveSimulation -v

# 并发等价串行校验（竞态检测）
go test ./fetch -run TestConcurrency -race -v
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
