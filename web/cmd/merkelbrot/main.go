// Command merkelbrot serves or exports a zoomable view of a Merkle DAG.
//
// Run "merkelbrot --help" for the command tree, or see
// [github.com/danielriddell21/merkelbrot/web] for the renderer it drives.
package main

import (
	"os"

	"github.com/danielriddell21/merkelbrot/web/internal/cli"
)

// version is set by the release build.
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		os.Exit(1)
	}
}
