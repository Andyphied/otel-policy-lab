package main

import (
	"os"

	"github.com/andyphied/otel-policy-lab/internal/cli"
)

var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Run(os.Args))
}
