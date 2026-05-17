package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/cafecito-games/godot-package-manager/internal/cli"
	"github.com/cafecito-games/godot-package-manager/internal/output"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "gpm:", err)
		code := output.CodeFor(err)
		var ue *cli.UsageError
		if errors.As(err, &ue) {
			code = output.ExitUsage
		}
		os.Exit(int(code))
	}
}
