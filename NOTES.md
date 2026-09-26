# NOTES — 计数器模式流密码推导

key=0x0123456789ABCDEF, nonce=0x1；seed=splitmix64(key^nonce)=0xe821eebbc0778421；block0=splitmix64(seed^0)=0x8a236066f56b86f6，小端密钥流字节 f6 86 6b f5 66 60 23 8a。

| j | 明文字节 | 密钥流字节 | 密文字节 |
|---|---|---|---|
| 0 | 0x61 'a' | 0xf6 | 0x97 |
| 1 | 0x62 'b' | 0x86 | 0xe4 |
| 2 | 0x63 'c' | 0x6b | 0x08 |
| 3 | 0x64 'd' | 0xf5 | 0x91 |
| 4 | 0x65 'e' | 0x66 | 0x03 |
| 5 | 0x66 'f' | 0x60 | 0x06 |
| 6 | 0x67 'g' | 0x23 | 0x44 |
| 7 | 0x68 'h' | 0x8a | 0xe2 |

(甲) 大端取字节时第 0 字节错成 0x8a（block 最高位字节）；正确（小端）是 0xf6。
(乙) 块索引从 1 开始时第 0 块错成 splitmix64(seed^1)=0x84aede9228372122（正确 0x8a236066f56b86f6），第 0 个密钥流字节随之错成 0x22（正确 0xf6）。
(丙) C1^C2=(P1^K)^(P2^K)=P1^P2：同一密钥流抵消，密文异或即两条明文异或；知道任一条明文可逐字节还原另一条，重复片段与空字符模式也直接可见（crib-dragging）。处置：同一 (key,nonce) 第二次 New 必须拒绝（ErrNonceReused），不建会话。

## 不变量（保证位置 / 钉住的测试）

1. 加解密对称：加解密是同一条 XOR 路径 `stream.(*Cipher).xorAt`（Encrypt/Decrypt 都调它）；TestEncryptDecryptSymmetry、TestSessionRoundTrip 钉住。
2. 与朴素参照一致：`stream.(*Cipher).block` 即 splitmix64(seed^i)，xorAt 按小端取字节，与从头顺序生成逐字节相同；TestKeystreamMatchesNaiveReference 钉住。
3. 随机访问一致：xorAt 直接以 (offset+k)/8 定块、不重放前序块；TestEncryptAtMatchesSequential、TestRandomAccessComputesOneBlock（m=100…10000 块数恒 1）钉住。
4. 失败不留痕：`api.New` 先校验 key/nonce，再在 registry 锁内查存在性，被拒不插入；TestRejectedNewLeavesNoTrace 钉住。
