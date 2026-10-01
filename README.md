# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 字节码汇编器

`assembler` 包实现带分支松弛的字节码汇编器：

- `PAD n` 占 `n` 字节，`n` 必须满足 `1 <= n <= 1000`。
- `JMP label` 短形式 2 字节、长形式 5 字节。
- `JZ label` 短形式 2 字节、长形式 6 字节。
- 标签绑定到定义之后下一条追加指令的起始地址；定义在末尾时绑定到当前总长度。
- 跳转偏移为 `目标地址 - (跳转起始地址 + 当前形式长度)`，也就是从跳转末尾或下一条指令起始位置开始计算。
- 短形式只接受闭区间 `[-128, 127]`；`-129` 和 `128` 都必须变长。

### 松弛迭代

汇编是不修改状态的纯计算，规则如下：

1. 所有跳转先假设为短形式。
2. 根据当前形式顺序排布，计算每条指令的起始地址、长度和跳转偏移。
3. 同一轮内找出所有偏移不在短形式范围内的短跳转，下一轮统一改成长形式。
4. 长形式不会缩回短形式；重复第 2-3 步直到某一轮没有提升，得到最小不动点。
5. 对同一序列重复汇编得到完全相同的地址、形式、偏移、逐轮提升集合和总长度。

`AssembleRounds` 额外返回每一轮的布局与提升下标，便于精确复现变长连锁。

### 错误语义与优先级

- `PAD n` 的 `n` 越界返回可通过 `errors.Is(err, ErrInvalidPadSize)` 判定的错误。
- 定义空标签返回 `ErrEmptyLabel`；该校验先于重复标签校验。
- 重复定义标签返回 `ErrLabelDefined`。
- 汇编时按指令下标从前到后检查，多个未定义引用只报最前的一处；错误类型为 `UndefinedLabelError`，同时包装 `ErrUndefinedLabel`。
- 追加或定义标签失败不会改变已有序列；汇编失败只基于快照计算，不会留下半成品布局。
- `Assembler` 内部使用读写锁，`Snapshot` 和汇编结果均为独立拷贝；并发追加、定义标签、汇编、查询等价于某个合法串行顺序。

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

# 汇编器边界、连锁、错误优先级与竞态验证
go test -race -v ./assembler

# 如果系统 Go 构建缓存目录只读，可指定临时缓存
GOCACHE=/tmp/ontology-go-cache GOMODCACHE=/tmp/ontology-go-modcache go test -race -v ./assembler

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
