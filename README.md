# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 文档补丁（`patch` 包）

`patch` 包在两个版本的 JSON 兼容文档之间生成最小有序差异补丁，并由下游按序、
原子地应用补丁以还原目标版本。入口：

- `patch.Generate(source, target, ...Option) (Patch, error)`：生成差异补丁。
- `patch.Apply(doc, patch) (any, error)`：在深拷贝上原子应用，失败整体拒绝。
- `patch.SelfCheck(ctx, source, target, ...Option) (Patch, error)`：生成并应用，
  校验往返深度相等且输入不被修改。

三者不持有包级可变状态，可被多个执行体并发调用。

### 路径编码（JSON Pointer 风格）

- 路径由 `/` 分隔的若干段组成，空字符串 `""` 表示文档根。
- 段内转义顺序固定：`~` 写成 `~1`，`/` 写成 `~0`（先转义 `~` 再转义 `/`）；
  解码时仅接受 `~0`、`~1`，其他 `~x` 或悬空 `~` 均为非法路径。
- 空段不合法（`/a//b`），对象空键无法编码，生成时整体拒绝。
- 数组下标段必须是十进制非负整数且无前导零：`0`、`12` 合法，`01`、`-1`、
  `1.5` 非法（归类为 `ErrInvalidPath`）。

### 生成规则

从根开始递归比较同路径上的两个值：

1. 两值深度相等（含 `null` 与缺失键的区分）：不产生任何操作。
2. 两侧都是对象：取键的并集，按字节序逐键处理。
   - 仅源侧有：生成 `remove`；仅目标侧有：生成 `add`；两侧都有：递归。
3. 其余情况——类型不同、任一侧是数组、标量不等——整体生成一条 `replace`，
   绝不深入数组内部做逐元素 diff。
4. 操作条数达到上限（默认 `DefaultMaxOps = 10000`，可用 `WithMaxOps` 覆盖）
   即停止并整体返回 `ErrPatchTooLarge`。

键序确定、无相等冗余条目，因此补丁最小、有序、可复现。
`WithDecisionLogger` 可回调每一步的输入路径、两侧值类型、判定依据
（`equal` / `object` / `add` / `remove` / `replace`）与产生的补丁条目，
便于日志审计。

### 应用规则

按补丁顺序逐条执行：

- 应用前对调用方文档做深拷贝，所有修改发生在副本上；只有全部成功才返回结果，
  因此成功或失败，调用方的文档与补丁都保持深度不变（失败不留痕）。
- `add`：对象键存在与否均可设置；数组按下标插入（允许等于长度的尾后下标）。
- `replace`：对象键与数组元素必须已存在，否则路径不存在；根路径仅支持整体
  `replace`，根上 `add`/`remove` 为非法操作。
- `remove`：对象键或数组元素必须存在；数组删除后下标整体前移。
- 中间段必须存在：下钻途中任何一段缺失、越过标量，或父级不是对象/数组，
  都会在执行该条之前失败。
- 任一条失败即整份补丁失败；补丁条目数超过上限同样在执行前拒绝。

### 边界与错误类别

所有错误均为哨兵错误，互相不同且可用 `errors.Is` 精确区分：

| 错误 | 触发场景 |
| --- | --- |
| `ErrInvalidPath` | 路径不以 `/` 开头、空段、非法转义、数组下标含前导零/负数/非数字、空键 |
| `ErrPathNotFound` | 中间段或目标键/下标不存在、越过标量继续下钻 |
| `ErrInvalidOperation` | 未知操作类型、根路径 `add`/`remove` |
| `ErrPatchTooLarge` | 生成或应用的操作条数超过上限 |
| `ErrUnsupportedValue` | 文档含 JSON 兼容范围之外的值类型（如 `chan`、函数、指针） |

支持的值类型：`nil`、`string`、`bool`、有/无符号整数与浮点数、
`map[string]any`、`[]any`。`null` 值键与缺失键严格区分：
缺失 → `null` 产生 `add`，`replace` 缺失键会失败。

### 本地验证

```bash
# 全量测试（竞态检测）
go test -race -v ./patch/

# 覆盖率
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
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
