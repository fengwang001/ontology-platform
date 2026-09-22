package serve

import "ontology/multipart"

func contentTypeBoundary(b string) string {
	return multipart.ContentType(b)
}
