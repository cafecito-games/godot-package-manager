package manifest

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// Validate checks every addon entry for required and consistent fields.
// It returns an *output.ManifestError describing the first problem found.
func (m *Manifest) Validate() error {
	for name, addon := range m.Addons {
		if err := validateSpec(name, addon); err != nil {
			return &output.ManifestError{Err: err}
		}
	}
	return nil
}

// validateAddonName rejects names that could escape the addons directory when
// used as a single path segment. It checks for empty strings, path separators,
// the relative-traversal components "." and "..", and absolute paths.
func validateAddonName(name string) error {
	if name == "" {
		return fmt.Errorf("addon name must not be empty")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("addon name %q must not be an absolute path", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("addon name %q must not contain path separators", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("addon name %q is not a valid directory name", name)
	}
	return nil
}

// validateSourcePath rejects source_path values that could escape the fetched
// source root. Absolute paths and any component equal to ".." are rejected.
func validateSourcePath(sourcePath string) error {
	if filepath.IsAbs(sourcePath) {
		return fmt.Errorf("source_path %q must not be an absolute path", sourcePath)
	}
	cleaned := filepath.Clean(sourcePath)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("source_path %q must not escape the source root", sourcePath)
	}
	return nil
}

// sha256Pattern matches a bare SHA-256 digest: exactly 64 lowercase hex digits.
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validateChecksum rejects checksum values that are not a bare SHA-256 digest.
func validateChecksum(checksum string) error {
	if !sha256Pattern.MatchString(checksum) {
		return fmt.Errorf("checksum %q must be 64 lowercase hex digits (SHA-256)", checksum)
	}
	return nil
}

// scpLikeGitURL reports whether s is a git SCP-style location ([user@]host:path)
// rather than a scheme URL. It is identified by a colon that precedes the first
// slash, with no "://" present.
func scpLikeGitURL(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	colon := strings.IndexByte(s, ':')
	slash := strings.IndexByte(s, '/')
	return colon > 0 && (slash == -1 || colon < slash)
}

// validateGitURL allows only URL forms that are safe to hand to the system git
// binary. It rejects the "transport::address" remote-helper syntax (e.g.
// "ext::sh -c ...", which would execute arbitrary commands) and values that
// could be misparsed as command-line flags. A scheme-less value is treated as
// a local filesystem path, which git can clone and which grants no access the
// user does not already have.
func validateGitURL(rawURL string) error {
	if strings.HasPrefix(rawURL, "-") {
		return fmt.Errorf("url %q must not begin with '-'", rawURL)
	}
	if strings.Contains(rawURL, "::") {
		return fmt.Errorf("url %q uses a disallowed git remote-helper syntax", rawURL)
	}
	if scpLikeGitURL(rawURL) {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("url %q is not a valid URL: %w", rawURL, err)
	}
	switch parsed.Scheme {
	case "https", "http", "ssh", "git", "file", "":
		return nil
	default:
		return fmt.Errorf("url %q uses unsupported scheme %q (allowed: https, http, ssh, git, file)", rawURL, parsed.Scheme)
	}
}

// validateArchiveURL allows only plain HTTP(S) archive URLs.
func validateArchiveURL(rawURL string) error {
	if strings.HasPrefix(rawURL, "-") {
		return fmt.Errorf("url %q must not begin with '-'", rawURL)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("url %q is not a valid URL: %w", rawURL, err)
	}
	switch parsed.Scheme {
	case "http", "https":
		return nil
	case "":
		return fmt.Errorf("url %q must include an http:// or https:// scheme", rawURL)
	default:
		return fmt.Errorf("url %q uses unsupported scheme %q (allowed: http, https)", rawURL, parsed.Scheme)
	}
}

// validateGitVersion rejects refs that could be misparsed as git command-line
// flags when passed positionally to git fetch/checkout.
func validateGitVersion(version string) error {
	if strings.HasPrefix(version, "-") {
		return fmt.Errorf("version %q must not begin with '-'", version)
	}
	return nil
}

// validateSpec checks one addon entry's required fields for its source type.
func validateSpec(name string, addon AddonSpec) error {
	if err := validateAddonName(name); err != nil {
		return err
	}
	if addon.InstallAs != "" {
		if err := validateAddonName(addon.InstallAs); err != nil {
			return fmt.Errorf("addon %q: invalid install_as: %w", name, err)
		}
	}
	if addon.SourcePath != "" {
		if err := validateSourcePath(addon.SourcePath); err != nil {
			return fmt.Errorf("addon %q: invalid source_path: %w", name, err)
		}
	}
	if addon.Checksum != "" {
		if addon.Source == SourceGit {
			return fmt.Errorf("addon %q: checksum is not supported for git sources", name)
		}
		if err := validateChecksum(addon.Checksum); err != nil {
			return fmt.Errorf("addon %q: invalid checksum: %w", name, err)
		}
	}

	switch addon.Source {
	case SourceGit:
		if addon.URL == "" || addon.Version == "" {
			return fmt.Errorf("addon %q: git source requires url and version", name)
		}
		if err := validateGitURL(addon.URL); err != nil {
			return fmt.Errorf("addon %q: %w", name, err)
		}
		if err := validateGitVersion(addon.Version); err != nil {
			return fmt.Errorf("addon %q: %w", name, err)
		}
	case SourceGitHubRelease:
		if addon.Repo == "" || addon.Version == "" {
			return fmt.Errorf("addon %q: github-release source requires repo and version", name)
		}
	case SourceArchive:
		if addon.URL == "" {
			return fmt.Errorf("addon %q: archive source requires url", name)
		}
		if err := validateArchiveURL(addon.URL); err != nil {
			return fmt.Errorf("addon %q: %w", name, err)
		}
	default:
		return fmt.Errorf("addon %q: unknown source %q (want git, github-release, or archive)", name, addon.Source)
	}
	return nil
}
