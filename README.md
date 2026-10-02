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

## matcher：带名额的双边稳定匹配器

`matcher` 包实现申请人提议的延迟接受（deferred acceptance）算法，并提供任意方案的阻塞对核验。

### 登记

- `AddApplicant(id, prefs)`：登记申请人。`prefs` 为项目编号的严格偏好列表（越靠前越喜欢），只列愿意接受的项目。
- `AddProgram(id, cap, prefs)`：登记项目。`cap` 为名额，`prefs` 为申请人编号的严格偏好列表，只列愿意接受的申请人。
- 申请人 `a` 与项目 `p` **互相可接受**，当且仅当 `a` 的列表含 `p` 且 `p` 的列表含 `a`。
- 申请人与项目的编号空间互相独立，均为正整数。

登记拒绝原因（均为可区分的哨兵错误，按序只报第一个，被拒绝的操作不改变任何状态）：

1. `ErrFrozen`：已冻结（`Run` 成功之后）；
2. `ErrInvalidID`：自身编号小于 1；
3. `ErrInvalidCapacity`：（仅项目）`cap` 小于 1；
4. `ErrDuplicateID`：编号已存在；
5. `ErrInvalidPreference`：偏好列表含小于 1 的项；
6. `ErrDuplicatePreference`：偏好列表含重复项。

### 延迟接受流程（Run）

`Run()` 先检查引用：任一偏好列表引用了未登记的项目或申请人时报 `ErrUnknownReference`（按申请人编号升序、再按项目编号升序、各自按列表次序取第一处），此时**不冻结、不改变任何状态**，可补登记后重试。引用齐全则冻结登记并计算：

1. 待提议队列初始为全部申请人按编号升序。
2. 循环取队首 `a`：若 `a` 的列表已全部提议过，则 `a` 保持未匹配并出队；否则 `a` 向列表中下一个未提议的项目 `p` 提议，提议计数加一。
3. 若 `a` 与 `p` 不互相可接受，`a` 被拒并回到队尾。
4. 否则 `p` 暂收 `a`；若 `p` 暂收人数超过 `cap`，踢出暂收者中按 `p` 的偏好最靠后者（可能就是 `a` 自己），被踢者回到队尾。
5. 队列为空时结束。

已冻结后再次调用 `Run` 返回同一结果而不重算。`Result()` 返回每个申请人的项目（未匹配为 `0`）与总提议次数；总提议次数等于每个申请人的提议数之和（已匹配者为其项目在列表中的位置，从 1 起；未匹配者为其列表长度）。任意打乱登记顺序、或重放相同登记序列，均得到逐字段相同的结果。所有方法可并发调用，效果等价于某个串行顺序。

### 阻塞对与 Verify

`Verify(m)` 核验给定方案 `m`（申请人到项目的映射，缺省或 `0` 为未匹配），返回按 `(申请人, 项目)` 升序的阻塞对列表。`(a, p)` 为阻塞对，当且仅当：

- `a` 与 `p` 互相可接受；
- `a` 在 `m` 中未匹配，或更喜欢 `p` 胜过其当前项目；
- `p` 的当前人数小于 `cap`，或 `p` 更喜欢 `a` 胜过其当前申请人中最靠后者。

`Result` 与 `Verify` 在未 `Run` 时报 `ErrNotRun`。`Verify` 的拒绝原因按序只报第一个：

1. `ErrUnknownApplicant` / `ErrUnknownProgram`：方案引用未知申请人或项目；
2. `ErrNotMutuallyAcceptable`：方案中的配对不互相可接受（按申请人升序取第一处）；
3. `ErrOverCapacity`：项目匹配人数超过 `cap`（按项目升序取第一处）。

`Run` 的结果满足：每个项目人数不超过 `cap`、全部配对互相可接受、`Verify` 返回空列表（无阻塞对），且为申请人最优稳定匹配。

### 本地验证

```bash
# 全部测试（含 2000 组随机输入对暴力枚举全部稳定方案的校验）
go test ./matcher/

# 查看随机用例的输入、输出与判定依据日志
go test -v -run TestRandomizedAgainstBruteForce ./matcher/

# 竞态检测
go test -race ./matcher/
```
