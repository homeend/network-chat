package main

import (
	"os"

	"netchat/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:])) }
