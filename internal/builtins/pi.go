// Package builtins describes the agents Mu has adapters for. It's where each one's
// particulars live, so that the app and the view can treat them all alike.
package builtins

import (
	"context"

	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/piacp"
)

// Pi is the built-in agent for Pi. stateDir is where its adapter may keep markers
// for sessions that have no file yet.
func Pi(stateDir string) app.Builtin {
	return app.Builtin{
		Name:           "pi",
		Title:          "Pi",
		Mark:           "π",
		Site:           "https://pi.dev",
		SiteLabel:      "pi.dev",
		MinimumVersion: piacp.MinimumVersion,
		SignInHelp:     "Pi needs a model provider. Run pi in a terminal, then enter /login.",
		SignInCommand:  "pi",
		Facts: []app.BuiltinFact{
			{Label: "Sign-in", Text: "Handled by Pi, with /login. Mu never sees your credentials."},
			{Label: "Tool use", Text: "Pi doesn't ask before every tool call, so expect few permission requests. It runs with your user's access."},
			{Label: "Projects", Text: "A folder's .pi settings, prompts, skills and extensions load only once you've trusted it with /trust in Pi."},
			{Label: "Sessions", Text: "Kept by Pi, so you can open them in either place."},
		},
		Detect: func(ctx context.Context, executable, cwd string) app.BuiltinStatus {
			return piStatus(piacp.Detect(ctx, executable, cwd))
		},
		Serve: func(ctx context.Context, link agent.Link, executable func() string) error {
			return piacp.Serve(ctx, link.Transport, piacp.Config{Executable: executable, WorkDir: link.Cwd, StateDir: stateDir, Diag: link.Diag})
		},
	}
}

func piStatus(d piacp.Detection) app.BuiltinStatus {
	status := app.BuiltinStatus{Version: d.Version, Path: d.Path}
	switch {
	case !d.Found:
		status.State = app.BuiltinNotFound
	case d.Err != nil:
		status.State, status.Detail = app.BuiltinBroken, d.Err.Error()
	case d.Outdated:
		status.State = app.BuiltinOutdated
	case !d.SignedIn:
		status.State = app.BuiltinSignedOut
	default:
		status.State = app.BuiltinReady
	}
	return status
}
