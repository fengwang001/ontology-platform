# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 节点内存超卖准入与压力驱逐器（`node` 包）

`node.Node` 在单节点上维护容器内存账目：可分配内存 `A`、整数超卖倍数 `O`，
上限总容量为 `O×A`。所有操作（准入 / 上报 / 删除 / 查询）在同一把互斥锁下串行化，
任意时刻请求之和不超过 `A`、上限之和不超过 `O×A`，每次上报处理完后用量之和不超过 `A`。

容器按请求 `r` 与上限 `l` 分三类：`r==l` 保证型、`0<r<l` 突发型、`r==0` 尽力型。
新容器当前用量初始为 0。

### 两类准入条件

`Admit(id, r, l)` 当且仅当以下两条同时成立才准入，否则整体拒绝且账目不变：

1. **请求条件**：在册请求之和 + `r` ≤ `A`，不足时返回 `ErrRequestExhausted`；
2. **上限条件**：在册上限之和 + `l` ≤ `O×A`，超限返回 `ErrLimitOversold`。

两条同时不成立时**先报请求不足**。其余非法输入另有可区分原因：
`A<=0` → `ErrInvalidAllocatable`、`O<1` → `ErrInvalidOversell`、
`r<0` → `ErrNegativeRequest`、`l<=0` 或 `l<r` → `ErrInvalidLimit`、
标识重复 → `ErrDuplicateID`。

### 上报与驱逐次序四个键

`Report(id, u)` 要求容器在册且 `0 ≤ u ≤ l`，否则返回
`ErrContainerNotFound` / `ErrInvalidUsage` 且不改账。记账后若全部在册用量之和严格大于 `A`，
则在驱逐开始时一次性固定全部在册容器的次序，随后按序逐个驱逐，直到用量之和 ≤ `A`。
排序键依次为：

1. `u > r` 者先于 `u <= r` 者（`u` 恰等于 `r` 不算超出）；
2. 尽力型先于突发型先于保证型；
3. `u - r` 大者先；
4. 标识升序（保证相同操作序列重放得到完全相同的驱逐序列）。

### 达标即停

按固定次序逐个驱逐，**一旦用量之和 ≤ `A` 立即停止，不多驱逐**；
被驱逐者的请求、上限、用量即刻退出账目，腾出的空间可立即重新准入（同标识也可）。
`Report` 返回被驱逐标识序列及每个容器的四键依据（`Eviction` 结构）。
`Delete(id)` 删除在册容器并即刻退出账目，删除不存在的容器返回 `ErrContainerNotFound`。

判定过程通过日志输出输入、输出与判定依据（默认 stderr，可用 `node.WithLogWriter` 重定向）。

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

`node` 包本地验证（若 `go` 不在 PATH，先 `export PATH=$PATH:/usr/local/go/bin`；
只读环境下可 `export GOCACHE=/tmp/gocache`）：

```bash
# 竞态检测 + 详细用例
go test -race -v ./node

# 覆盖率
go test -cover ./node
```

测试覆盖：恰好装满的准入边界（`sumR==A`、`sumL==O*A`）、上限超卖边界与
“先报请求不足”、`u==r` 不算超出、`sumU==A` 不驱逐、保证型最后被驱逐、
并列按标识升序、第三键 `u-r`、恰驱逐到达标即停、驱逐后腾出准入空间、
非法操作可区分拒绝且不改账、重放确定性与并发竞态安全。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
