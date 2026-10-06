package revision

import "fmt"

// ObjType 为对象库中对象的四种类型。
type ObjType int

const (
	TypeCommit ObjType = iota
	TypeTag
	TypeTree
	TypeBlob
)

// Object 是对象库中的不可变条目。
type Object struct {
	ID      string
	Type    ObjType
	Tree    string   // 提交的树
	Parents []string // 提交的父（有序，第一父在下标 0）
	Target  string   // 标签指向的对象（可继续指向标签）
}

// Config 控制缩写下限等解析参数。
type Config struct {
	MinAbbrev int // 标识前缀参与标识匹配的最短长度
}

// ObjectDB 是只增不删的对象库：map 负责按完整标识 O(1) 读取，
// hexTrie 负责与对象总数无关的前缀计数与最短缩写计算。
type ObjectDB struct {
	cfg  Config
	objs map[string]Object
	trie *hexTrie
}

// NewObjectDB 创建对象库。
func NewObjectDB(cfg Config) *ObjectDB {
	return &ObjectDB{cfg: cfg, objs: map[string]Object{}, trie: newHexTrie()}
}

// Add 写入对象。重复写入同一对象幂等；同标识不同内容或引用完整性违规则拒绝。
// 调用方（Store）负责持锁，因此这里不做并发保护。
func (db *ObjectDB) Add(obj Object) error {
	if len(obj.ID) != IDFullLen {
		return fmt.Errorf("object id must be %d hex chars", IDFullLen)
	}
	for i := 0; i < len(obj.ID); i++ {
		if _, ok := hexVal(obj.ID[i]); !ok {
			return fmt.Errorf("object id must be hexadecimal")
		}
	}
	if old, exists := db.objs[obj.ID]; exists {
		if !sameObject(old, obj) {
			return fmt.Errorf("object %s already exists with different content", obj.ID)
		}
		return nil
	}
	switch obj.Type {
	case TypeCommit:
		tree, ok := db.objs[obj.Tree]
		if !ok || tree.Type != TypeTree {
			return fmt.Errorf("commit %s references missing or non-tree tree", obj.ID)
		}
		for i, p := range obj.Parents {
			par, ok := db.objs[p]
			if !ok || par.Type != TypeCommit {
				return fmt.Errorf("commit %s parent %d missing or not a commit", obj.ID, i+1)
			}
		}
	case TypeTag:
		if _, ok := db.objs[obj.Target]; !ok {
			return fmt.Errorf("tag %s target missing", obj.ID)
		}
	case TypeTree, TypeBlob:
	default:
		return fmt.Errorf("invalid object type")
	}
	db.objs[obj.ID] = obj
	db.trie.insert(obj.ID)
	return nil
}

func sameObject(a, b Object) bool {
	if a.ID != b.ID || a.Type != b.Type || a.Tree != b.Tree || a.Target != b.Target {
		return false
	}
	if len(a.Parents) != len(b.Parents) {
		return false
	}
	for i := range a.Parents {
		if a.Parents[i] != b.Parents[i] {
			return false
		}
	}
	return true
}

// Get 按完整标识读取对象。
func (db *ObjectDB) Get(id string) (Object, bool) {
	o, ok := db.objs[id]
	return o, ok
}

// countPrefix 返回以 p 开头的对象数（O(len(p))，与库大小无关）。
func (db *ObjectDB) countPrefix(p string) int { return db.trie.countPrefix(p) }

// findByPrefix 返回以 p 为前缀的唯一对象；不唯一或不存在时 ok=false。
// 复杂度 O(IDFullLen)，与对象总数无关。
func (db *ObjectDB) findByPrefix(p string) (Object, bool) {
	id, ok := db.trie.findUnique(p)
	if !ok {
		return Object{}, false
	}
	obj, ok := db.objs[id]
	return obj, ok
}

// shortestAbbrev 返回 id 满足配置下限的最短唯一缩写。
func (db *ObjectDB) shortestAbbrev(id string) (string, bool) {
	if _, ok := db.objs[id]; !ok {
		return "", false
	}
	return db.trie.shortestUnique(id, db.cfg.MinAbbrev), true
}
