# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 银行流水 / 总账对账匹配器

`reconcile` 包（见 `reconcile/matcher.go`）实现银行（Bank）与账簿（Book）两侧明细的确定性对账。

### 数据模型与登记

- `AddLine(side, id, amt, day, ref)` 随时登记明细，两侧各自最多 `10^5` 行：
  - `id`：非空字节串，同一侧内唯一；重复登记（无论该行已匹配还是曾随匹配撤销）返回 `ErrDuplicateID`。
  - `amt`：带符号非零金额（int64，单位分），`0 < |amt| ≤ 10^12`，正负号参与相等比较。
  - `day`：`[0, 10^9]` 的整数天。
  - `ref`：引用号，可为空串。
  - 错误按 `ErrInvalidSide`（side 不是 Bank/Book）→ `ErrInvalidLine`（id 空 / amt 为 0 或越界 / day 越界 / 该侧已满）→ `ErrDuplicateID` 的顺序只报第一个；被拒绝的操作不改变任何状态。
- 系统保存只增不减的禁配集合 `F ⊆ BankID × BookID` 与匹配序号计数器（从 1 起，跨多次 `Reconcile` 连续递增，撤销不回收 mid）。

### Reconcile(W) 四轮规则

`Reconcile(W)` 把**全部四轮作为一个原子步骤**，只处理当前未匹配行，`W ∈ [0, 10^9]`（越界返回 `ErrInvalidW`）。日期条件统一为 `|dayBank − dayBook| ≤ W`（恰等于 W 算在内，W+1 不算）。任何属于 `F` 的 (银行行, 账簿行) 组合在所有轮次中都不得成为候选或进入集合。

1. **第一轮（ref 一对一）**：银行未匹配行按 id 字节序升序逐条处理；账簿候选需 `ref` 非空且与银行行相同、金额严格相等、日期差 ≤ W。取日期差最小者，日期差并列时取 id 字节序最小者，一对一配成。
2. **第二轮（无 ref 一对一）**：处理顺序与候选选择规则同第一轮，但不再要求 ref 相同。第一轮未配上的行才会进入这一轮，因此“ref 相同但稍远”的候选始终优先于“ref 不同但更近”的候选。
3. **第三轮（一对多）**：银行未匹配行按 id 升序逐条处理，仅处理 `ref` 非空者。取账簿中全部 `ref` 相同、日期差 ≤ W、未匹配且组合不在 `F` 的行组成集合 `S`；当且仅当 `|S| ≥ 2` 且 `sum(S) = 银行金额` 时整组一次配成一对多。不满足则完全不匹配，**不尝试 S 的任何子集**。
4. **第四轮（多对一）**：账簿未匹配行按 id 升序逐条处理，仅处理 `ref` 非空者。取银行中全部 `ref` 相同、日期差 ≤ W、未匹配且组合不在 `F` 的行组成集合 `T`；当且仅当 `|T| ≥ 2` 且 `sum(T) = 账簿金额` 时整组配成多对一，否则不匹配。

每成功配一次即取下一个 mid。返回值为本次新增匹配列表（按产生次序），每项含 `Mid`、`Round`、升序的银行 id 列表与账簿 id 列表。处理是贪心的：按银行（第四轮按账簿）id 顺序逐条处理，先处理的行先占用候选，不做全局最优（例如 b4/b5 共用 k5 时 b4 先配走）。

### 撤销与禁配

- `Reverse(mid)` 撤销一个**仍有效**的匹配：其中所有行回到未匹配状态，并把该匹配的每一个 (银行行, 账簿行) 笛卡尔组合加入 `F`；返回按 id 升序恢复的银行行与账簿行。
- 错误区分：mid 从未产生返回 `ErrMatchNotFound`；mid 曾产生但已撤销返回 `ErrMatchReversed`（前者优先判定）。撤销不回收 mid；这些组合此后在四轮中都不会再成候选（第三、四轮里禁配行会被直接排除出 S/T，可能使集合缩小、基数或金额和不再满足）。

### 查询

- `Unmatched(side)`：按 id 升序返回该侧未匹配行（未达账项）。
- `Matches()`：返回全部**有效**匹配，按银行 id 列表中最小者升序、再按 mid 升序。
- `Forbidden()`：返回 `F` 的快照，按 (银行 id, 账簿 id) 升序。

所有方法（含 `AddLine`、`Reconcile`、`Reverse` 与查询）在内部互斥锁下串行化，结果等价于某个串行顺序；相同的调用序列重放得到完全相同的匹配列表、归属与 F。

### 本地验证

```bash
# 全量测试（含 2000 组随机明细/撤销序列与朴素模拟逐项对照）
go test ./reconcile

# 竞态检测 + 详细日志（打印输入、输出与判定依据）
go test -race -v ./reconcile

# 只跑随机对照 / 规格示例
go test -run TestRandomCompareNaive -v ./reconcile
go test -run 'TestExampleFromSpec|TestManyToOneReverseForbidden' -v ./reconcile
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
