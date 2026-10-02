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

## streammatch：流式近似子串匹配器

`streammatch` 包把任意切分的字节流与固定模式 `P`（长度 `m` 为 1 到 64）
按字节级 Levenshtein 距离（插入、删除、替换各计 1）做增量比对。

### 匹配定义

- 文本位置从 0 起按字节计，`Feed` 依次消费字节流。对每个结束位置 `e`
  （开区间，已消费 `e` 个字节，`e >= 1`），`D(e)` 是 `P` 与任一以 `e`
  结尾的子串 `T[s,e)`（`s` 取 0 到 `e`，可为空串）的最小编辑距离。
- `D(e) <= k` 时 `e` 是命中结束位置：`Dist = D(e)`，`Start` 为所有达到
  `D(e)` 的起点 `s` 中的最小者（最左起点）。同一结束位置可能有多条
  等代价路径（如替换与插入/删除），其起点不同，一律取最小。

### 连续段折叠与抑制

- 相邻且都命中的结束位置（`e` 与 `e+1`）属于同一连续段；`D(e) > k`
  的结束位置把段隔开。段内代表为 `Dist` 最小者，并列取 `End` 最小者。
- 段在消费了第一个使 `D` 大于 `k` 的字节的那次 `Feed` 结束，或在
  `Close` 时结束（流末尾仍开着的段）。`Feed` 返回本次块内结束的各段
  报告，按 `End` 升序；空块合法，不产生报告也不改变状态。
- 不重叠规则：`lastEnd` 为已报告匹配的最大 `End`（初值 0）。代表的
  `Start < lastEnd` 时被抑制（`Suppressed` 加 1，不更新 `lastEnd`）；
  `Start == lastEnd` 属于相接，保留并令 `lastEnd = End`。
- 代表与 `lastEnd` 判定随流增量维护：段内状态只有当前代表（O(1)，
  不随段长增长），不缓存全部命中结束位置。

### 实现与复杂度

- Sellers 动态规划只保留相邻两列（每列同时记录该格最优值的最左起点），
每消费一个字节恰更新 `m` 个单元（测试用非导出计数器 `cellUpdates`
验证其等于 `m * 已消费字节数`，1000 与 100000 字节两档均成立，不随
已消费长度回头重算）。时间 `O(m)` 每字节，空间 `O(m)`。
- `Feed`、`Close`、`Stats` 可并发调用（互斥锁串行化），结果等价于某个
  串行顺序；同一字节流任意切分，全部返回报告拼接后逐项相同。
- 拒绝原因可区分且有序（被拒绝的操作不改变状态）：模式为空或长于 64
  返回 `ErrInvalidPattern`；`k < 0` 或 `k >= m` 返回 `ErrInvalidK`；
  已关闭后 `Feed` 或第二次 `Close` 返回 `ErrClosed`。

### 本地验证

```bash
# 全部测试（含 2000 组随机输入与朴素实现的对拍，
# 以及逐字节、随机切分、每个切分点两块的切分不变性）
go test ./streammatch/

# 竞态检测 + 打印每组对拍的输入、输出与判定依据
go test -race -v -run TestRandomAgainstOracle ./streammatch/
```
