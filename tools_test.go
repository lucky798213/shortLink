package main

import (
	"fmt"
	"short_url/pkg/generator"
)

func main() {
	fmt.Printf("ID=3521614606208 → %q\n", generator.Encode(3521614606208))
}
