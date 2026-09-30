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

## 时间步一次性口令验证器（TOTP + 水位防重放）

实现位于 `otp.go`，测试位于 `otp_test.go`。

### 注册参数与状态

- `Register(user, P, w, D)`：`P` 为步长（秒，正整数），`w` 为容忍半径，`D` 为漂移上限（步），约束 `0 ≤ w ≤ D`。
- 口令由外部确定性函数 `CodeFunc(user, step)` 给出，验证器只做比较（构造时注入，见 `NewVerifier`）。
- 当前步 `c = floor(now / P)`；每个用户维护偏移估计 `d`（初值 `0`）与已用水位 `u`（初值 `-1`）。

### 窗口规则

每次验证的搜索窗口是两个区间的交集：

- 容忍窗口：`[c + d - w, c + d + w]`
- 漂移硬界：`[c - D, c + D]`

即 `lo = max(c+d-w, c-D)`，`hi = min(c+d+w, c+D)`。

### 判定顺序与水位规则

1. 用户未注册 → `OutcomeUserNotFound`
2. 口令为空 → `OutcomeEmptyCode`
3. 从 `lo` 到 `hi` 升序扫描：窗口内序号大于 `u` 的步为**候选步**；取候选中序号最小的匹配步，成功：`u = 该步`，`d = 该步 - c`（漂移校正，窗口中心随之移动）。
4. 无候选匹配，但窗口内存在匹配步（必然不大于 `u`）→ `OutcomeCodeUsed`（口令已使用）。
5. 窗口内不存在任何匹配步 → `OutcomeCodeWrong`（口令错误）。

水位 `u` 单调不减；一旦某个时间步成功使用，该步及更早步的口令永远不能再次通过（即使更早步从未被直接提交过——例如设备快一步成功后，当前步口令立即失效）。

### 注册拒绝原因（按此顺序只报第一个）

- `ErrUserExists`：用户已存在
- `ErrInvalidPeriod`：`P ≤ 0`
- `ErrNegativeWindow`：`w < 0`
- `ErrDriftBelowWindow`：`D < w`

被拒绝的操作不会写入或修改任何用户的 `d` 与 `u`。

### 并发与确定性

- 整个判定在互斥锁内完成，`Verify` 可并发调用；同一用户同一口令并发提交恰有一个通过，其余得到 `OutcomeCodeUsed`，任意交错下 `u` 单调不减，不同用户互不影响。
- 相同的操作序列与相同时钟序列产生相同结果；测试通过 `SetClock` 注入固定时钟保证可重复。
- 每次注册/验证均打印输入、输出与判定依据（`basis=...`），可用 `SetLogger` 重定向或关闭。

### 本地验证

```bash
# 全部测试（含竞态检测；关键用例见 otp_test.go）
go test -race -v ./...

# 反复跑并发用例，排查交错问题
go test -race -count=20 ./...

# 单个用例
go test -race -run TestConcurrentSameCodeExactlyOnePass -v ./...
```

测试覆盖：时钟快一步后当前步口令被拒、漂移校正后窗口中心移动、恰在漂移上限内外（含负方向）的两个口令、已使用与错误可区分且失败不改状态、同一口令并发恰一个通过、交错提交水位单调、用户隔离、原因报告顺序、注册拒绝原因、确定性双跑对比，以及判定日志内容。
