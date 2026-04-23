package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	pipe2 "github.com/pipe2-ai/sdk-go"
)

func newAuthCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "auth",
		Short: "Manage Pipe2.ai authentication",
	}
	c.AddCommand(newAuthLoginCmd(), newAuthLogoutCmd(), newAuthWhoamiCmd())
	return c
}

func newAuthLoginCmd() *cobra.Command {
	var tokenFlag, apiURLFlag string
	c := &cobra.Command{
		Use:   "login",
		Short: "Save a personal access token to the config file",
		Long: `Save a Pipe2.ai personal access token (PAT) to the config file.

You can mint a token at https://pipe2.ai/api-keys and pass it via --token,
or pipe it on stdin:

  echo $PAT | pipe2 auth login --token -

The token is stored at $XDG_CONFIG_HOME/pipe2/config.json with mode 0600.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token := tokenFlag
			if token == "-" {
				b, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				token = strings.TrimSpace(b)
			}
			if token == "" {
				return &ExitError{
					Code: ExitUsage,
					Err:  fmt.Errorf("--token is required (use `--token -` to read from stdin)"),
				}
			}

			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			cfg.Token = token
			if apiURLFlag != "" {
				cfg.APIURL = apiURLFlag
			}

			// Verify the token works before persisting.
			client := pipe2.NewClient(token, cfg.EffectiveAPIURL())
			user, err := pipe2.GetCurrentUser(cmd.Context(), client)
			if err != nil {
				return &ExitError{Code: ExitUnauthorized, Err: fmt.Errorf("token rejected: %w", err)}
			}

			if err := SaveConfig(cfg); err != nil {
				return err
			}
			Status("logged in (token saved to %s)", mustConfigPath())
			return Out(map[string]any{
				"ok":   true,
				"user": user.Current_user,
			})
		},
	}
	c.Flags().StringVar(&tokenFlag, "token", "", `personal access token, or "-" to read from stdin`)
	c.Flags().StringVar(&apiURLFlag, "api-url", "", "override saved API URL")
	return c
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear the saved token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			cfg.Token = ""
			if err := SaveConfig(cfg); err != nil {
				return err
			}
			Status("logged out")
			return Out(map[string]any{"ok": true})
		},
	}
}

func newAuthWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the user identified by the current token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := MustClient()
			if err != nil {
				return err
			}
			user, err := pipe2.GetCurrentUser(cmd.Context(), client)
			if err != nil {
				return classifyAPIError(err)
			}
			return Out(user.Current_user)
		},
	}
}

func mustConfigPath() string {
	p, _ := configPath()
	return p
}
