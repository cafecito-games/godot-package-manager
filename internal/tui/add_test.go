package tui

import (
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/stretchr/testify/require"
)

func TestWizardBuildsGitSpec(t *testing.T) {
	wizard := newWizardState()
	wizard.setSource(manifest.SourceGit)
	require.Equal(t, []string{"name", "url", "version", "source_path", "install_as"}, wizard.fieldOrder())

	wizard.set("name", "dialogue")
	wizard.set("url", "https://example.com/d.git")
	wizard.set("version", "v1.0")

	spec := wizard.spec()
	require.Equal(t, manifest.SourceGit, spec.Source)
	require.Equal(t, "dialogue", spec.Name)

	m := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{spec.Name: spec}}
	require.NoError(t, m.Validate())
}

func TestWizardFieldsForArchive(t *testing.T) {
	wizard := newWizardState()
	wizard.setSource(manifest.SourceArchive)
	require.Equal(t, []string{"name", "url", "source_path", "install_as"}, wizard.fieldOrder())
}

func TestWizardFieldsForRelease(t *testing.T) {
	wizard := newWizardState()
	wizard.setSource(manifest.SourceGitHubRelease)
	require.Equal(t, []string{"name", "repo", "version", "asset", "source_path", "install_as"}, wizard.fieldOrder())
}

func TestWizardRejectsEmptyName(t *testing.T) {
	wizard := newWizardState()
	wizard.setSource(manifest.SourceGit)
	wizard.set("url", "https://example.com/repo.git")
	wizard.set("version", "v1.0")
	// name is intentionally not set

	spec := wizard.spec()
	require.Equal(t, "", spec.Name, "spec should have empty name when name field was not set")
}
