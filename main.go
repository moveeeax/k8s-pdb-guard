package main

import (
	"fmt"
	"os"

	"github.com/moveeeax/k8s-pdb-guard/cmd"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}
