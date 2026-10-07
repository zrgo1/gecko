package main

import (
	"os"

	"github.com/zrgo/gecko/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
