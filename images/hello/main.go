// Command hello is a demo image: it greets and exits.
package main

import (
	"fmt"
	"os"
)

func main() {
	name := "world"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	fmt.Printf("Hello, %s! This container is a WebAssembly module.\n", name)
	if v := os.Getenv("GREETING"); v != "" {
		fmt.Println("GREETING =", v)
	}
}
