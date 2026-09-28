# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 幂等生产者序号校验（`idempotency` 包）

`idempotency.Validator` 在服务端按（生产者、世代、分区、序号）判定写请求，
保证同一序号在同一分区至多落盘一次，且接受后的序号按位点连续。
判定结果（`append` / `duplicate` / `epoch-bump` / `reject`）连同请求与
判定依据通过 `slog` 逐条打印。

### 四步判定顺序

1. **合法性**：生产者 ID 为空、世代 / 分区 / 序号为负、负载为 nil，
   整体拒绝（`ErrInvalidRequest`）。
2. **世代围栏**：请求世代低于服务端记录的当前世代，拒绝
   （`ErrProducerFenced`）；高于当前世代则视为世代升级——升级仅在
   请求被接受时提交，提交时清空该生产者全部分区的序号状态与窗口。
3. **首条约束**：新世代或新分区的首条记录序号必须为零，否则拒绝
   （`ErrOutOfOrderSequence`）。
4. **序号判定**：序号等于最后序号加一则追加并返回新位点；命中序号
   窗口则判重复，返回原分配位点且不重复落盘；序号不大于最后序号但
   已滑出窗口，拒绝（`ErrDuplicateSequenceStale`）；序号大于最后序号
   加一（跳号），拒绝（`ErrOutOfOrderSequence`）。

### 序号窗口

每个（生产者, 分区）保留最近 `windowSize` 条已接受记录的
「序号 → 位点」映射。窗口内的重复请求返回原位点并确认成功；
滑出窗口的旧序号无法与乱序区分，统一按 `ErrDuplicateSequenceStale`
拒绝。

### 边界与错误类别

- 四类错误互不相同、可用 `errors.Is` 区分：`ErrInvalidRequest`、
  `ErrProducerFenced`、`ErrOutOfOrderSequence`、`ErrDuplicateSequenceStale`。
- 重复确认与任何拒绝都不改变日志、当前世代、序号状态与窗口
  （失败不留痕）；世代升级只在首条记录被接受时生效。
- 每个分区的位点独立、从零开始连续递增；世代升级后序号与位点
  重新从零计数。
- `Validator` 可并发使用，内部以互斥锁串行化判定与追加。

### 本地验证

```bash
# 场景测试 + 并发测试 + 朴素参照一致性（含竞态检测）
go test -race -v ./idempotency/
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
