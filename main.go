package main

import (
	"fmt"
	"os"

	"github.com/dennis/fediresolve/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
