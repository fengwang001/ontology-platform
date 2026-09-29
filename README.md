# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 事务消息分区日志（txnlog）

`txnlog` 包实现事务消息分区日志的稳定位点（stable offset）与已提交读
（read committed），语义对齐 Kafka 事务分区的 LSO 模型。

### 事务归属

- 生产者须先 `RegisterProducer` 注册，才能追加记录。
- 每条记录（数据或控制标记）追加时获得从 0 开始的连续位点。
- 某生产者的一个事务由其上一个标记之后写入的全部数据记录组成；
  首条数据记录的位点即事务首位点，下一条标记（提交/中止）结束该事务。
- 同一生产者同时至多一个进行中的事务；无数据即写标记会被拒绝。

### 高水位与稳定位点

- 高水位（HW）只进不退，且不超过日志末尾；HW 之前的记录对消费者可见。
- 稳定位点 = min(HW, 所有首位点低于 HW 且尚无可见结论的事务首位点)。
  标记位点尚未被 HW 覆盖时，其结论不可见，事务仍卡住稳定位点。
- 稳定位点只进不退。

### 已提交读

- `ReadCommitted(from, maxCount)` 返回位点在 `[from, 稳定位点)` 内、
  所属事务以提交结束的数据记录，按位点升序，结果可复现。
- 绝不返回控制标记，也绝不返回未决或已中止事务的数据。

### 边界与错误类别

所有非法输入整体拒绝且不留痕（日志、事务表、HW、稳定位点均不变），
错误可用 `errors.Is` 两两区分：

- `ErrUnknownProducer`：空标识或未注册的生产者写数据/标记、注册空标识。
- `ErrNoOngoingTransaction`：生产者无进行中事务却写标记。
- `ErrInvalidHighWatermark`：HW 为负、倒退或越过日志末尾。
- `ErrInvalidReadStart`：读取起点为负或越过当前稳定位点。

### 并发

追加、推进高水位与读取均可被多个执行体并发调用，内部以互斥锁串行化，
先校验后变更，保证失败无副作用。

### 本地验证

```bash
go test -race -v ./txnlog/          # 含逐步日志：每步输入、稳定位点与判定依据
go test -race -count=3 ./txnlog/    # 重复执行验证稳定性
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
