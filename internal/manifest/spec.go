package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// SourceType identifies how an addon is obtained.
type SourceType string

const (
	SourceGit           SourceType = "git"
	SourceGitHubRelease SourceType = "github-release"
	SourceArchive       SourceType = "archive"
)

// AddonSpec is one addon entry declared in addons.toml.
type AddonSpec struct {
	// Name is the TOML table key. It is set during Load and is not a TOML field.
	Name string `toml:"-"`

	Source     SourceType `toml:"source"`
	URL        string     `toml:"url,omitempty"`
	Repo       string     `toml:"repo,omitempty"`
	Version    string     `toml:"version,omitempty"`
	Asset      string     `toml:"asset,omitempty"`
	SourcePath string     `toml:"source_path,omitempty"`
	InstallAs  string     `toml:"install_as,omitempty"`
}

// Manifest is the parsed contents of addons.toml.
type Manifest struct {
	Addons map[string]AddonSpec `toml:"addons"`
}

// InstallName returns the directory name under addons/ for this addon.
func (s AddonSpec) InstallName() string {
	if s.InstallAs != "" {
		return s.InstallAs
	}
	return s.Name
}

// Hash returns a stable hash of the spec's resolvable fields, used to detect
// drift between addons.toml and addons.lock.
func (s AddonSpec) Hash() string {
	repr := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		s.Source, s.URL, s.Repo, s.Version, s.Asset, s.SourcePath, s.InstallAs)
	sum := sha256.Sum256([]byte(repr))
	return hex.EncodeToString(sum[:])
}
