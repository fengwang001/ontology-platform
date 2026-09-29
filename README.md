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

## 特性开关规则集（`featureflag` 包）

`featureflag` 包实现特性开关规则集的原子发布与并发求值，位于 `featureflag/`：

- `types.go`：规则集、开关、前置、定向规则、放量与求值结果的类型定义
- `validate.go`：发布前整体校验（错误优先级、环检测、深拷贝隔离）
- `hash.go`：确定性分桶与按累计区间选取变体
- `eval.go`：绑定单一版本快照的递归求值器（含前置链与条件匹配）
- `store.go`：`atomic.Pointer` 持有不可变快照，发布/求值无锁并发

### 规则模型

每个开关（`SwitchDef`）包含：

- `Enabled`：启用标志；为 `false` 时求值**直接**返回 `OffVariant`（不再下钻其前置）。
- `Variants`：显式声明的合法变体名；`OffVariant` 与所有被引用变体都必须在其中。
- `Prerequisites`：有序前置列表，每项要求另一开关对该用户的结果等于 `RequiredVariant`。
- `Targeting`：有序定向规则，每条规则有若干 `Condition`，可指定固定 `Variant` 或 `Rollout`。
- `DefaultRollout`：无规则命中时的默认放量。

### 求值顺序

对开关 `flag`、用户 `userID`、属性 `attrs`：

1. 开关未启用 → 返回关闭变体（原因 `disabled`）。
2. 按序求前置开关；任一前置结果 ≠ 要求变体 → 返回本开关关闭变体（原因 `prerequisite_failed`）。
3. 取**首个条件全部满足**的定向规则：
   - 条件支持 `eq`（属性等于单值）与 `in`（属性属于集合）；
   - **缺少所引用属性视为不满足**，该规则整体跳过；
   - 规则指定固定变体则直接返回（原因 `targeting_rule`），否则走该规则放量（原因 `targeting_rule_rollout`）。
4. 无规则命中 → 走默认放量（原因 `default_rollout`）。

结果 `EvalResult` 含 `Variant`、`Version`、`Reason`、`MatchedRule` 与 `Bucket`。

### 分桶与权重区间

- 放量把「开关键 + 用户标识」做 **FNV-1a 64 位** 哈希（两段之间以 NUL 分隔，避免拼接歧义），
  再对 `10000` 取模得到稳定桶号 `0..9999`。哈希是纯函数，跨进程、跨重启稳定。
- 权重以桶为单位，每个放量的权重之和必须恰为 `10000`。
- 按变体**声明顺序**划分累计区间：`[0, w0)`、`[w0, w0+w1)` …，桶号落入哪个区间就返回哪个变体。
- 调权重不漂移：区间从 0 端累计，因此把权重从**后一个**变体挪给**前一个**变体时，
  原本落在前一个变体区间内的用户桶号与区间前缀都不变，必然仍命中前一个变体；
  只有边界附近、原属后一个变体的用户会被前一个变体「吞并」。

### 前置依赖与版本一致性

- 一次顶层求值创建一个绑定当前快照的 `evaluator`，其全部递归前置求值复用同一快照，
  并按开关记忆化（memo），因此**一次求值连同递归前置只看同一版规则集**，菱形依赖结果也一致。
- 发布是整份规则集的原子替换（`atomic.Pointer.CompareAndSwap`）：
  - **发布返回后开始的求值一定看到新版本**；发布进行中的求值仍完整使用旧版本，不会新旧混用。
- 同一版本与同一输入（用户标识、属性）反复求值结果完全相同（纯函数 + 不可变快照）。

### 发布校验与错误优先级

`Publish` 在替换前对整份规则集校验；多类错误同时成立时，严格按下列顺序只报第一个
（`PublishError` 包装哨兵错误，可用 `errors.Is` 区分，并携带开关名与细节）：

1. `ErrUnknownVariant`：引用了未在所属开关（前置要求则在前置开关）声明的变体
2. `ErrNegativeWeight`：放量权重为负
3. `ErrBadWeightSum`：权重之和不为 10000（空放量同样拒绝）
4. `ErrUnknownPrerequisite`：前置开关不存在
5. `ErrPrerequisiteCycle`：前置依赖成环（DFS 三色检测，错误信息带环路径）

被拒绝的发布**不会**改变当前生效版本；发布时会深拷贝规则集，调用方后续修改入参不影响已发布快照。
求值未知开关返回包装了 `ErrUnknownFlag` 的错误，与发布类错误可区分。

### 日志

求值在 `slog` 中打印输入（开关、用户标识、属性）、输出（变体、原因、命中规则、桶号）
与**判定依据**（本次递归链上每个开关的变体、原因、规则、桶号）及版本号；
发布成功/被拒也分别记录版本与拒绝原因。`NewStore(nil)` 时使用 `slog.Default()`。

### 本地验证

```bash
# 全量测试（含权重挪动一万用户、三层前置链、缺属性、成环、错误优先级、并发版本一致性、日志）
go test -race -v ./featureflag

# 竞态检测 + 重复执行，验证发布/求值并发安全
go test -race -count=3 ./...

# 格式与静态检查
gofmt -l .
go vet ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```
