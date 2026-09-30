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

## 证书吊销状态响应缓存（`revocation` 包）

缓存合并乱序到达的证书状态响应，并按有效区间判定证书当前是否可信。
每个证书只保留一条合并记录，合并结果与响应到达顺序无关。

### 数据模型

- 响应：证书序号、状态（正常 / 暂扣 / 吊销）、生效时刻 `a`、下次更新时刻 `b`（要求 `a < b`）。
- 记录：每个证书一条 `{状态, a, b}`。

### 合并规则（与到达顺序无关）

- 吊销为终态：一旦任一响应为吊销，记录即为吊销，`a` 取所有吊销响应中最小的 `a`；
  此后只有更早生效（`a` 更小）的吊销响应会把 `a` 改小，其余响应一律不改变记录。
  因此已吊销证书永远不会因任何较旧或较新的「正常」响应恢复信任。
- 双方均非吊销时：`a` 较大的响应胜出；`a` 相等时暂扣胜正常；
  状态与 `a` 都相同时取较小的 `b`；记录的 `b` 取胜出响应自身的 `b`。

顺序无关的理由：非吊销分支的比较（先比 `a`，再比状态优先级，再比 `b`）构成全序，
合并等价于在该全序下取最大元，满足结合律与交换律；吊销分支等价于对吊销响应的
`a` 取最小值且优先级高于一切非吊销响应，同样与顺序无关。`TestPermutationConsistency`
对一批响应的全部 720 种到达顺序验证了合并结果完全一致。

### 判定规则

- 无记录 → 未知（不可信）；吊销 → 已吊销（不可信）；暂扣 → 已暂扣（不可信）。
- 正常记录仅当当前时刻 `< b` 才可信，否则报「状态已过期」（有效区间为 `[a, b)`，
  恰在 `b` 时刻即过期）。
- 任一判定一旦报出已吊销，其后所有判定都不可信（吊销为终态，记录不可回退）。

### 拒绝规则

响应按以下顺序校验，只报第一个原因并整体拒绝，被拒绝的响应不改变任何记录：

1. 证书序号为空；2. 状态非法；3. `a` 不小于 `b`；4. `a` 晚于当前时刻（来自未来）；
5. 容量已满且无可淘汰记录。

### 容量与淘汰

- 缓存最多容纳 `M` 个证书；仅当新证书到达且缓存已满时才尝试淘汰。
- 只淘汰已过期的正常记录（当前时刻 `>= b`），取 `b` 最小者，并列取序号字典序小者。
- 吊销与暂扣记录、未过期的正常记录均不被淘汰；无可淘汰记录时拒绝新证书的响应
  （已有记录的证书仍可正常合并）。

### 并发

提交（`Submit`）与判定（`Judge`）均可并发调用，内部由互斥锁保护；
时钟可通过 `WithClock` 注入，便于测试与确定性复现。

### 本地验证

```bash
# 全部测试（含竞态检测与详细日志，日志打印输入、输出与判定依据）
go test -race -v ./revocation/

# 关键用例
go test -v -run 'TestGoodThenOlderRevoked|TestRevokedThenNewerGood' ./revocation/
go test -v -run 'TestPermutationConsistency|TestEviction|TestExpiredExactlyAtB' ./revocation/
```
