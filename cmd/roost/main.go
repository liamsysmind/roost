package main

import (
	"fmt"
	"os"
)

const version = "0.0.0-dev"

func main() {
	fmt.Fprintf(os.Stderr, "roost %s — not yet wired up. See W1.\n", version)
}
