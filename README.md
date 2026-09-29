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

`featureflag` 包实现特性开关的规则集发布与求值：支持前置开关、有序定向规则、
默认放量与百分比分桶；权重调整时老用户不漂移；发布与求值可并发，且一次求值
（含递归前置）永远基于同一版不可变规则集。

### 数据模型

- `SpecSet`：一次发布的完整规则集（开关名 → `FlagSpec`），发布是整体替换。
- `FlagSpec`：`Enabled` 启用标志、`OffVariant` 关闭变体、`Variants` 声明的全部变体、
  `Prerequisites` 有序前置、`Rules` 有序定向规则、`DefaultRollout` 默认放量（`nil` 表示无命中时返回关闭变体）。
- `Rule`：`Conditions` 全部满足时命中；命中后要么返回固定 `Variant`，要么走 `Rollout` 百分比放量。
- 条件算子：`eq` / `ne` / `in`；**所引用属性缺失时该条件不满足**（规则整体不命中）。

### 求值顺序

对开关 `f` 的用户标识求值时，严格按以下顺序短路：

1. `Enabled == false`：直接返回 `OffVariant`，原因 `off`（不检查前置、不看规则）。
2. 按声明顺序求每个前置开关；任一前置对该用户的结果不等于要求的变体，
   立即返回 `f` 的 `OffVariant`，原因 `prerequisite_failed:<前置开关>`。
3. 按声明顺序取首个「所有条件都满足」的规则：
   - 规则指定固定变体：返回该变体，原因 `rule:<规则名>:variant`；
   - 规则指定放量：按桶选变体，原因 `rule:<规则名>:rollout`。
4. 没有规则命中时：有默认放量则按桶选变体（原因 `default:rollout`），
   否则返回 `OffVariant`（原因 `default:off`）。

求值未知开关返回 `*featureflag.EvalError`。结果 `Result` 同时带 `Version` 与 `Reason`。

### 分桶与权重区间

- 桶值 = `FNV-1a64("开关键\n用户标识") mod 10000`，范围 `[0, 9999]`。
  开关键作为盐，同一用户在不同开关上的桶互不相关；同一（开关, 用户）永远落同一桶。
- 放量权重总和必须为 `10000`，按变体**声明顺序**累加形成半开区间：
  第 0 个变体占 `[0, w0)`，第 1 个占 `[w0, w0+w1)`，依此类推。
- **防漂移保证**：把权重从后一个变体挪给前一个变体时，只会扩大前一个区间的右边界，
  其左边界与原覆盖区间不变，因此原先落在前一个变体的用户结果不变；
  被挪动部分的用户从后一个变体迁入前一个变体，不影响任何其他变体。
  因此调整权重时应只做相邻区间的扩缩，不要重新排列变体声明顺序。

### 发布校验与错误优先级

`Publish` 对整个规则集做原子校验，失败返回 `*featureflag.PublishError`，
`Code` 可取以下可区分原因。多因同时成立时**只报第一个**，优先级为：

1. `variant_not_found`：关闭变体、规则/放量引用的变体、或可解析前置要求的变体未在对应开关声明；
2. `invalid_weights`：某放量存在负权重，或权重之和不等于 `10000`；
3. `prerequisite_not_found`：前置开关不在本次规则集中；
4. `prerequisite_cycle`：前置依赖图成环（DFS 检测，错误中带回环路径）。

被拒绝的发布不会编译生效，**当前版本号与生效规则集保持不变**；
每次成功发布版本号 +1（空 Store 初始版本为 0）。

### 前置依赖与版本一致性

- 一次求值在入口处原子获取当前不可变快照，随后递归求值所有前置时**只使用同一快照**，
  因此不会出现「主开关看新版、前置看旧版」的新旧混用。
- 发布通过 `atomic.Pointer` 整体替换快照：发布返回后开始的求值必然看到新版本；
  发布进行中的求值继续使用旧快照直到结束。发布与求值、求值与求值均可并发。
- 同一版本 + 同一输入（用户标识与属性）反复求值结果完全相同（含变体、版本、原因）。

### 日志

每次求值通过 `featureflag.Logger`（`*slog.Logger`，可用自定义 logger 替换）打印一条结构化日志，
包含输入（版本号、开关、用户标识、属性）、输出（变体、命中原因）与判定依据
（如「前置 b 求出 b_off，要求 b_on」「桶号与变体下标」等）。

### 本地验证

```bash
# 单元测试（含权重挪动、三层前置链、缺属性、成环、错误优先级、并发版本一致性）
go test ./featureflag

# 竞态检测 + 多轮重复（验证并发发布/求值安全）
go test -race -count=10 ./featureflag

# 全量检查
gofmt -l . && go vet ./... && go test ./...
```
