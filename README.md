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

## routing 包：距离向量路由表

`routing` 包实现 RIP 风格的距离向量路由表。前缀为非空字符串，链路代价为 1，
参数为路由超时 `T` 与垃圾保持时间 `G`。度量恒不超过 16，16 一律表示不可达。
所有操作通过互斥锁保证并发安全；时间由调用方显式传入，相同操作序列重放结果完全相同。

### 接受规则（`Receive(now, n, p, m)`）

- 新度量 `c = min(m+1, 16)`。
- 无表项：仅 `c < 16` 才建表项（下一跳 `n`，更新时刻为现在）。
- 有表项且 `n` 是当前下一跳：总是接受 `c`（含变大变小）并刷新更新时刻；
  `c = 16` 而原本不是垃圾则转垃圾、垃圾起点为现在，原本已是垃圾则起点不变；
  `c < 16` 则垃圾项恢复为有效。
- 有表项且 `n` 不是当前下一跳：仅当 `c` 严格小于现有度量才改下一跳为 `n` 并刷新，
  等值不替换；垃圾项被接受 `c < 16` 后恢复为有效。
- 拒绝（按此顺序只报第一个，且不改变表项与更新时刻）：
  时钟回拨（对所有带时刻的操作）、邻居或前缀为空、通告度量超出 0 至 16。

### 整理规则（`Sweep(now)`）

- 有效表项更新时刻加 `T` 不晚于现在则转垃圾，度量置 16，
  垃圾起点为「更新时刻加 `T`」而非现在。
- 垃圾起点加 `G` 不晚于现在则删除；一次整理可对同一表项连续完成两步。

### 查询与通告规则

- `NextHop(p)` 只返回度量小于 16 的有效表项。
- `Advertisements(x)` 为邻居 `x` 生成通告：每个未删除表项一条，
  下一跳是 `x` 或表项为垃圾则度量为 16（水平分割加毒性逆转），
  否则为表项度量，按前缀升序。

### 本地验证

```bash
# 运行 routing 包全部测试（日志打印输入、输出与判定依据）
go test -v ./routing

# 并发安全（竞态检测）
go test -race ./routing
```
