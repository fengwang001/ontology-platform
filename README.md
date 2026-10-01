# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## zstore：只用存储块的 zlib 容器

`zstore` 包提供流式编码器（`Encoder`）与解码器（`Decoder`），
容器内只使用存储（stored）块，块切分、头部、块头校验与
Adler-32 尾部的字节表示均可精确复现，且解码结果与输入切分方式无关。

### 容器格式

```
+----------+-------------------+-----------+
| 2 字节头 | 若干存储块         | 4 字节尾部 |
| 78 01   | （见下）           | Adler-32  |
+----------+-------------------+-----------+
```

- 头部固定为 `78 01`（CM=8、CINFO=7、FCHECK 使 16 位大端值被 31 整除、无字典）。
- 每个存储块：1 字节块头（bit0=BFINAL，bit1-2=类型 00，高 5 位为 0）、
  2 字节小端 LEN、2 字节小端 NLEN（必须等于 LEN 按位取反）、LEN 字节数据。
- 尾部为已解出数据的 Adler-32，4 字节大端。

### 块切分规则

- 编码器缓存写入数据，每满 65535 字节输出一个**非终块**（块头 `00`）。
- `Close` 把剩余数据（可为 0 字节）作为**终块**（块头 `01`）输出，再输出尾部。
- 输出与 `Write` 的切分无关；空输入输出固定为
  `78 01 01 00 00 FF FF 00 00 00 01`。
- 非终块的 LEN 可为 0；终块之后必须恰好 4 字节尾部，不得再有字节。

### Adler-32 定义

`a=1`、`b=0` 起，每个字节先 `a=(a+字节) mod 65521`，再 `b=(b+a) mod 65521`，
结果为 `b<<16 | a`。校验值：字符串 `Wikipedia` 为 `0x11E60398`。

### 解码校验顺序

头部按序只报第一个错误：压缩方法（低 4 位）不为 8 → 窗口信息（高 4 位）大于 7 →
头两字节大端 16 位数不被 31 整除 → 字典标志位（0x20）置位。
块级错误依次为：块头高 5 位非零、类型 01/10 不支持、类型 11 保留、
NLEN 不是 LEN 的补码；尾部错误为尾部后多余字节、Adler-32 不符；
`Close` 时未读完尾部报截断错误。任一错误后进入粘滞失败态，
之后调用一律返回 `ErrPoisoned` 且不改变状态。
数据块内容边收边交付，已交付字节数可用 `Delivered()` 查询。

### 本地验证

```bash
# 全部测试（日志打印输入、输出与判定依据）
go test -v ./zstore

# 竞态检测
go test -race ./zstore

# 指定用例，例如空输入与切分一致性
go test -v -run 'TestEmptyInput|TestAllSplitPoints' ./zstore
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
