package cli

import (
	"github.com/spf13/cobra"
)

func completionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for merkelbrot.

Bash:
  merkelbrot completion bash > /etc/bash_completion.d/merkelbrot
  # or for the current user:
  merkelbrot completion bash > ~/.local/share/bash-completion/completions/merkelbrot

Zsh:
  merkelbrot completion zsh > "${fpath[1]}/_merkelbrot"
  # then restart your shell or run: autoload -U compinit && compinit

Fish:
  merkelbrot completion fish > ~/.config/fish/completions/merkelbrot.fish

PowerShell:
  merkelbrot completion powershell | Out-String | Invoke-Expression
  # to persist, add that line to your $PROFILE`,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(out, true) //nolint:wrapcheck // cobra's own error is already clear
			case "zsh":
				return cmd.Root().GenZshCompletion(out) //nolint:wrapcheck // cobra's own error is already clear
			case "fish":
				return cmd.Root().GenFishCompletion(out, true) //nolint:wrapcheck // cobra's own error is already clear
			default:
				return cmd.Root().GenPowerShellCompletionWithDesc(out) //nolint:wrapcheck // cobra's own error is already clear
			}
		},
	}
	return cmd
}
