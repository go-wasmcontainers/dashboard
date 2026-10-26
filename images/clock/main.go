// Command clock is a demo image: it prints a coloured, numbered line every
// second (or every -interval), forever or -n times, so there are logs to
// follow and a container to stop.
package main

import (
	"flag"
	"fmt"
	"time"
)

func main() {
	n := flag.Int("n", 0, "lines to print (0: forever)")
	every := flag.Duration("interval", time.Second, "time between lines")
	flag.Parse()
	colours := []int{31, 32, 33, 34, 35, 36}
	for i := 1; *n == 0 || i <= *n; i++ {
		fmt.Printf("\x1b[%dmtick %d\x1b[0m %s\n", colours[i%len(colours)], i, time.Now().UTC().Format(time.TimeOnly))
		time.Sleep(*every)
	}
}
