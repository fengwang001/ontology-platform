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

## SLA 违约积分结算器（`sla` 包）

`sla` 包实现带排除窗口、连续违约升级与年度封顶的服务等级违约积分结算器 `Settler`。所有方法可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序；相同操作序列重放得到完全相同的结算结果。

### 结算流程（`Close` 的固定次序）

1. **归并**：本月登记的故障区间按半开区间 `[a, b)` 归并，重叠与首尾相接（前段 `b` 等于后段 `a`）都合并。
2. **扣排除**：从归并结果中减去全部排除窗口的并集，一段可能被切成两段。
3. **丢抖动**：丢弃长度严格小于 `g` 的残段（恰等于 `g` 保留），剩余总长记为 `D`（恒有 `0 ≤ D ≤ L`）。注意先扣排除再丢弃，与先丢弃再扣排除结果不同。
4. **可用度**：`A = floor((L − D) × 10^6 / L)`（百万分比，向下取整）。
5. **基础档位**：`A ≥ T1` 时 `base = 0`；否则 `A ≥ T2` 时 `base = c1`；否则 `A ≥ T3` 时 `base = c2`；否则 `base = c3`。
6. **升级与封顶**：`base = 0` 时连续违约月数 `s` 归零、积分为 0；否则
   - `pct = min(100, base + st × min(s, sm))`；
   - `credit = ceil(fee × pct / 100)`（向上取整，且不超过 `fee`）；
   - 年度截断：`credit = min(credit, Y − 该年已发放)`，截断发生在升级之后；年号为月序号 `m` 除以 12 向下取整，跨年时年度额度重置而 `s` 不重置；
   - 该年已发放加上 `credit`（恒不超过 `Y`），`s` 加 1。

### 操作与拒绝规则

- `New(params)`：构造参数越界（月长、抖动、档位、百分点、步长、封顶月数、年度额度、排除上限）整体拒绝。
- `NewMonth(fee)`：`fee` 须在 `1..10^12`；已有开放月时以「月已开放」拒绝。
- `Report(a, b)` / `AddExclude(a, b)`：登记故障区间 / 排除窗口，要求 `0 ≤ a < b ≤ L`；`AddExclude` 还要求加入后本月排除窗口**并集**总长不超过 `ex`（恰等于允许，重叠或相同窗口不重复计长），否则以「排除超限」拒绝。
- `Revoke(a, b)`：删除一份完全相同的已登记故障区间，找不到时以「不存在」拒绝。
- 无开放月时 `Report`/`AddExclude`/`Revoke`/`Close` 以「无开放月」拒绝。
- 错误优先级：参数非法 → 状态类（月已开放/无开放月）→ 排除超限 → 不存在，只报第一个；被拒绝的操作不改变任何状态。

### 本地验证

```bash
# 单元测试 + 2000 组随机序列与按分钟标记的朴素模拟对照（-v 打印输入、输出与判定依据）
go test ./sla/
go test -v -run TestRandomAgainstNaive ./sla/

# 竞态检测
go test -race ./...

# 代码检查
gofmt -l .
go vet ./...
```
