# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## STM 竞争管理器（`stm` 包）

`stm` 包实现软件事务内存的竞争管理器：事务打开对象发生冲突时，按累计积分、失败重试次数与被中止次数升级出的特权裁决谁中止、谁等待，并给出退避延迟。所有结果、积分与延迟完全确定，相同调用序列重放结果一致；所有方法可并发调用，效果等价于某个串行顺序（内部以单互斥锁串行化）。

### 构造参数

| 参数 | 字段 | 范围 | 含义 |
| --- | --- | --- | --- |
| M | `Objects` | 1..64 | 对象数 |
| L | `PrivThreshold` | 1..16 | 特权阈值：`ab >= L` 即特权事务 |
| D | `BaseDelay` | 1..1000 | 基础退避延迟 |
| E | `ExpCap` | 0..20 | 退避指数封顶 |
| P | `ScoreCap` | 1..1000 | 积分 `kp` 上限 |
| Q | `ForceThreshold` | 1..16 | 强制阈值：重试计数 `k >= Q` 压过非特权敌手 |

任一参数越界则整体拒绝（`ErrInvalidConfig`）。`Begin()` 返回从 1 起递增的事务号。

### 裁决顺序（Open 冲突时）

设 `k = att[o]`（本次尝试中对对象 o 的连续失败次数），敌手为：求写时的他人写者与全部他人读者；求读时的他人写者。逐个敌手 `e` 判定 `t` 是否压过 `e`：

1. 皆特权：事务号小者胜；
2. 仅 `t` 特权：`t` 胜；
3. 仅 `e` 特权：`t` 败；
4. 皆非特权：`k >= Q` 或 `kp(t) + k > kp(e)`（严格大于）时胜。

压过全部敌手才动手：每个敌手转已中止、`ab` 加一、`kp` 衰减为 `⌈kp/2⌉`、释放全部持有并清空 `att`；随后 `t` 获得对象，返回被中止者升序列表。否则不动任何敌手，仅 `att[o] = k+1`，返回等待与延迟。

### 积分与计数规则

- `kp`：仅在本次尝试中首次获得某对象时 `+1`（封顶 P）；被他人中止时取 `⌈kp/2⌉`；读升级为写、重复打开已持有对象均不加分。
- `ab`：只因被他人中止而 `+1`，提交前不减；自愿中止（`Abort`）不改 `ab` 与 `kp`。
- `att[o]`：等待时 `+1`；获得该对象时清零（无论是否加分）；被他人中止、`Commit`、`Abort`、`Restart` 释放持有时一并清空。
- 敌手考察只读对象的写者/读者集合，数量不超过读者数加一，与事务总数无关。

### 延迟公式

- `Open` 等待：`D × 2^min(k, E)`，`k` 取 `att[o]` 增加前的值。
- `Restart`：`D × 2^min(max(ab−1, 0), E)`。

### 调用拒绝

构造参数越界整体拒绝。其余调用按以下顺序只报第一个原因：事务号不存在（`ErrNoSuchTxn`）；状态不符（`ErrBadState`，`Open`/`Commit`/`Abort` 须活跃，`Restart` 须已中止）；对象越界（`ErrNoSuchObject`，仅 `Open`）。被拒绝的调用不改变任何状态；被中止者在 `Restart` 前不可再打开对象。

### 本地验证

```bash
go test ./stm/                 # 规则单元测试 + 2000 组随机序列对照朴素模拟
go test -race ./stm/           # 并发线性化与不变量检查
go test ./stm/ -run TestRandomSequencesMatchNaive -v   # 打印每步输入、输出与判定依据
```

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
