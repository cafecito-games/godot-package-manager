package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// GitFetcher clones a git ref using the system `git` binary, inheriting the
// user's existing SSH and credential-helper configuration.
type GitFetcher struct{}

// Fetch clones spec.URL at spec.Version into a temp directory and reports the
// resolved commit SHA. It first attempts a shallow fetch of the ref; if that
// fails (e.g. when Version is a bare commit SHA that the server cannot resolve
// via smart-fetch), it falls back to a full fetch and then checks out the
// exact SHA or ref.
func (f *GitFetcher) Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error) {
	// Defense in depth: the manifest validator already rejects these, but the
	// fetcher must not pass a value that git could parse as a command-line flag.
	if strings.HasPrefix(spec.URL, "-") {
		return FetchResult{}, &output.FetchError{Err: fmt.Errorf("git url %q must not begin with '-'", spec.URL)}
	}
	if strings.HasPrefix(spec.Version, "-") {
		return FetchResult{}, &output.FetchError{Err: fmt.Errorf("git version %q must not begin with '-'", spec.Version)}
	}
	if _, err := exec.LookPath("git"); err != nil {
		return FetchResult{}, &output.FetchError{Err: fmt.Errorf("git binary not found on PATH")}
	}
	dir, err := os.MkdirTemp("", "gpm-git-*")
	if err != nil {
		return FetchResult{}, &output.FetchError{Err: err}
	}

	if err := runGit(ctx, dir, "init", "-q"); err != nil {
		_ = os.RemoveAll(dir)
		return FetchResult{}, err
	}
	if err := runGit(ctx, dir, "remote", "add", "origin", spec.URL); err != nil {
		_ = os.RemoveAll(dir)
		return FetchResult{}, err
	}

	shallowErr := runGit(ctx, dir, "fetch", "-q", "--depth", "1", "origin", spec.Version)
	if shallowErr == nil {
		if err := runGit(ctx, dir, "-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD"); err != nil {
			_ = os.RemoveAll(dir)
			return FetchResult{}, err
		}
	} else {
		// Shallow fetch failed — fall back to a full fetch so that bare commit
		// SHAs and other refs not served by the shallow protocol still work.
		if err := runGit(ctx, dir, "fetch", "-q", "origin"); err != nil {
			_ = os.RemoveAll(dir)
			return FetchResult{}, err
		}
		if err := runGit(ctx, dir, "-c", "advice.detachedHead=false", "checkout", "-q", spec.Version); err != nil {
			_ = os.RemoveAll(dir)
			return FetchResult{}, err
		}
	}

	stdout, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		_ = os.RemoveAll(dir)
		return FetchResult{}, err
	}
	return FetchResult{Dir: dir, ResolvedVersion: strings.TrimSpace(stdout)}, nil
}

// gitCommand builds a git invocation. It disables the `ext::` remote helper
// (which would let a crafted URL execute arbitrary commands) and credential
// prompting so a fetch cannot block waiting on terminal input.
func gitCommand(ctx context.Context, directory string, args ...string) *exec.Cmd {
	full := append([]string{"-c", "protocol.ext.allow=never"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
}

func runGit(ctx context.Context, directory string, args ...string) error {
	cmd := gitCommand(ctx, directory, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &output.FetchError{Err: fmt.Errorf("git %s: %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))}
	}
	return nil
}

func gitOutput(ctx context.Context, directory string, args ...string) (string, error) {
	cmd := gitCommand(ctx, directory, args...)
	stdout, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", &output.FetchError{Err: fmt.Errorf("git %s: %w: %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(exitErr.Stderr)))}
		}
		return "", &output.FetchError{Err: fmt.Errorf("git %s: %w", strings.Join(args, " "), err)}
	}
	return string(stdout), nil
}
