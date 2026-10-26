// Command fail is a demo image: it reports an error on stderr and exits
// with status 3, to show an exit code in the dashboard.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("starting...")
	fmt.Fprintln(os.Stderr, "fail: something went wrong (on purpose)")
	os.Exit(3)
}
