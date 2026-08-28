package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danielriddell21/merkelbrot/web"
)

func exportCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "export",
		Short: "Write a self-contained HTML page to stdout",
		Long: `Write the viewer as a single HTML page.

Styles, script and data are all inlined, so the output opens with no server and
no network access.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := opts.build()
			if err != nil {
				return err
			}
			if err := web.Render(cmd.Context(), cmd.OutOrStdout(), s); err != nil {
				return fmt.Errorf("rendering page: %w", err)
			}
			return nil
		},
	}
}
