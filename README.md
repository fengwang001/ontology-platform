# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 用户级广告频次控制器（`freqcap` 包）

`freqcap.Controller` 为每个用户独立维护已放行曝光记录（时刻 `t`、系列、创意），
构造参数：`Wg` 全局窗口长度（1..1e9）、`Cg` 全局窗口上限（1..1e6）、`K` 收紧步长
（1..1e6）、`Cc` 系列日上限（1..1e6）、`g0` 创意基础间隔（1..1e9）；`now` 为
0..1e15 的 int64。构造参数越界时 `NewController` 返回错误。

### 三项判定的次序与公式

`Admit` / `Peek` / `AdmitBatch` 中每条请求都按固定次序判定，全部通过才放行：

1. **创意连播间隔**：若该用户最近一次已放行曝光的创意等于 `cre`，令 `s` 为该用户
   末尾连续同一创意的已放行曝光个数（不论这些曝光是否已移出全局窗口），要求
   `now - t_last >= g0 * min(s, 3)`；最近一次创意不同则无间隔要求。
2. **全局窗口**：`cg = #{已放行曝光满足 t + Wg > now}`，要求 `cg < Cg`。
   `t + Wg == now` 视为已过期。
3. **系列日上限**：日 `day = floor(now / 86400)`，`n_c` 为该用户在同一 `day` 内对
   `camp` 已放行的曝光个数，有效上限 `E = max(1, Cc - floor(cg / K))`
   （`cg` 取第 2 项算得的值），要求 `n_c < E`。

### 拒绝原因与优先级

只报告第一个命中的原因，顺序为：

1. `invalid_param`：构造参数越界、用户/系列/创意为空、`now` 越界、批量长度不在 1..1000；
2. `clock_rollback`：`now` 小于该用户最近一次已放行曝光的时刻（不同用户互不影响）；
3. `creative_interval`；4. `global_window`；5. `campaign_daily`。

被拒绝的操作不改变任何用户的记录、连播计数与时钟。`Peek` 与 `Admit` 判定完全相同
但不记录；`AdmitBatch` 对同一用户按列表顺序逐条判定（前面已放行的会影响后面的
`s`、`cg`、`n_c`），任一条被拒则整批回滚并返回首个被拒下标与原因，全部通过才整批记录。

### 有效上限收紧与连播计数规则

- 全局曝光量收紧系列上限：窗口内每多 `K` 次曝光，`E` 减 1，且 `E` 不低于 1。
- 连播计数 `s` 只统计末尾连续同一创意的已放行曝光；被不同创意打断后从 1 重新计；
  间隔要求随 `s` 增长但在 `s = 3` 处封顶；间隔拒绝不推进时钟也不计入 `s`。
- 过期记录随 `now` 前进被回收：每个用户保留的窗口内记录数不超过 `Cg`，当日各系列
  计数只保留当天，均不随累计曝光总数增长。`Stats(user)` 暴露 `Admitted` / `Evicted`
  / `Windowed` 计数器用于验证（弹出总数不超过已放行次数）。
- 并发：所有方法可并发调用，等价于某个串行顺序；不同用户持独立锁，互不阻塞。
  相同操作序列重放得到完全相同的放行结果与拒绝原因。

### 本地验证

```bash
# 全部单元测试（含与朴素模拟对照的 2000 组随机序列）
go test ./freqcap/

# 打印每条随机操作的输入、输出与判定依据
go test -run TestRandomAgainstNaive -v ./freqcap/

# 竞态检测 + 并发不变量
go test -race ./freqcap/

# 过期记录回收档位（1000 与 100000 次放行下的计数器对比）
go test -run TestEvictionBounds -v ./freqcap/
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
