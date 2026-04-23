package cli

import (
	"fmt"
	"strings"
)

// classifyAPIError inspects a genqlient/GraphQL error string and maps it to
// the most specific ExitError. We keep this dumb (substring matching) on
// purpose — the SDK doesn't expose typed errors and rebuilding it just for
// classification would be overkill.
func classifyAPIError(err error) error {
	if err == nil {
		return nil
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "401"), strings.Contains(s, "unauthor"), strings.Contains(s, "jwt"):
		return &ExitError{Code: ExitUnauthorized, Err: err}
	case strings.Contains(s, "403"), strings.Contains(s, "forbidden"), strings.Contains(s, "permission"):
		return &ExitError{Code: ExitForbidden, Err: err}
	case strings.Contains(s, "404"), strings.Contains(s, "not found"):
		return &ExitError{Code: ExitNotFound, Err: err}
	default:
		return &ExitError{Code: ExitGeneric, Err: fmt.Errorf("api: %w", err)}
	}
}
