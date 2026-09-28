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

## 消费组增量协作式再均衡（`rebalance` 包）

`rebalance` 包实现消费组在成员变化时的增量协作式再均衡，采用两轮协议，
保证无需迁移的分区不中断消费、任意时刻每个分区至多一个持有者、结果确定可复现。

### 两轮协议

1. **撤销轮（第一轮）**：`Join` / `Leave` 后重新计算每个分区的目标持有者。
   只有当前“消费中”且目标改变的分区进入“撤销中”；目标未变的“消费中”分区
   保持消费、不中断。“撤销中”分区只会等待旧持有者确认，期间即使目标再变
   也不会取消撤销（避免在新旧目标间来回抖动）。
2. **确认 + 分配轮（第二轮）**：成员调用 `ConfirmRevocation` 后，其全部
   “撤销中”分区变为“无主”。当全组不存在“撤销中”分区时，所有“无主”分区
   一次性按当前目标分配并置为“消费中”；若仍有分区处于“撤销中”，分配延后。
   成员集合为空时不分配，分区保持“无主”。

### 分区状态

- `Consuming`（消费中）：被某个成员稳定持有；目标未变时永不中断。
- `Revoking`（撤销中）：等待当前持有者确认撤销；确认后变“无主”，
  期间目标再变也不会取消。
- `Unassigned`（无主）：无持有者；全组无“撤销中”分区时才会被分配。

### 目标计算

目标是成员 ID 升序集合上的确定性函数（成员 ID 为正整数，分区编号从 0 开始）：

- 取**不小于分区号的最小成员**；
- 若不存在（所有成员都小于分区号），**环形回绕取最小成员**；
- 成员为空时无目标。

该函数无随机数、与操作历史无关，因此相同操作序列在任意调度下结果一致
（确定性测试用相同序列重放校验完全相等的快照）。

### 并发与不变量

协调器内部用互斥锁保护，`Join`、`Leave`、`ConfirmRevocation` 与所有查询
可被多个执行体并发调用。不变量：

- 任意时刻每个分区至多一个持有者（“无主”时 owner 为 -1）；
- 目标未变的“消费中”分区不会被撤销或重分配；
- 静止时（无“撤销中”分区）若成员非空，全部分区为“消费中”且持有者
   与朴素目标函数完全一致。

所有变更操作采用“先校验、后落盘”：任一前置条件不满足即整体拒绝，
拒绝不会改变成员集合或任何分区状态（失败不留痕）。

### 边界与错误类别

错误哨兵互不相同，可用 `errors.Is` 区分：

- `ErrInvalidArgument`：分区数非正、成员 ID 非正、分区号越界等非法参数；
- `ErrMemberExists`：重复加入（成员已在组内）；
- `ErrMemberNotFound`：离开或确认时成员不存在；
- `ErrNoPendingRevocation`：成员确认时没有任何“撤销中”分区。

### 本地验证

```bash
# 全量测试 + 竞态检测（推荐）
go test -race -v ./rebalance/

# 单场景
go test -run 'TestTwoRoundProtocol|TestWrapAround' -v ./rebalance/

# 覆盖率
go test -coverprofile=coverage.out ./rebalance/
go tool cover -html=coverage.out
```

测试通过注入的 logger 打印每步输入、全量分区状态（状态/持有者）与每步
判定依据（哪个分区为何进入撤销、为何保持消费、第二轮为何延后或分配）。
覆盖场景：两轮协议、环形回绕、确认后分配、离开后重分配、空组不分配、
已撤销分区不被目标变化取消、四类非法输入及拒绝后状态不变、并发不变量
与静止时与朴素目标一致、相同操作序列结果可复现。
