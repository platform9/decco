package main

import (
	"github.com/erwinvaneyk/cobras"

	"github.com/platform9/decco/cmd/decco/cmd"
)

// TODO add decco serve
func main() {
	cobras.Execute(cmd.NewRootCmd())
}
