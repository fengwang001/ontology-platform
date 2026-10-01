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

## GIF 变宽 LZW（`lzw` 子包）

`lzw` 包实现 GIF 风格的变宽码字 LZW 流式编码器（`Encoder`）与解码器
（`Decoder`），码按低位在先打包进字节。

### 编码规则

- 符号为字节 `0..255`；清除码 `256`、结束码 `257`；新增条目从 `258`
  起编号，最大 `4095`；码宽初始 `9`、最大 `12`。
- 第一个输出码是清除码（9 位）。维护当前串 `w`：
  - `w+c` 在字典则 `w = w+c`；否则输出 `w` 的码、新增条目 `w+c`
    （编号取下一个空闲值）、`w = c`。
  - 新增条目 `e` 恰等于 `2^当前码宽` 且码宽 `<12` 时，**此后**码宽
    `+1`：条目 512 → 10 位、1024 → 11 位、2048 → 12 位。
  - 新增条目 `e=4095` 后紧接输出清除码（12 位），字典复位、码宽
    回到 9，`w` 保持为 `c`。
- `Close`：若 `w` 非空先输出其码；把下一个空闲编号“补计”为已占用
  并做同样加宽判定（无内容、不会被引用）；输出结束码；用 0 位补齐
  到整字节。
- 例：`AAAAAAA` → `256,65,258,259,65,257`，均为 9 位。

### 解码端码宽时机的推导

不另行约定，直接由编码规则推出：任何“非清除后首码”的数据码 `D`
被写出，必伴随一次 `w+c` 不匹配，条目编号在写码的同一步被占用；
`Close` 时最后一个数据码之后还会补计一个空闲编号，结束码按补计后
的码宽输出。因此解码端读取“清除后首码之外的任何码”时一律按
`widthFor(free+1)` 取码：

- 读到数据码：把新条目回填到编号 `free`（码值等于 `free` 即合法的
  自引用 `KwKwK`，大于 `free` 非法），随后 `free++`；
- 读到结束码：`free` 仅为 Close 补计，无内容；
- 表满（4095 已建）后下一个码必须是 12 位清除码，否则按越界拒绝。

### 错误模型

按流中先出现者报错，均可 `errors.Is` 区分：`ErrNotCleared`、
`ErrCodeAfterClear`、`ErrCodeOutOfRange`、`ErrPaddingNonZero`、
`ErrTrailingData`、`ErrTruncated`；错误包装为 `*CodeError`，携带
出错码的序号（从 1 起，含清除码），可用 `errors.As` 取得。解码失败
后进入粘滞失败态，后续调用返回同一原因且不改状态；编码器 `Close`
后再 `Write` 或重复 `Close` 返回 `ErrClosed`，被拒绝调用不改状态。

### 本地验证

```bash
# 单元测试（含与逐步朴素实现的随机对照、码宽边界、非法码流）
go test -v ./lzw

# 竞态检测（覆盖编码器/解码器并发调用）
go test -race -count=2 ./lzw

# 覆盖率
go test -coverprofile=/tmp/lzw.cover ./lzw
go tool cover -func=/tmp/lzw.cover
```

测试覆盖：`AAAAAAA` 示例、258/259 自引用、511/512 与 2047/2048
附近升宽、字典写满触发清除后继续、空输入、Close 补计恰好触发升宽
（`buildPairs(127)` → 结束码 10 位）、六类非法码流及粘滞性、
Write 任意切分（含逐字节）逐字节一致、200 组随机输入与朴素实现对照、
并发读写安全；`-v` 日志打印输入、码序列（`码/位宽`）与判定依据。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
