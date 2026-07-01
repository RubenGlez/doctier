// Command doctier classifies generated documents into visibility (public/
// private) and lifetime (durable/ephemeral) tiers and enforces that policy over
// git. It is harness-agnostic: the .doctier.yml manifest is the only input.
package main

import (
	"os"

	"github.com/rubenglez/doctier/cmd"
)

func main() {
	os.Exit(cmd.Execute(os.Args[1:]))
}
