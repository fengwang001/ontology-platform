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

## 变更日志按键压缩合并器（`ontology/changelog`）

只追加日志（序号从 1 连续递增），可对闭区间 `[left, right]` 按键压缩：

- 可见性：`ReadAt(key, pos)` 返回所有 `seq <= pos` 条目中序号最大者；无可见
  条目返回“不存在”（`ok=false, err=nil`），不是错误。
- 区间外条目原样保留；区间内同键多条写入只保留最后一条，合并为压缩记录
  （`Compacted=true`，沿用保留条目的原序号与最后一次写入值）；区间内没有写入
  的键不产生压缩记录；压缩后合法位点 `1..NextSeq()-1` 全部仍可寻址。
- 边界：合法区间满足 `1 <= left <= right <= NextSeq()-1`；左边界小于一
  （`ErrCompactLeftTooSmall`）、右边界超过最新序号（`ErrCompactRightTooLarge`）、
  左右倒置（`ErrCompactInverted`）分别返回可区分错误；空键 `ErrEmptyKey`、
  位点越界 `ErrPositionOutOfRange`。任何失败都先校验、后变更，不改变日志与
  位点映射。
- 并发：状态为不可变快照 + 原子替换，`ReadAt`/`Verify` 无锁，可在追加与压缩
  期间并发读取；并发读结果与串行重放参照逐值相同（`-race` 单测验证）。

本地验证（批量参照只看区间内条目，逐位点核对）：

```bash
go test -race -v ./ontology/changelog/
go test -race -count=50 -run TestConcurrent ./...
```

详细说明见 `ontology/changelog/README.md`。
