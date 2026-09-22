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

## 向前兼容的消息解析与回写器

本仓库实现一个向前兼容的消息解析与回写器：旧版本解析器不认识新版本
加入的字段，但转发出去的字节必须让下游新版本服务仍能读到这些字段。

### 编码格式

消息是若干字段的序列，每个字段编码为：

```
[字段号 varint][线型 1 字节][长度 varint（仅 bytes/message）][负载]
```

- 线型三种：`varint`(0)、`bytes`(1)、`message`(2，负载为嵌套消息)。
- varint 为标准 7 位一组、最高位续位、小端序编码，最长 10 字节。
- 已知 schema：字段 1（varint，ID）、字段 2（bytes，Name）、
  字段 3（message，Child）。字段号与线型都匹配才算"已知"，
  其余一律按未知字段保留。

### 包划分（依赖方向单向）

- `wire`：varint 与字段头编解码、线型判定、长度校验、错误分类。
- `unknown`：未知字段容器，按到达顺序保留完整原始字节并原样回写。
- `message`：已知结构的解析、编辑与序列化，依赖 `wire` 与 `unknown`。

### 关键推导：回写顺序规则

由不变量 1（解析后回写必须与输入逐字节相同）直接推导：

1. 输入中已知字段与未知字段可以任意交错，且同一字段号可重复出现。
2. 因此回写时**不能**对字段做任何重排——既不能按字段号排序，也不能
   把未知字段集中到尾部；任何重排在交错输入下都会破坏字节等价。
3. 结论：每个字段（无论已知未知）必须在其**原始位置**重新发出。
   实现上，`Message` 维护一个按输入顺序记录已知/未知出现的 slot
   序列；未知字段存完整原始字节原样重发，已知字段未修改时也直接
   重发原始字节（连非规范 varint 都能逐字节保留），只有被显式修改
   的字段才在原地重新编码。
4. 修改已知字段只替换其所在 slot 的内容；删除已知字段只移除其
   slot，其余字段（含全部未知字段）的相对次序不变。

### 错误分类

语法错误以 `*wire.Error` 返回，携带 `Kind` 与绝对字节偏移 `Offset`，
五类彼此可区分（另附截断错误）：

| Kind | 哨兵错误 | 含义 |
| --- | --- | --- |
| `KindVarintOverflow` | `wire.ErrVarintOverflow` | varint 超过 10 字节未结束 |
| `KindUnknownWireType` | `wire.ErrUnknownWireType` | 未知线型编号 |
| `KindLengthOverflow` | `wire.ErrLengthOverflow` | 长度前缀超出剩余字节 |
| `KindFieldNumberZero` | `wire.ErrFieldNumberZero` | 字段号为 0 |
| `KindNestedLengthMismatch` | `wire.ErrNestedLengthMismatch` | 嵌套长度与内部消费不一致 |
| `KindTruncated` | `wire.ErrTruncated` | 输入在字段头或 varint 中途结束 |

任何解析失败都返回 `nil` 消息，不泄漏半结果。资源上限
（`message.Limits`：消息总字节、单字段负载、未知字段条数、嵌套深度，
0 表示不限）在超限点立即拒绝，对应哨兵错误
`ErrMessageTooLarge` / `ErrPayloadTooLarge` / `ErrTooManyUnknowns` /
`ErrDepthExceeded`。

### 演示

```bash
go run ./cmd/demo   # 10 项检查全部 OK，退出码 0
```
