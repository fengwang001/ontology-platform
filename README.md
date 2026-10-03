# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## SRP 栈资源策略启动闸门（`srp` 包）

`srp` 包实现了多单元资源的栈资源策略（Stack Resource Policy）启动闸门，
接口为 `DeclareResource(编号, N)`、`AddTask(编号, D, μ 表)`、`RemoveTask(编号)`
与作业操作 `Start` / `Acquire` / `Release` / `Finish`，查询为
`SysCeil` / `Ceil` / `Avail` / `Stack` / `Level`。所有操作与查询都可并发调用，
内部以一把互斥锁串行化，结果等价于某个串行顺序；相同操作序列重放结果完全相同。

### 抢占层级 π 的推法

取当前全部任务的相对截止期 D 的**互不相同**取值，按从大到小排序：
D 最大者 π=1，其次 π=2，依此类推；D 相同的任务 π 相同。
`AddTask` / `RemoveTask` 之后 π 会按上述规则整体重推，
已运行的作业栈不受影响（栈内任务的相对次序在重推后保持不变）。

### 天花板与严格不等号

- 资源天花板 `Ceil(r) = max{ π_k : μ_{k,r} > avail_r }`，**严格大于**：
  μ 恰等于 avail 不进入天花板，大 1 才进入；没有满足条件的任务时为 0。
- 系统天花板 `SysCeil` 为各资源天花板的最大值；无资源时为 0。
- 天花板随 `avail` 动态变化：取资源后可能升高，归还后相应下降。

### 栈次序规则

- 运行中的作业构成栈，栈顶为正在运行者；任何时刻栈中作业所属任务的 π
  自栈底到栈顶严格递增。
- `Start(作业, 任务)` 是唯一的启动闸门：要求该任务的 π **严格大于**栈顶作业
  所属任务的 π（栈空则无此要求），且**严格大于**当前 `SysCeil`，通过则压栈。
  作业一旦启动，其取放资源不再被他人阻塞。
- `Acquire` / `Release` / `Finish` 均要求作业为栈顶；`Finish` 还要求作业
  不持有任何资源，通过则弹栈。
- 不变式：任意时刻 `avail + 全部作业持有量 = N`；在每个作业都遵守自己 μ 声明的
  合法操作序列下，`Acquire` 的"单元不足"永不出现（内部断言计数器恒为 0）。

### 拒绝原因

按固定优先级只报第一个：参数非法 → 不存在 → 编号重复 → 容量已满 → 非栈顶 →
抢占层级不足（Start，π 不大于栈顶）→ 天花板拦截（Start，π 不大于 SysCeil）→
超出声明（Acquire）→ 单元不足（Acquire）→ 未持有（Release）→ 仍持有资源
（Finish）→ 任务在用（RemoveTask）。被拒绝的操作不改变余量、栈、持有量与 π。

### 本地验证

```bash
# 单元测试 + 2000 组随机合法/非法序列与朴素模拟器对拍
go test ./srp

# 查看对拍日志（每个操作的输入、输出与判定依据）
go test -v -run TestDifferentialRandom ./srp

# 竞态检测
go test -race ./srp
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
