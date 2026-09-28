package main

import "ontology/ontology"

func main() {
	_ = ontology.NewBroker(8, nil)
}
