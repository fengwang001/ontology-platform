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

## 分周期配额结转账本（`quota` 包）

第 k 个周期为半开区间 `[t0+kP, t0+(k+1)P)`（k 从 0 起），创建参数为
起点 `t0`、周期长度 `P`、每周期基础额度 `Q`、结转上限 `M`。

### 摊入公式

用量记录带标识、半开区间 `[s,e)` 与总量 `amount`，按与各周期的重叠
长度比例摊入：

- 周期 k 分得 `⌊amount × overlap(k) ÷ (e−s)⌋`，其中 `overlap(k)` 为
  区间与周期 k 的重叠长度；
- 余数 `amount − Σ各周期整除部分` 全部加到区间所触及的最后一个周期；
- 各周期摊入量之和恒等于 `amount`（总量守恒）。

### 结转与超额

- 周期 k 的有效额度 `E(k) = Q + C(k)`，其中 `C(0) = 0`，
  `C(k) = min(M, max(0, E(k−1) − U(k−1)))`，`U` 为该周期摊入总用量；
- 用量超过有效额度的部分为超额量 `max(0, U(k) − E(k))`，只如实报告，
  不拒绝记录，也不产生负结转（未用额度不足 0 按 0 计）。

### 查询语义

查询按当前全部记录现算，因此结果与记录到达顺序无关；晚到的记录会
改变此前周期的结果并顺延影响之后的结转。记录与查询均可并发调用。
非法参数/记录整体拒绝并返回可区分的错误（见 `quota/ledger.go` 中的
`Err*` 变量），被拒绝的操作不改变任何账目。

### 本地验证

```bash
go test -race -v ./quota/   # 日志含每个用例的输入、输出与判定依据
```
