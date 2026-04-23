package cli

import (
	"fmt"

	"github.com/Khan/genqlient/graphql"
	pipe2 "github.com/pipe2-ai/sdk-go"
)

// MustClient builds a GraphQL client from the loaded config. Returns an
// ExitError with code ExitUnauthorized if no token is available — agents
// can then re-auth or surface the error cleanly.
func MustClient() (graphql.Client, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, &ExitError{Code: ExitGeneric, Err: err}
	}
	token := cfg.EffectiveToken()
	if token == "" {
		return nil, &ExitError{
			Code: ExitUnauthorized,
			Err: fmt.Errorf("no token configured. Run `pipe2 auth login` " +
				"or set $PIPE2_TOKEN"),
		}
	}
	return pipe2.NewClient(token, cfg.EffectiveAPIURL()), nil
}
