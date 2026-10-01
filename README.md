# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## NUMA 对齐管理器（`numa` 包）

`numa.Manager` 把内置 CPU 提供者与多个外部资源提供者对同一容器给出的
NUMA 亲和提示合并为一个最优提示，按策略准入，并按结果掩码做 CPU 记账。

### 基本概念

- `n`：NUMA 节点数（1–8）；`cap_i`：节点 `i` 的 CPU 容量（1–10^9）。
- 掩码：`n` 位非空节点集合，第 `i` 位表示节点 `i`（位 0 为最低位）；全掩码为 `2^n - 1`。
- 提示 `Hint{Mask, Preferred}`；外部提供者 `ProviderHints`：
  - `NoPreference=true` 表示无偏好；
  - `Hints` 为空切片表示空列表（无法满足）；
  - 否则为非空提示列表（允许重复，重复提示不去重）。

### 内置 CPU 提供者提示

设 `free_i = cap_i - 已分配`，对容器需求 `req`（1–10^12）：

1. 枚举每个非空掩码 `m`，若 `m` 内各节点 `free` 之和 `>= req` 则 `m` 可行；
2. 令 `Zc` 为所有可行掩码中最小的位数；
3. 每个可行掩码产出 `(m, 位数 == Zc)`；没有可行掩码时列表为空。

### 提示归一（内置与外部一律适用）

- 无偏好 -> 单提示 `(全掩码, true)`；
- 空列表 -> 单提示 `(全掩码, false)`；
- `single-numa-node` 策略下，非空列表先丢弃位数不为 1 的提示，丢弃后为空按空列表处理。

### 合并

- 对每个提供者各取一个提示做笛卡尔积（内置提供者计入），掩码按位与；
- 相与为空的组合丢弃，其余成为候选；
- 候选 `allPref` 为所选各提示 `preferred` 的逻辑与；
- 令 `Z` 为所有 `allPref` 候选中最小的位数；候选的 `preferred = allPref && 位数 == Z`；
  没有 `allPref` 候选时所有候选 `preferred=false`。

### 最优选择

依次按：`preferred=true` 优先 -> 位数少 -> 掩码数值小。没有候选时最优为 `(全掩码, false)`。

### 策略准入

- `none`：总是准入，结果固定为 `(全掩码, true)`，忽略全部提示且不做组合数检查；
- `best-effort`：总是准入，取最优提示；
- `restricted` / `single-numa-node`：仅当最优提示 `preferred=true` 时准入。

### CPU 分配次序

准入成功后按结果掩码分配 `req`：

1. 先按节点号升序，从掩码内节点各取 `min(free_i, 剩余需求)`；
2. 仍不足时，再按节点号升序从掩码外节点取；
3. 记录每节点分配量。总空闲不足已在准入前拦截，因此两步后必然分满。

`Release(id)` 删除记录并归还各节点分配；`Query(id)` 返回掩码、preferred 与各节点分配量。

### 拒绝原因（按此顺序只报第一个）

1. 参数非法：容器 ID 为空、`req` 越界、任一外部提示掩码为 0 或含超出 `n` 位的位；
2. 容器已存在（`Admit`）；
3. 容量不足：全部节点空闲和 < `req`（`none` 同样检查）；
4. 组合过多：归一后各提供者提示数乘积 > 10^5（内置计入、重复不去重、`none` 不检查）；
5. 提示不满足：`restricted` / `single-numa-node` 下最优提示 `preferred=false`。

构造期 `n` 越界、`cap` 越界或个数不符、策略未知均为配置非法（`ErrInvalidConfig`）。
`Release` 与 `Query` 的容器不存在分别返回 `ErrReleaseNotFound`、`ErrQueryNotFound`。
被拒绝的操作不改变任何已登记容器、掩码与分配。

### 并发与确定性

所有操作在互斥锁下串行化，等价于某个全局串行顺序；同一容器的并发
`Admit` 恰有一次成功。相同的 `Admit`/`Release` 序列重放得到完全相同的
结果掩码、preferred、准入判定与各节点分配。

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

# NUMA 包：规则用例 + 并发/确定性 + 2000 组暴力枚举随机对照（日志含输入/输出/判定依据）
go test -race -v ./numa
go test -run TestRandomDifferential2000 -v ./numa

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
