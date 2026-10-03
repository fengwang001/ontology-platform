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

## 固定优先级 RTA 分析器（`rta` 包）

`rta` 包实现带释放抖动 J 与阻塞项 B 的固定优先级响应时间分析（RTA），
支持 Audsley 优先级分配与增量任务接纳。入口为 `rta.New()` 返回的
`*rta.Scheduler`，所有方法均并发安全（内部 `sync.RWMutex`，
结果等价于某个串行顺序）。

### 任务与校验

`rta.Task{ID, C, T, D, J, B}` 均为 int64（`ID` 为字符串）：

- `ID`：非空，UTF-8 字节长度 ≤ 64。
- `1 ≤ C,T ≤ 10^9`；`1 ≤ D ≤ T`；`0 ≤ J,B ≤ 10^9`。
- 分析器最多容纳 `rta.MaxTasks = 32` 个任务。

### 响应时间公式与不动点终止条件

优先级为全序，序在前者更高。对序中任务 i，令 H 为排在 i 之前的全部
任务，定义工作量需求

```
w = C_i + B_i + Σ_{j∈H} ceil((w + J_j) / T_j) · C_j
```

取其最小不动点：自初值 `w₀ = C_i + B_i` 起迭代 `w ← demand(w)`。
终止规则：
- 迭代中出现的**每一个** w（含初值）都先做截止判定；一旦
  `w + J_i > D_i` 立即判该序不可调度并停止（本任务自己的抖动计入
  截止判定；干扰任务的抖动进入 ceil 内的 `w + J_j`）。
- 当 `demand(w) ≤ w` 时收敛，响应时间 `R_i = w + J_i`。
- 求和使用 128 位累加防溢出；中间需求超过饱和阈值（10^18）时直接
  判不可调度（合法参数下真实 w 远小于该值，需求单调不可能回落）。

不变量：任何时刻 `Order()` 的任意前缀作为独立任务集都全员可调度，
且 `R_i ≤ D_i`；`Response(id)` 只读取任务上缓存的 R，不执行任何
不动点迭代。

### Add 的插入尝试与 Audsley 回退

`Add(task)` 分两阶段：

1. **增量插入**：自最低优先级位置（排最后）起向高位逐个尝试把新任务
   插入现序，取第一个使全体任务可调度的位置；已有任务相对次序保持
   不变。成功时位置 p 之前的任务 R 不变，仅重算 p 及其之后的任务。
2. **Audsley 回退**：所有位置都失败时，对“现有任务 + 新任务”整体
   重跑 Audsley——自最低位起，在未分配任务中令其余全部未分配任务
   为 H，取其中可调度且编号字节序最小者占据该位，再填次低位，直至
   填完；某位无人可调度则整体失败（该 Add 被拒绝）。候选的可调度性
   只依赖 H 的集合，与 H 内部次序无关。

返回值 `AddResult.Reordered` 标识是否发生了阶段二的整体重排。
`Remove(id)` 仅删除该任务，其余次序不变，且只重算排在被删任务之后
的任务；删除后余下任务恒可调度。

### 拒绝原因优先级

被拒绝的操作不改变任务集与优先级序。错误为 `*rta.Error`，按下列
顺序只报第一个匹配项：

1. `ReasonInvalidParam`：编号空/超长、数值越界，或 Remove/Response
   的编号为空字符串。
2. `ReasonDuplicateID`：编号已存在。
3. `ReasonCapacityFull`：已有 32 个任务。
4. `ReasonUnschedulable`：插入尝试与 Audsley 回退均失败；
   `Error.Unassigned` 携带 Audsley 失败时尚未分配的任务个数
   （含正在填的那一位）。
5. `ReasonNotFound`：Remove/Response 的编号不存在。

相同的操作序列重放得到完全相同的序、响应时间与重排标记
（规则对编号字节序确定唯一，无随机/map 遍历依赖）。

### 本地验证

```bash
# 全量测试（2000 组随机操作序列与朴素模拟 + 全排列穷举对拍）
go test ./...

# 竞态检测 + 对拍日志（输入、输出与判定依据逐操作打印）
go test -race -v ./rta/

# 只看对拍
go test -run TestRandomDifferential -v ./rta/
```

`rta/diff_test.go` 内的朴素参考实现与生产代码独立：w 自 `C+B` 起
逐 1 递增求最小满足 `demand(w) ≤ w` 者；任务集 ≤ 6 时穷举全部排列，
核对 Add 失败当且仅当不存在可行全序，并验证 Audsley 成功所得序确实
全员可调度。
