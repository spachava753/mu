package piacp

import (
	"context"
	"os/exec"
)

// Detection is what's known about a Pi executable.
type Detection struct {
	Found    bool
	Path     string
	Version  string
	Outdated bool
	// SignedIn is whether Pi has any model it can use. Credentials are only checked
	// for being there, so a bad key still counts, and shows up as a failed turn.
	SignedIn bool
	// Err is why Pi was found but couldn't be asked.
	Err error
}

// Detect asks a Pi executable about itself, which starts it, so it takes a moment
// and belongs off any thread that shouldn't wait. An empty executable means "pi"
// from the PATH. A process with no session writes nothing.
func Detect(ctx context.Context, executable, cwd string) Detection {
	if executable == "" {
		executable = defaultExecutable
	}
	path, err := exec.LookPath(executable)
	if err != nil {
		return Detection{}
	}
	d := Detection{Found: true, Path: path}
	if d.Version, d.Err = piVersion(ctx, path); d.Err != nil {
		return d
	}
	if !atLeast(d.Version, MinimumVersion) {
		d.Outdated = true
		return d
	}
	p, err := startPi(path, cwd, []string{"--no-session"}, func(string, []byte) {})
	if err != nil {
		d.Err = err
		return d
	}
	defer p.kill()
	var models struct {
		Models []piModel `json:"models"`
	}
	if d.Err = p.call(ctx, "get_available_models", nil, &models); d.Err == nil {
		d.SignedIn = len(models.Models) > 0
	}
	return d
}
