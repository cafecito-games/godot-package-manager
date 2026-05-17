package main

import (
	"os"

	"github.com/cafecito-games/godot-package-manager/internal/cli"
)

func main() {
	os.Exit(int(cli.Execute(os.Args[1:], os.Stdout, os.Stderr)))
}
