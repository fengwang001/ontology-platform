package diff

// fingerprint.go 定义快照各组成部分的规范化指纹（FNV-1a 64bit）。

func fingerprintObject(o Object) uint64         { return 0 }
func fingerprintLink(l Link) uint64             { return 0 }
func fingerprintRemoval(r RemovalRecord) uint64 { return 0 }
func fingerprintProperty(p PropertyDecl) uint64 { return 0 }
