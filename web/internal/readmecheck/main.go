// Command readmecheck compiles the usage snippet from the README so it cannot
// drift from the API.
package main

import (
	"context"
	"log"
	"os"

	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
	"github.com/danielriddell21/merkelbrot/scene"
	"github.com/danielriddell21/merkelbrot/web"
)

func main() {
	src := graph.NewMemorySource([]string{"commit"},
		graph.Node[string]{ID: "commit", Kind: "commit", Label: "initial", Children: []string{"tree"}},
		graph.Node[string]{ID: "tree", Kind: "tree", Label: "/", Children: []string{"readme"}},
		graph.Node[string]{
			ID: "readme", Kind: "blob", Label: "README.md",
			Payload: []graph.Field{{Key: "size", Value: "184 B"}},
		},
	)

	g, err := graph.New(src)
	if err != nil {
		log.Fatal(err)
	}

	s := scene.Builder[string]{Title: "my graph"}.Scene(layout.Pack(g, layout.Options{}))
	if err := web.Render(context.Background(), os.Stdout, s); err != nil {
		log.Fatal(err)
	}
}
