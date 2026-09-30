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

## ABAC 访问控制（`abac` 包）

基于属性的访问控制组件：按**主体属性 + 资源属性 + 环境上下文 + 策略规则**判定访问。

### 模型

- **属性**：`Attributes`（`map[string]any`），分三个作用域——主体（`ScopeSubject`）、资源（`ScopeResource`）、环境（`ScopeEnvironment`）。
- **条件**：`Condition{Scope, Key, Op, Value}`，对某一作用域中的属性做比较，支持 `eq / neq / gt / gte / lt / lte / in`。
- **策略**：`Policy{ID, Effect, Conditions}`，`Effect` 为 `permit` 或 `deny`，所有条件按 AND 组合，全部满足时策略才匹配。
- **请求**：`Request{Subject, Resource, Environment, Action}`；`Resource == nil` 表示资源不存在。
- **判定**：`Decision{Allowed, Reason, MatchedPolicies}`，`MatchedPolicies` 按策略 ID 排序，构成判定依据。

### 组合与判定规则

- **拒绝优先（deny-overrides）**：任一 `deny` 策略匹配即拒绝（`ReasonDeniedByPolicy`），允许策略无法覆盖拒绝。
- **缺失属性不匹配**：条件引用的属性缺失时，该条件不满足、策略不适用；缺失属性**绝不**被当作假值/零值参与比较（数值比较遇类型不符同样视为不满足）。
- **默认拒绝**：没有任何 `permit` 策略匹配时拒绝（`ReasonNoApplicablePermit`）。
- **不泄露资源存在性**：资源不存在（`Resource == nil`）与"资源存在但无适用允许策略"返回完全相同的判定（`Allowed=false` + `ReasonNoApplicablePermit`），调用方无法通过判定结果探测资源是否存在。
- **确定性**：策略在引擎内按 ID 排序存储与求值，同一组策略以任意顺序注册得到完全相同的判定；`Evaluate` 为纯读操作，可并发调用且结果一致。
- **不可变性**：`Evaluate` 不修改策略集；注册校验失败（空 ID、无条件、非法作用域/运算符/效果、重复 ID）时返回错误且策略集不变。

### 日志

每次判定输出一条结构化日志（`slog` JSON），包含主体、资源、环境、动作、命中策略列表（`matchedPolicies`）、判定结果与原因（`reason`），即完整的判定依据。

### 本地验证

```bash
# 运行 ABAC 测试（属性条件、拒绝优先、缺失属性、存在性不泄露、并发一致性、注册顺序无关）
go test ./abac/ -v

# 并发竞态检测
go test ./abac/ -race -count=1
```
