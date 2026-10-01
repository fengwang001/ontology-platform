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

## 流式近似子串匹配器（`matcher` 包）

`matcher.New(pattern, k)` 创建线程安全的流式匹配器，用 `Feed([]byte)` 逐块
消费任意切分的字节流，用 `Close()` 结束；`Stats()` 可在任何时刻并发查询。

### 匹配定义

- 模式 `P` 为 1–64 字节，阈值 `k` 满足 `0 <= k < len(P)`。
- 编辑距离为字节级 Levenshtein，插入、删除、替换各计 1。
- 对每个结束位置 `e`（开区间，已消费 `e` 个字节，`e >= 1`），在所有
  `s ∈ [0, e]`（子串可为空）中取 `Lev(P, T[s:e))` 的最小值 `D(e)`。
- 当 `D(e) <= k` 时 `e` 是命中结束位置；`Dist = D(e)`，`Start` 是所有取得
  `D(e)` 的起点 `s` 中最小者（最左起点）。

实现采用半全局（semi-global）动态规划，每个单元保存 `(dist, start)` 二元组，
按字典序取优：距离优先，距离相同取更小起点。DP 只保留相邻两行，状态 O(m)，
不随已消费字节数增长；非导出计数器 `cellUpdates` 记录被更新的单元数，恰等于
`m × 已消费字节数`（见 `TestCellUpdateCounter`，对比 1000 与 100000 字节两档）。

### 连续段折叠

- 相邻命中结束位置（`e` 与 `e+1`）属于同一连续段；`D(e) > k` 的位置把段隔开。
- 每个连续段结束时至多产生一条报告：代表取段内 `Dist` 最小者，`Dist` 并列时
  取 `End` 最小者，报告含 `End`、`Dist`、该位置的 `Start`。
- 段在消费到第一个 `D > k` 的字节时（该次 `Feed`）结束；流末尾仍开着的段在
  `Close` 时结束。`Feed` 返回本次块内结束各段的报告，按 `End` 升序。
- 折叠随流增量维护，只保存“段内代表”这一个候选，不缓存全部命中位置，因此段内
  状态大小不随段长增长。

### 抑制规则

记 `lastEnd` 为已报告匹配的最大 `End`（初值 0）：

- 代表 `Start < lastEnd`：与已报告匹配重叠，抑制，`Suppressed += 1`，不更新
  `lastEnd`；
- 代表 `Start == lastEnd`：相接不算重叠，保留；
- 保留的报告 `Reports += 1`，并更新 `lastEnd = max(lastEnd, End)`。

空块（`nil` 或长度 0）不产生报告也不改变状态。任意切分（含逐字节、切在段
中间）下，全部返回报告拼接后逐项相同，与整段一次性计算一致；相同输入重放结果
完全相同。

### 拒绝原因（只报第一个，被拒绝操作不改变状态）

1. 构造：模式为空或长于 64 → `matcher.ErrInvalidPattern`；
2. 构造：`k < 0` 或 `k >= len(P)` → `matcher.ErrInvalidThreshold`；
3. 已关闭后调用 `Feed` 或再次 `Close` → `matcher.ErrClosed`；首次 `Close` 成功。

### 本地验证

```bash
# 常规
go test ./...

# 竞态检测 + 详细日志（含 2000 组与朴素实现的随机对拍，打印输入/输出/判定依据）
go test -race -v ./matcher/

# 只跑对拍或单元更新计数验证
go test -run 'TestRandomAgainstNaive|TestCellUpdateCounter' -v ./matcher/
```

测试覆盖：`k=0` 精确匹配与重叠出现、距离恰等于 `k` 与 `k+1`、同结束位置多
起点（替换/插入/删除路径）取最左、起点取到 0、段内 `Dist` 并列取最早 `End`、
段在 `Feed` 内结束与在 `Close` 结束、逐字节喂入与每个切分点两块切分、
`Start == lastEnd` 保留与 `< lastEnd` 抑制、被抑制代表不更新 `lastEnd`、空块、
全相同字节最坏形态、题面两个示例，以及与朴素实现（枚举每个 `(s,e)` 直接计算
Levenshtein 再分段折叠与抑制）的 2000 组随机对拍和并发调用不变量校验。
