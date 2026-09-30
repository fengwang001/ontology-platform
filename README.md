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

## 距离向量路由表（`dv` 包）

`dv` 包实现 RIP 风格的距离向量路由表：前缀为非空字符串，链路代价恒为 1，
度量上限为 16，**16 一律表示不可达**。构造参数为路由超时 `T` 与垃圾保持时间 `G`。
所有携带时刻的操作（`Receive`、`Sweep`）的时刻由调用方注入，因此相同操作序列
重放结果完全相同；全部操作（通告、整理、查询、生成）由互斥锁保护，可并发调用，
每个前缀至多一条表项。

### 接受规则（`Receive(now, neighbor, prefix, metric)`）

新度量 `c = min(metric+1, 16)`。

- 无表项：仅 `c < 16` 才建项（下一跳为邻居，更新时刻为现在）。
- 邻居是当前下一跳：总是接受 `c`（含变大变小）并刷新更新时刻；
  `c == 16` 且原本不是垃圾则转垃圾、垃圾起点为现在，原本已是垃圾则起点不变；
  `c < 16` 时垃圾项恢复为有效。
- 邻居不是当前下一跳：仅当 `c` 严格小于现有度量才改下一跳并刷新，等值不替换；
  垃圾项被接受 `c < 16` 后恢复为有效。

### 整理规则（`Sweep(now)`）

一次整理可对同一表项连续完成两步：

1. 有效表项 `更新时刻 + T <= now`：转垃圾，度量置 16，垃圾起点为「更新时刻 + T」而非现在；
2. 垃圾项 `垃圾起点 + G <= now`：删除该表项。

### 通告规则（`Advertise(x)`）

每个未删除表项生成一条通告，按前缀升序。水平分割加毒性逆转：
下一跳是 `x` 或表项处于垃圾期则度量为 16，否则为表项度量。
查询（`Lookup`）只返回度量小于 16 的有效（非垃圾）表项。

### 拒绝规则

按以下顺序只报第一个错误，被拒绝的操作不改变表项与更新时刻：

1. 时钟回拨（所有带时刻的操作，`now` 早于已见过的最大时刻）；
2. 邻居或前缀为空；
3. 通告度量超出 `[0, 16]`。

### 本地验证

```bash
# 全部测试（含竞态检测与输入/输出/判定依据日志）
go test -race -v ./dv

# 静态检查
gofmt -l . && go vet ./...
```
