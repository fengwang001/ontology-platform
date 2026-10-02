# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 银行流水与总账对账匹配器（`reconcile` 包）

`reconcile` 包在银行（Bank）与账簿（Book）两侧明细行之间做确定性对账。
每侧至多 10^5 行；行含非空唯一编号 `id`、带符号非零金额 `amt`（int64，
单位分，|amt| ≤ 10^12）、日期 `day`（0 到 10^9）与可空的引用号 `ref`。
系统维护只增不减的禁配集合 F（(银行行 id, 账簿行 id) 对）与从 1 起、跨
`Reconcile` 连续递增的匹配序号 mid。

### 四轮匹配规则

`Reconcile(W)`（W 为日期容差，0 到 10^9，日期差恰等于 W 算合格）对当前
全部未匹配行原子地执行四轮；凡属于 F 的组合一律不得成为候选或进入集合：

1. **第一轮（ref 精确一对一）**：银行未匹配行按 id 字节序升序逐条处理，
   在账簿未匹配行中找 ref 非空且相同、金额相等、日期差 ≤ W 的行；取日期
   差最小者，并列取 id 最小者。
2. **第二轮（自由一对一）**：同样逐条处理，去掉 ref 要求（仍需金额相等、
   日期差 ≤ W），选择规则同第一轮。先处理的银行行优先，不做全局最优。
3. **第三轮（一对多）**：银行未匹配且 ref 非空的行按 id 升序逐条处理，
   取账簿中全部 ref 相同、日期差 ≤ W 的未匹配行组成集合 S；当 |S| ≥ 2
   且 S 内金额之和等于该银行行金额时整组一次配成，否则不匹配，也不尝试
   S 的任何子集。
4. **第四轮（多对一）**：第三轮的镜像——账簿未匹配且 ref 非空的行按 id
   升序处理，集合 T 取自银行侧，条件相同。

每次成功配对取下一个 mid。`Reconcile` 返回本次新增匹配（含 mid、轮次、
升序的两侧 id 列表），按产生次序排列。

### 撤销与禁配

`Reverse(mid)` 撤销一个仍有效的匹配：其中全部行回到未匹配状态，且该匹配
中每一个 (银行行, 账簿行) 组合都加入 F，此后任何轮次都不得再配回；mid 不
回收，重新匹配会得到新 mid。`Reverse` 区分"mid 从未产生"
（`ErrMatchNotFound`）与"已撤销"（`ErrMatchReversed`）。

### 查询与校验

- `Unmatched(side)` 按 id 升序返回未匹配行（未达账项）。
- `Matches()` 返回全部有效匹配，按银行行 id 列表最小者升序、再按 mid 升序。
- `Forbidden()` 返回当前禁配集合（升序）。
- `AddLine` 按"侧非法 → 行非法（id 空、amt 为 0 或越界、day 越界、该侧
  已满）→ 编号重复"的顺序只报第一个错误；`Reconcile` 的 W 越界报
  `ErrInvalidWindow`。任何被拒绝的操作不改变行、匹配、F 与 mid 计数器。
- 全部方法可并发调用，结果等价于某个串行顺序；同一批行以任意顺序登记后
  执行一次 `Reconcile` 得到逐项相同的匹配；相同调用序列重放得到完全相同
  的匹配列表与 F。

### 本地验证

```bash
# 全部测试（含两个规格示例、边界规则、2000 组随机对照朴素模拟）
go test ./reconcile/

# 查看随机对照试验的输入、输出与判定日志
go test -run TestRandomAgainstNaive -v ./reconcile/

# 竞态检测
go test -race ./reconcile/
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
