// Package all is the registration manifest for every cookbook recipe
// shipped with the binary. Importing it for side effects pulls every
// recipe sub-package's init() into the program, populating the
// cookbook.Register'd registry.
//
// Adding a recipe means creating cookbook/<slug>/recipe.go and adding
// a side-effect import here. The binary can't drift between "what's
// compiled in" and "what's registered" — a package that isn't listed
// here is silently disabled.
//
// Imported by cmd/pipe2/main.go (production) and by integration tests
// that need the full registry.
package all

import (
	_ "github.com/pipe2-ai/pipe2-cli/cookbook/clip_factory"
	_ "github.com/pipe2-ai/pipe2-cli/cookbook/dance_reel"
)
