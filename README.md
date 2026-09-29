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

## 对象属性加密（`encryption` 包）

对敏感属性做字段级加密存储、读取时解密，位于 `encryption/`，仅依赖 Go 标准库。

### 加密规则

- 算法为 AES-256-GCM；敏感字段在 `Store.Put` 时加密，`Store.Get` / `Query` / `SortAll` 时解密。
- 每次加密都通过 `crypto/rand` 生成一次性 12 字节随机 nonce；同一明文、同一密钥的两次加密必然产生不同密文，密文因此不泄露“明文是否相同”的语义。
- 密文以自描述信封落库（`magic | envelope 版本 | 密钥版本 | nonce 长度 | nonce | GCM 密文`，前缀 `ENV1`），不含任何密钥材料。
- 非敏感属性原样存储；查询与排序一律在内存中对**解密后的明文**比较，禁止对密文直接比较。
- 密钥与数据分离：密钥只存在于 `KeyRing`（生产环境可通过 `AddExternalKey` 注入 KMS 托管密钥），存储层只看到密文与密钥版本号。
- 日志只打印对象 ID、密文摘要、nonce、密钥版本与“判定依据（basis）”，不打印明文。

### 密钥轮换规则

- `KeyRing.Rotate()` 只**新增**当前密钥版本，历史版本全部保留；旧密文按信封中的版本号选择旧密钥解密，因此轮换后旧数据始终可读。
- 轮换后新写入自动使用最新版本；可调用 `Store.Rewrap(ctx)` 用旧密钥解密、新密钥重加密历史数据（明文不变，信封版本更新）。
- `KeyRing.RetireKey` 删除任何在用版本都会返回 `ErrKeyRotationBreaksOldData` 并拒绝执行，密钥环与数据均不改变。
- 密文引用的密钥版本不存在时，`Open` 返回可区分的 `ErrKeyNotFound`（包装错误，`errors.Is` 可判定）。

### 明确拒绝的用法

三类危险操作返回**可区分**的哨兵错误，且拒绝发生在任何写入之前，被拒绝的操作不改变数据：

- `ErrDeterministicEncryption`：`Put(ctx, obj, deterministic=true)` 要求相同明文产生相同密文。
- `ErrCiphertextOperation`：查询/排序谓词设置 `OnCiphertext: true`，要求直接对密文比较或排序。
- `ErrKeyRotationBreaksOldData`：轮换试图丢弃旧版本密钥，导致旧密文不可解。

### 使用示例

```go
ring := encryption.NewKeyRing()
ring.GenerateKey()                 // v1 初始密钥
store := encryption.NewStore(encryption.NewCodec(ring),
    []string{"ssn", "email"},
    encryption.SlogLogger{Logger: slog.Default()},
)

store.Put(ctx, encryption.Object{ID: "u1", Attributes: map[string][]byte{
    "ssn":   []byte("110-..."),
    "email": []byte("a@x.com"),
}}, false)

obj, _ := store.Get(ctx, "u1")                               // 返回明文副本
hits, _ := store.Query(ctx, encryption.Predicate{            // 解密后比较
    Attribute: "email", Equals: []byte("a@x.com"),
})
ordered, _ := store.SortAll(ctx, encryption.SortOrder{Attribute: "ssn"})

ring.Rotate()            // 新增 v2，保留 v1；旧数据仍可解
store.Rewrap(ctx)        // 可选：把旧密文迁移到 v2
```

并发：`Codec`/`Store` 均为无共享可变状态（`KeyRing`、`Store` 内部用读写锁保护），可被多 goroutine 同时调用；同一对象并发加解密结果一致，同一密钥并发加密同一明文得到互不相同但都可解的密文。

### 本地验证

```bash
# 全量测试（含随机 nonce、解密后查询/排序、轮换兼容、拒绝路径、并发）
go test -v ./encryption/

# 竞态检测 + 覆盖率
go test -race -coverprofile=coverage.out ./encryption/
go tool cover -func=coverage.out

# 全仓库
go test -race ./...
gofmt -l . && go vet ./...
```
