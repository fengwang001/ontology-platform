package objectcrypto_test

import (
	"context"
	"fmt"
	"os"

	"ontology/objectcrypto"
)

func ExampleStore() {
	ring := objectcrypto.NewKeyRing()
	key, _ := objectcrypto.GenerateKey()
	if err := ring.Rotate(1, key); err != nil {
		panic(err)
	}
	store := objectcrypto.NewStore(objectcrypto.NewCipher(ring), ring, objectcrypto.NewTextLogger(os.Stdout))
	ctx := context.Background()

	_ = store.Put(ctx, objectcrypto.Object{
		ID:         "customer-7",
		Properties: map[string]string{"phone": "13800001111"},
	})

	if err := store.RequireDeterministicCiphertext(ctx); err != nil {
		fmt.Println("rejected:", err)
	}

	obj, _, _ := store.Get(ctx, "customer-7")
	fmt.Println("plaintext phone:", obj.Properties["phone"])
}
