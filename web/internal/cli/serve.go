package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/danielriddell21/merkelbrot/web"
)

func serveCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the viewer on a local address",
		Long: `Serve the viewer over HTTP.

GET / returns the page and GET /scene.json returns the scene as JSON, for
anything that would rather read the data than the picture.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The graph is kept for the life of the server, so a page asking to see
			// more of it never makes the source be read from the beginning again.
			gr, err := newGrower(opts)
			if err != nil {
				return err
			}
			s, err := gr.scene(web.Ask{})
			if err != nil {
				return err
			}

			var lc net.ListenConfig
			listener, err := lc.Listen(cmd.Context(), "tcp", opts.addr)
			if err != nil {
				return fmt.Errorf("listening on %s: %w", opts.addr, err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "merkelbrot: %d nodes on http://%s\n", s.Stats.Nodes, listener.Addr())

			// A served page can ask for the parts of the graph its limits left out,
			// which an exported one has nobody to ask for.
			handler := (&web.Server{Scene: s, Expand: gr.scene}).Handler()
			srv := &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 5 * time.Second,
			}

			// Serve does not watch the context, so an interrupt is turned into a
			// shutdown here: requests in flight get a moment to finish instead of
			// being cut off.
			ctx := cmd.Context()
			go func() {
				<-ctx.Done()
				grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(grace)
			}()

			if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("serving: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.addr, "addr", "127.0.0.1:8080", "address to listen on")
	return cmd
}
