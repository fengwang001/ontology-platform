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

## timelog：按时间戳查位点

`timelog` 包在时间戳不保证单调的只追加日志上，提供按给定时间定位
重读位点的能力，追加与查询均可被多个执行体并发调用。

### 时间索引的建立

- 追加消息时，仅当其时间戳**严格大于**此前所有消息的最大值，
  才把该消息的位点记入时间索引；相等或回落均不记录。
- 因此索引中的时间戳严格递增，项数只与"新高"次数有关，
  乱序、重复时间戳不会使索引膨胀。

### 查询语义

- `Query(t)` 返回位点最小的、时间戳不小于 `t` 的消息位点；
  日志中无满足条件者时返回日志结束位点且 `found=false`。
- 首个满足 `ts >= t` 的消息必为索引项（若位点 p 未入索引，
  则存在 q < p 使 `ts[q] >= ts[p] >= t`，与 p 最小矛盾），
  故查询在索引上二分定位，不做逐条扫描，结果与朴素扫描一致且可复现。
- 查询结果对目标时间单调不减；只追加日志的既有前缀不变，
  已存在的首个命中不因后续追加而改变。

### 边界与错误类别

- 空日志：任意合法 `t` 均返回结束位点 0 且 `found=false`。
- `t` 超过所有时间戳：返回结束位点且 `found=false`。
- 拒绝原因互不相同、可用 `errors.Is` 区分，且任何一次被拒都
  整体失败、不留痕（结束位点、索引、已有消息均不变）：
  - `ErrNegativeTimestamp`：追加或查询使用负时间戳；
  - `ErrEmptyPayload`：追加的消息载荷为空；
  - `ErrCapacityExceeded`：追加会超出构造时指定的容量上限。

### 本地验证

```bash
# 运行 timelog 全部测试（含竞态检测与每步输入/位点/判定依据日志）
go test -race -v ./timelog

# 仅查看与朴素扫描的一致性对照
go test -run TestMatchesNaiveScan -v ./timelog
```
