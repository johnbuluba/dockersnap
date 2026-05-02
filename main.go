package main

import (
	"os"

	"github.com/johnbuluba/dockersnap/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

