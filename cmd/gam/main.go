package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/CafecitoGames/godot-addon-manager/internal/cli"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "gam:", err)
		code := output.CodeFor(err)
		var ue *cli.UsageError
		if errors.As(err, &ue) {
			code = output.ExitUsage
		}
		os.Exit(int(code))
	}
}
