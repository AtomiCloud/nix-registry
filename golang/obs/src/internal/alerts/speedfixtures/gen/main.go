// Command gen writes the speed-table fixture sets (see package speedfixtures).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/AtomiCloud/obs/internal/alerts/speedfixtures"
)

func main() {
	out := flag.String("out", "tests/fixtures/alerts-speed", "output folder")
	flag.Parse()
	if err := speedfixtures.Write(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}
