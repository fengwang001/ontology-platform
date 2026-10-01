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

## 值班轮值表（`schedule` 包）

`schedule` 提供带临时覆盖的值班轮值表，核心类型为 `Scheduler`：

```go
s, _ := schedule.NewScheduler([]string{"A", "B", "C"}, T0, L)
s.AddOverride("ov1", "B", s0, e0)   // 添加覆盖 [s0, e0)
member, source := s.Who(t)          // 时刻 t 的当值成员与来源
segs, _ := s.Timeline(a, b)         // [a, b) 合并后的时间线
s.RemoveOverride("ov1")             // 删除覆盖
```

### 轮值与向前外推

时刻 `t` 的轮值成员为 `members[floorMod(floorDiv(t-T0, L), n)]`，其中
`floorDiv` 向下取整、`floorMod` 结果非负。因此 `t < T0` 时按同一公式
向前外推：`t == T0-1` 属于名册最后一个成员，`t == T0-L` 恰为上一班起点。

### 覆盖优先级

- 覆盖 `(id, 成员, [s,e))` 左闭右开，可相互重叠及与轮值重叠。
- 重叠处**最后添加且未被删除**的覆盖生效；删除后其时段恢复为下层
  生效者（更早的覆盖或轮值）。
- 已删除的 id 可再次添加，视为全新的最后添加者。
- 被拒绝的操作（区间为空或颠倒、成员不在名册、id 重复等）不改变
  覆盖集合；校验顺序为区间 → 成员 → id。

### 时间线合并规则

`Timeline(a, b)` 返回 `[a, b)` 内的最大连续段 `(起, 止, 成员, 来源)`，
仅当相邻两段**成员与来源都相同**才合并：

- 名册只有一人时，各班合并为一段（来源同为 `rotation`）。
- 同成员但不同 id 的覆盖不合并。
- 覆盖与轮值即使成员相同也不合并（来源不同）。
- 结果段首尾相接恰好覆盖 `[a, b)`，段数超过 10000 时整体拒绝
  （`ErrTooManySegments`）。

### 并发与可复现性

所有方法均可并发调用（内部读写锁），效果等价于某个串行顺序；
相同的操作序列重放得到完全相同的结果。

### 本地验证

```bash
# 全部用例（含与逐分钟朴素扫描实现的对照、竞态检测）
go test -race -v ./schedule/

# 日志中会打印每个用例的输入、输出与判定依据
go test -v -run TestAgainstNaive ./schedule/
```
