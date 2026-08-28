package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func sceneCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "scene",
		Short: "Write the scene as JSON to stdout",
		Long: `Write the laid-out scene as JSON.

A scene is plain data — positions, radii, kinds and payload fields — so any
front end can consume it in place of the bundled viewer.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := opts.build()
			if err != nil {
				return err
			}
			if err := s.WriteJSON(cmd.OutOrStdout()); err != nil {
				return fmt.Errorf("writing scene: %w", err)
			}
			return nil
		},
	}
}
