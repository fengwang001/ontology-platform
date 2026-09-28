# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 增量分组聚合组件 `groupagg`

`groupagg` 包实现了一个支持**分组键变更**的增量分组聚合组件：随着行的
插入（Insert）、更新（Update）、删除（Delete）实时维护每个分组的
**求和（Sum）**与**计数（Count）**，并输出净变化（changelog）。下游按
顺序应用这些条目，始终得到正确的分组聚合视图。

### 聚合不变量

- 每个分组的 `Sum` / `Count` 恒等于该组当前所有行的值之和与行数；
- 分组**只在计数大于 0 时**出现在视图中；最后一行离开后分组立即消失
  （求和为 0 但计数为正的组仍然保留）；
- 视图可通过 `Snapshot()`（map）、`View()`（按分组键排序）并发读取，
  始终与对行表做批量重算的结果一致；
- 同一输入序列反复计算产生完全相同的日志与视图。

### 输出规则（changelog）

每个操作先确定其前后受影响的分组，再按以下顺序输出条目：

1. **改分组键时先旧组、后新组**（同组更新/插入/删除只涉及一个组）；
2. 对每个受影响分组，先输出一条 `RETRACT`（该组变更前的完整
   Sum/Count），再输出一条 `PUT`（变更后的完整 Sum/Count）；
3. `Seq` 全局单调递增（从 1 开始）。

边界约定：

- 向此前不存在的分组插入行时，先撤回缺席值 `RETRACT{sum:0,count:0}`，
  再 `PUT` 新聚合；
- 删除/移走分组最后一行时，以 `PUT{sum:0,count:0}` 作为墓碑，组从视图
  消失。

下游回放语义：收到 `RETRACT` 删除该分组旧值；收到 `PUT` 时 `count>0`
则设置、`count=0` 则删除该分组。

### 非法输入与原子拒绝

以下情况整批拒绝，返回可区分原因的 `*RejectError`（附触发操作的下标），
**行表、聚合与已产生的日志均不改变**（批次先在副本上模拟，全部合法才提交）：

| 原因（`Reason`）         | 含义                                   |
| ------------------------ | -------------------------------------- |
| `DUPLICATE_INSERT`       | 重复插入已存在的行 ID                  |
| `UPDATE_MISSING`         | 更新不存在的行                         |
| `DELETE_MISSING`         | 删除不存在的行                         |
| `EMPTY_GROUP_KEY`        | 插入/更新使用空分组键                  |
| `EMPTY_ID`               | 空行 ID                                 |
| `UNKNOWN_OP`             | 未知操作类型                           |
| `TOO_MANY_GROUPS`        | 操作会使非空分组数超过 `maxGroups` 上限 |

改分组键是“旧组 −1、新组 +1”：若旧组仅剩该行，非空分组数净变化为 0，
即使达到组数上限也允许；只有净增新组时才触发上限拒绝。

### 诊断日志

`New(maxGroups, io.Writer)` 传入 writer 后，每个接受的操作会打印
**输入操作、输出条目与判定依据（basis）**；被拒绝的批次会打印全部输入、
标出触发操作下标并给出拒绝原因。该日志仅用于诊断，不是 changelog。

### 最小用法

```go
import "ontology/groupagg"

a := groupagg.New(0, os.Stdout)              // 0 表示不限制组数
res, err := a.Apply([]groupagg.Op{
    {Kind: groupagg.OpInsert, ID: "r1", GroupKey: "alpha", Value: 10},
    {Kind: groupagg.OpUpdate, ID: "r1", GroupKey: "beta", Value: 10}, // 改键
    {Kind: groupagg.OpDelete, ID: "r1"},
})
// res.Entries 即本批次净变化；a.View() 为当前排序视图
```

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

## 测试与本地验证

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出（含并发读用例）
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestRekey ./groupagg

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
