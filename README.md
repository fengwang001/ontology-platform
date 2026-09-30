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

## 节点内存超卖准入与压力驱逐器（`memory` 包）

节点可分配内存为 `A`，超卖倍数为整数 `O (>= 1)`。容器声明请求 `r` 与上限 `l`
（`0 <= r <= l`，`l > 0`）：`r == l` 为保证型，`0 < r < l` 为突发型，`r == 0` 为尽力型。
新容器用量初始为 0；在册指已准入且未被驱逐或删除。

### 准入条件（两类，同时校验）

1. 请求不超额：在册请求之和 + `r` <= `A`，否则拒绝并返回 `ErrInsufficientRequest`。
2. 上限超卖不超限：在册上限之和 + `l` <= `O x A`，否则拒绝并返回 `ErrOversellLimitExceeded`。

两类同时违反时先报请求不足。参数非法（`r < 0`、`l <= 0` 或 `l < r`、标识重复等）
整体拒绝并返回可区分的错误；任何被拒绝的操作都不改变账目。

### 驱逐次序（四个键，一次驱逐过程中固定）

每次上报用量 `u (0 <= u <= l)` 后，若在册用量之和超过 `A`，按以下次序逐个驱逐：

1. `u > r` 者先于 `u <= r` 者（`u == r` 不算超出）；
2. 尽力型先于突发型先于保证型（保证型最后）；
3. `u - r` 大者先；
4. 标识升序。

### 达标即停

按序逐个驱逐，一旦用量之和降到 `A` 以内（含等于）立即停止，不多驱逐；
被驱逐者的请求与上限即刻退出账目，因此驱逐会腾出后续准入空间。

### 并发与确定性

准入、上报、删除与查询（`Admit` / `Report` / `Delete` / `Snapshot`）均可并发调用，
内部以互斥锁串行化；任意时刻请求之和不超过 `A`、上限之和不超过 `O x A`，
每次上报处理完毕后用量之和不超过 `A`。相同操作序列重放得到完全相同的驱逐序列。

### 本地验证

```bash
# 全部测试（含边界、驱逐次序、达标即停、重放确定性用例，日志打印输入/输出/判定依据）
go test -v ./memory/

# 并发不变量与竞态检测
go test -race -run TestConcurrent -v ./memory/
```
