package output

import "errors"

// ExitCode is a process exit status with a defined meaning.
type ExitCode int

const (
	ExitOK       ExitCode = 0
	ExitGeneric  ExitCode = 1
	ExitUsage    ExitCode = 2
	ExitManifest ExitCode = 3
	ExitFetch    ExitCode = 4
	ExitInstall  ExitCode = 5
)

// ManifestError wraps a manifest or lockfile failure (exit code 3).
type ManifestError struct{ Err error }

func (e *ManifestError) Error() string { return e.Err.Error() }
func (e *ManifestError) Unwrap() error { return e.Err }

// FetchError wraps a network/auth/source-resolution failure (exit code 4).
type FetchError struct{ Err error }

func (e *FetchError) Error() string { return e.Err.Error() }
func (e *FetchError) Unwrap() error { return e.Err }

// InstallError wraps a filesystem/extraction failure (exit code 5).
type InstallError struct{ Err error }

func (e *InstallError) Error() string { return e.Err.Error() }
func (e *InstallError) Unwrap() error { return e.Err }

// CodeFor maps an error to the exit code the process should return.
func CodeFor(err error) ExitCode {
	if err == nil {
		return ExitOK
	}
	var me *ManifestError
	var fe *FetchError
	var ie *InstallError
	switch {
	case errors.As(err, &me):
		return ExitManifest
	case errors.As(err, &fe):
		return ExitFetch
	case errors.As(err, &ie):
		return ExitInstall
	default:
		return ExitGeneric
	}
}
