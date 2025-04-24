package main

import (
	"fmt"
	"os"

	"gitlab.melroy.org/melroy/fediresolve/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
