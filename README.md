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

## 对象属性加密（`objectcrypto` 包）

`objectcrypto` 对对象的敏感属性提供“写入加密、读取解密”的字段级加密，密钥与数据分离，密文自带密钥版本。

### 加密与 nonce 规则

- 算法：AES-256-GCM 认证加密；密钥长度固定 32 字节，由 `GenerateKey()` 生成。
- 每次 `Encrypt`/`Put` 都从 `crypto/rand` 生成全新随机 nonce；同一明文每次产生**不同密文**，密文不泄露明文相等性等语义。
- 组件内部按密钥版本记录已用 nonce，随机碰撞会被拒绝（`ErrNonceReuse`），不静默复用。
- 密文为自描述信封 `encv1:<keyVersion>:<base64 nonce>:<base64 密文>`，落盘内容不含明文，可通过 `KeyVersionOf` 读取其密钥版本。
- 读取（`Get`）、查询（`QueryByField`）、排序（`SortByField`）一律**先解密后操作**，绝不直接比较或排序密文。

### 密钥轮换规则

- 密钥保存在独立的 `KeyRing` 中，与加密数据分离；`Rotate(version, key)` 登记新版本并将其设为后续加密的活动版本。
- 轮换只允许版本号单调前进；**所有历史版本密钥都保留**，旧密文按信封中的版本号取对应密钥解密，轮换后旧数据仍可读。
- 替换同一版本的密钥、回退版本号、退役（`Retire`）旧密钥都会被拒绝并返回 `ErrKeyRotatedUnreadable`，且密钥环与数据均不改变。
- 密文引用了密钥环中不存在的版本时，解密返回 `ErrUnknownKeyVersion`。

### 必须拒绝的操作（错误可区分，且不改变数据）

- 要求“相同明文产生相同密文”（确定性加密）：`ErrDeterministicCiphertext`（`RequireDeterministicCiphertext`）。
- 直接对密文做查询/排序：`ErrCiphertextQuery`（`QueryCiphertext`）。
- 密钥轮换后使旧数据不可解（退役/覆盖旧版本）：`ErrKeyRotatedUnreadable`（`RotateKeyUnreadable` / `KeyRing.Retire`）。

三类错误是不同的哨兵错误，可用 `errors.Is` 区分；被拒绝操作前后存储对象与密钥环保持一致。

### 并发

- `KeyRing` 用读写锁保护，`Cipher` 的 nonce 登记表与 `Store` 的记录均有互斥保护，可被多 goroutine 并发调用。
- 并发加密同一明文得到互不相同但均可正确解密的密文；并发读写同一对象时，每次成功读取都能得到一致、可解密的结果。

### 审计日志

- 注入 `Logger`（如 `NewTextLogger(os.Stdout)`）后，每次允许/拒绝都会记录：操作类型、对象 ID、字段、密文（含密钥版本）以及判定依据；明文不写日志。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -v ./objectcrypto

# 覆盖率
go test -cover ./objectcrypto

# 静态检查与格式
go vet ./...
gofmt -l .

# 若默认 GOCACHE/GOPATH 不可写，可指向临时目录：
GOCACHE=/tmp/gocache GOPATH=/tmp/gopath go test -race ./...
```
