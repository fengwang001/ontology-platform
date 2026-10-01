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

## 稳定匹配器（`matching` 包）

`matching` 包实现带名额的双边稳定匹配：申请人（applicant）与项目（program）
各有独立的正整数编号空间，通过 `AddApplicant(id, prefs)` /
`AddProgram(id, cap, prefs)` 登记严格偏好列表（越靠前越喜欢，只列愿意接受的对象）。
申请人 `a` 与项目 `p` **互相可接受**，当且仅当 `a` 的列表含 `p` 且 `p` 的列表含 `a`。

### 延迟接受流程（申请人提议）

`Run()` 先校验所有偏好引用，再冻结登记并计算：

1. 待提议队列初始为全部申请人，按编号升序。
2. 循环取队首申请人 `a`：若其列表已全部提议过，则 `a` 保持未匹配；
   否则 `a` 向列表中下一个未提议的项目 `p` 提议（计一次提议）。
3. 若 `a` 与 `p` 不互相可接受，`a` 被拒并回到队尾。
4. 否则 `p` 暂收 `a`；若暂收人数超过 `cap`，踢出暂收者中按 `p` 的偏好
   最靠后者（可能就是 `a` 自己），被踢者回到队尾。
5. 队列为空时结束。`Result()` 返回每个申请人的项目（未匹配为 `0`）与总提议次数。

该结果满足：每个项目人数不超过 `cap`、全部配对互相可接受、无阻塞对，
且为**申请人最优**稳定匹配；总提议次数等于每个申请人的提议数之和
（已匹配者为其项目在列表中的位置，从 1 起；未匹配者为其列表长度）。
冻结后再次调用 `Run()` 直接返回同一结果，不重算。

### 阻塞对定义

`Verify(m)` 核验给定方案 `m`（申请人到项目的映射，缺省或 `0` 为未匹配）。
`(a, p)` 是**阻塞对**，当且仅当：

- `a` 与 `p` 互相可接受；
- `a` 在 `m` 中未匹配，或更喜欢 `p` 胜过其当前项目；
- `p` 的当前人数小于 `cap`，或 `p` 更喜欢 `a` 胜过其当前申请人中最靠后者。

返回的阻塞对列表按 `(a, p)` 升序；空列表表示方案稳定。

### 错误优先级

所有失败均只报第一个原因，可用 `errors.Is` 区分：

- `AddApplicant` / `AddProgram`：已冻结（`ErrFrozen`）→ 编号小于 1（`ErrInvalidID`）
  →（项目）`cap` 小于 1（`ErrInvalidCapacity`）→ 编号已存在（`ErrDuplicateID`）
  → 偏好含小于 1 的项（`ErrInvalidPreference`）→ 偏好含重复项（`ErrDuplicatePreference`）。
- `Run`：偏好引用未登记编号时报 `ErrUnknownReference`（按申请人编号升序、再按
  项目编号升序、各自按列表次序取第一处），此时不冻结、不改变任何状态。
- `Result` / `Verify`：未 `Run` 时报 `ErrNotRun`。
- `Verify`：方案引用未知申请人或项目（`ErrUnknownMember`）→ 配对不互相可接受
  （`ErrNotMutuallyAcceptable`，按申请人升序取第一处）→ 项目超过名额
  （`ErrOverCapacity`，按项目升序取第一处）。

被拒绝的操作不改变登记与结果。所有方法可并发调用，效果等价于某个串行顺序；
任意打乱登记顺序、或重放相同登记序列，都得到逐字段相同的结果。

### 本地验证

```bash
# 全部匹配器测试（含 2000 组随机输入的暴力枚举对拍）
go test ./matching/

# 查看随机对拍日志（输入、输出与判定依据）
go test ./matching/ -run TestRandomApplicantOptimal -v

# 竞态检测
go test -race ./matching/
```
