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

## 文档补丁（`patch` 包）

对 JSON 风格文档（`map[string]any`、`[]any`、`string`、`float64`、`bool`、`nil`）
的两个版本生成差异补丁，并由下游应用补丁还原目标版本。

### 路径编码

- 路径由 `/` 分隔的段构成，空字符串 `""` 表示根。
- 段内特殊字符按引用规则转义：`~` → `~0`，`/` → `~1`（先转 `~` 再转 `/`，解码反之）。
- 数组下标段必须是无前导零的非负整数（`0`、`12` 合法；`01`、`-1`、`x` 非法）。
- 非法转义（如 `~`、`~2`）在解析时直接拒绝。

### 生成规则（`Generate`）

- 从根递归；两值相等不产生操作。
- 两侧都是对象：对键的并集按字节序逐键处理——只在旧侧则 `remove`，只在新侧则 `add`，两侧都有则递归。
- 其余情况（类型不同、数组不等、标量不等）整体 `replace`。
- 空值键（`nil`）与缺失键严格区分；补丁中所有值为深拷贝，不共享输入引用。

### 应用规则（`Apply`）

- 先整体预校验（操作类型、路径可解析、数量上限），再逐条顺序执行。
- 中间段必须存在且为容器；`add` 允许数组下标等于长度（追加），`replace`/`remove` 必须在界内。
- 任一操作失败则整份补丁失败；全程在深拷贝上执行，调用方文档与补丁不变（原子、可复现）。
- 应用生成结果的深度等于目标版本，应用前后传入文档与补丁深度不变。

### 边界与错误类别

| 类别 | 哨兵错误 | 触发场景 |
| --- | --- | --- |
| 非法路径 | `ErrInvalidPath` | 转义错误、缺前导 `/`、下标有前导零或为负数 |
| 路径不存在 | `ErrPathNotFound` | 中间段缺失、下标越界、`replace`/`remove` 目标不存在 |
| 非法操作 | `ErrInvalidOp` | 未知操作类型、`add` 到已存在的键、删除根 |
| 补丁超限 | `ErrPatchTooLarge` | 操作数超过 `MaxOps`、路径深度超过 `MaxPathDepth`、路径长度超过 `MaxPathLen` |
| 类型不符 | `ErrTypeMismatch` | 向标量内部继续下降 |

所有错误可用 `errors.Is` 区分；任何失败都不在调用方数据上留痕。

### 并发与自检

`Generate`、`Apply`、`Verify` 均为纯函数、无共享状态，可被多个执行体并发调用
（测试以 `-race` 验证）。`Verify` 执行“生成 → 应用 → 深度相等且往返相等”的自检。

### 本地验证

```bash
# 含竞态检测与每步日志（输入、补丁条目、判定依据）
go test -race -v ./patch/
```
