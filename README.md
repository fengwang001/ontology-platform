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

## 一次性口令验证器（`otp` 包）

基于时间步的一次性口令验证器，容忍设备时钟偏差并校正漂移。
口令由外部注入的确定性函数 `otp.Func` 按（用户，时间步序号）算出，验证器只做比较。

### 注册

`Register(user, P, w, D)`：步长 `P` 必须为正，`0 <= w <= D`。
用户已存在、`P` 非正、`w` 为负、`D < w` 分别返回可区分的错误，
被拒绝的注册整体不生效，不改变任何已有状态。

### 窗口与水位规则

- 当前步 `c = floor(now / P)`；每个用户维护偏移估计 `d`（初值 0）与已用水位 `u`（初值 -1）。
- 验证窗口为 `[c+d-w, c+d+w]` 与 `[c-D, c+D]` 的交集。
- 窗口内序号大于 `u` 的步为候选，取序号最小的匹配步；
  成功则 `u` 置为该步、`d` 置为 `该步 - c`（漂移校正使窗口中心随之前移）。
- `u` 单调不减：凡序号不大于 `u` 的步的口令永远不能再次通过。

### 已使用与错误的判别

失败原因按 **未注册 → 口令为空 → 已使用 → 口令错误** 的顺序只报第一个：

- 候选中无匹配，但窗口内仍存在匹配步（其序号必然 `<= u`）→ `Used`（口令已使用）；
- 窗口内完全无匹配步 → `Wrong`（口令错误）。
- 验证失败不改变 `d` 与 `u`。

### 并发与确定性

`Verify` 可并发调用：同一用户的验证串行化，同一口令并发提交恰有一个通过；
不同用户持有独立的锁，互不影响；相同的操作与时钟序列得到相同结果。

### 本地验证

```bash
# 运行全部测试（日志打印每次验证的输入、输出与判定依据）
go test -v ./otp

# 带竞态检测
go test -race -v ./otp
```
