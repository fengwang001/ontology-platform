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

## 哈希连接运行时过滤器（`runtimefilter`）

包路径：`runtimefilter`。构建侧 N 个分片并行产出连接键的取值摘要，
协调器在 N 片全部报告后把不可变的过滤器版本下发给一个或多个并发的探测侧
扫描，提前丢弃不可能匹配的行。过滤器只影响性能，不改变任何连接的结果。

### 摘要合并与降级规则

- 每片报告 `Min/Max/Distinct`，或报告 `Abandon`；空分片报告 `Empty: true`
  （不参与最小/最大值与集合合并）。
- 只有 N 片全部被接受后过滤器才就绪：最小值取各片最小，最大值取各片最大，
  去重集合取并集。
- 合并后的去重键数 `len(union) > MaxDistinct` 时**降级**：丢弃去重集合，
  只保留最小/最大值区间（区间内放行，可能带来少量假阳性，结果仍然正确）。
- 任一分片报告放弃，过滤器整体**作废**，探测侧全部放行。
- 所有分片为空时构建侧为空：内连接/半连接的探测行（含空键）全部丢弃。

### 各连接类型可否过滤的推导

判据是“探测侧是否为保留侧”：被预过滤丢弃的行若仍可能以未匹配扩展行出现
在结果中，过滤就不安全。

| 连接类型 | 探测侧角色 | 是否过滤 |
| --- | --- | --- |
| 内连接 inner | 非保留侧 | 是 |
| 半连接 semi | 非保留侧 | 是 |
| 左外连接（探测侧在左） | 保留侧 | 否 |
| 右外连接（探测侧在左） | 非保留侧，但语义随构建/探测朝向而变 | 否（规则只开放内连接/半连接） |
| 全外连接 full outer | 保留侧 | 否 |
| 反连接 anti | 保留侧（未匹配行才输出） | 否 |

实现上 `Filterable(jt)` 仅对内连接与半连接返回 true；所有外连接与反连接一律
放行，避免对连接朝向做隐式假设。空键在任何连接类型下都不可能与构建侧匹配；
仅在允许过滤的连接中就绪后被丢弃。

### 就绪、超时与版本语义

- 就绪前的批原样放行；就绪后按当前过滤器判定。
- 每批调用最多等待 `ReadyWait`（也受批次 deadline 约束，零值 deadline 不设
  额外期限）。超时后**当前批放行**；之后晚到的过滤器只作用于其后的批。
- 每一批在开始时取得一个完整的不可变过滤器快照（`version` 单调递增），
  绝不会看到合并了一半的摘要。
- 分片报告与多个扫描的批调用可并发；每个扫描器维护
  `Scanned == Passed + Dropped` 恒等式。

### 非法报告

三类错误以可区分的哨兵错误返回，且**被拒绝的报告不改变任何状态**：

- `ErrUnknownJoin`：连接类型未知（构造协调器时拒绝）。
- `ErrShardOutOfRange`：分片编号 `< 0` 或 `>= ShardCount`。
- `ErrDuplicateShard`：同一分片重复报告（即使内容是“放弃”也无效）。

### 日志

注入 `Logger` 后，每次分片报告、过滤器就绪/降级/作废、每行的
`decision=pass/drop` 与依据（`null-key`、`not-in-summary`、
`in-summary version=N`、`not-ready-or-timeout`、`filter-not-allowed(...)`）
以及每批的输入/输出计数都会打印。

### 本地验证

```bash
# 全量测试 + 竞态检测（覆盖左外/反连接不过滤、空键、构建侧为空、
# 去重超上限降级、分片放弃、超时后晚到、非法报告、并发对拍与确定性）
go test -race -v ./runtimefilter

# 可运行示例（含就绪前放行与就绪后过滤）
go test -run Example -v ./runtimefilter

go vet ./... && gofmt -l .
```
