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

## NUMA 对齐管理器（`numa` 包）

`numa.Manager` 把内置 CPU 提供者与多个外部资源提供者对同一容器给出的
NUMA 亲和提示合并为一个最优提示，按策略准入并按结果掩码记账分配 CPU。

构造：`NewManager(n, caps, policy)`。

- `n`：NUMA 节点数，1–8；`caps[i]`：各节点容量，1–10^9，个数必须等于 `n`。
- `policy`：`none` / `best-effort` / `restricted` / `single-numa-node`。
- 掩码为 `n` 位非空位集合（第 0 位为最低位），全掩码为低 `n` 位全 1；
  提示为 `(Mask, Preferred)`。

### 内置 CPU 提供者提示

`Admit(id, req, providers)` 时，`free_i = cap_i - 已分配`：

1. 枚举每个非空掩码 `m`，若 `m` 内各节点 `free` 之和 `>= req` 则 `m` 可行。
2. 令 `Zc` 为所有可行掩码中位数最小者的位数。
3. 每个可行掩码 `m` 都产出提示 `(m, 位数 == Zc)`；无可行掩码则列表为空。

### 提示归一（内置与外部一律适用）

- 无偏好（`nil` 提供者）→ 单一提示 `(全掩码, true)`。
- 空列表（无法满足）→ 单一提示 `(全掩码, false)`。
- `single-numa-node` 策略：非空列表先丢弃位数不为 1 的提示；丢弃后变空
  则按空列表处理。

### 合并

对每个提供者各取一个提示做全部笛卡尔组合：

1. 各提示掩码按位与，结果为 0（空）的组合丢弃，其余为候选。
2. 候选的 `allPref` = 所选各提示 `preferred` 全为 true。
3. 令 `Z` 为所有 `allPref` 候选中位数最小者的位数；候选最终
   `preferred = allPref && 位数 == Z`。没有 `allPref` 候选时无候选 preferred。

最优提示选择顺序：先取 preferred 的，再取位数少的，再取掩码数值小的；
没有候选时最优为 `(全掩码, false)`。

### 准入与分配

拒绝原因（按此顺序只报第一个）：

1. `invalid-argument`：ID 为空、`req` 越界（1–10^12）、外部掩码为 0 或含超出
   `n` 位的位。
2. `container-exists`：容器已存在。
3. `insufficient-cpu`：全部节点 `free` 之和小于 `req`（`none` 同样检查）。
4. `too-many-combinations`：归一后各提供者提示数乘积超过 10^5（内置计入，
   同列表内重复提示不去重；`none` 不检查）。
5. `hint-not-satisfied`：`restricted` / `single-numa-node` 下最优提示非 preferred。

策略行为：

- `none`：总是准入，结果恒为 `(全掩码, true)`，忽略全部提示且不做组合数检查。
- `best-effort`：总是准入，取最优提示（可为非 preferred）。
- `restricted` / `single-numa-node`：最优提示 preferred 为 true 才准入。

准入成功后的 CPU 分配次序（`req` 恒定满足，因容量已检查）：

1. 先按节点号升序，从结果掩码内节点各取 `min(free_i, 剩余需求)`。
2. 仍不足时，再按节点号升序从掩码外节点取。

`Release(id)` 归还并删除记录（不存在返回 `container-missing`）；
`Query(id)` 返回掩码、各节点分配量与 `req`（不存在返回 `container-missing`）。
所有操作在互斥锁下串行化，对外等价于某个全序，同一容器并发 `Admit`
恰有一次成功。

### 本地验证

```bash
# 全量测试（含 2000 组随机输入与独立暴力模拟对照、并发测试）
go test -race -v ./numa

# 快速模式（跳过 2000 组随机对照）
go test -short ./numa
```

随机对照测试把每组输入、内置提示（含 Zc）、候选数、最优提示、准入判定、
逐节点分配量写入系统临时目录下的 `numa-crosscheck.log`（测试输出中打印完整
路径），并逐步比对独立实现的笛卡尔积暴力枚举模拟器。
