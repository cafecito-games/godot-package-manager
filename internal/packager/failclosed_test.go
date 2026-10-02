package packager_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/packager"
)

// validConfig is the config every fail-closed case starts from, so each case
// differs from a working package in exactly the one way it is about.
const validConfig = `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`

// minimalFiles is an addon subtree that packages successfully.
func minimalFiles() map[string]string {
	return map[string]string{"addons/addon/plugin.gd": "extends Node\n"}
}

// nativeFiles is an addon subtree with a .gdextension and its binaries.
func nativeFiles(extension string) map[string]string {
	return map[string]string{
		"addons/addon/addon.gdextension":   extension,
		"addons/addon/plugin.gd":           "extends Node\n",
		"addons/addon/bin/addon_linux.so":  "linux",
		"addons/addon/bin/addon_ios.dylib": "ios",
		"addons/addon/bin/libsupport.a":    "support",
	}
}

const linuxExtension = `[configuration]

entry_symbol = "addon_main"

[libraries]

linux.template_release.x86_64 = "bin/addon_linux.so"
`

// TestPackageFailClosedContract walks every row of the issue's fail-closed
// contract: the input condition, the error type it must carry, and the exit code
// output.CodeFor must resolve it to.
func TestPackageFailClosedContract(t *testing.T) {
	cases := []struct {
		name     string
		config   string
		files    map[string]string
		setup    func(t *testing.T, root string)
		options  func(root string) packager.Options
		wantCode output.ExitCode
		wantText string
	}{
		{
			name:   "config missing",
			config: "",
			files:  minimalFiles(),
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.Remove(filepath.Join(root, packager.ConfigFileName)))
			},
			wantCode: output.ExitManifest,
			wantText: packager.ConfigFileName,
		},
		{
			name:     "config unparseable",
			config:   "[package\nname = \"addon\"\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "parsing",
		},
		{
			name:     "config declares an unknown key",
			config:   validConfig + "addon_paths = \"addons/addon\"\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "unknown key",
		},
		{
			name:     "name empty",
			config:   "[package]\naddon_path = \"addons/addon\"\nversion = \"1.2.3\"\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "addon name must not be empty",
		},
		{
			name:     "name carries a path separator",
			config:   "[package]\nname = \"a/b\"\naddon_path = \"addons/addon\"\nversion = \"1.2.3\"\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "must not contain path separators",
		},
		{
			name:     "addon_path absent",
			config:   "[package]\nname = \"addon\"\nversion = \"1.2.3\"\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "addon_path is required",
		},
		{
			name:     "addon_path absolute",
			config:   "[package]\nname = \"addon\"\naddon_path = \"/addons/addon\"\nversion = \"1.2.3\"\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "must be relative",
		},
		{
			name:     "addon_path traverses",
			config:   "[package]\nname = \"addon\"\naddon_path = \"../addon\"\nversion = \"1.2.3\"\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "must not escape the repository root",
		},
		{
			name:     "addon_path is a file",
			config:   "[package]\nname = \"addon\"\naddon_path = \"addons/addon/plugin.gd\"\nversion = \"1.2.3\"\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "not a directory",
		},
		{
			name:   "addon_path passes through a symlink",
			config: validConfig,
			files:  minimalFiles(),
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.Rename(filepath.Join(root, "addons"), filepath.Join(root, "real")))
				require.NoError(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "addons")))
			},
			wantCode: output.ExitManifest,
			wantText: "symlink",
		},
		{
			name:   "addon_path is empty",
			config: validConfig,
			files:  minimalFiles(),
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.Remove(filepath.Join(root, "addons", "addon", "plugin.gd")))
			},
			wantCode: output.ExitManifest,
			wantText: "holds no files",
		},
		{
			name:     "version empty and not overridden",
			config:   "[package]\nname = \"addon\"\naddon_path = \"addons/addon\"\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "version is required",
		},
		{
			name:     "slices key is not a slice ID",
			config:   validConfig + "\n[package.slices]\n\"linux.x86_65\" = [\"plugin.gd\"]\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "unknown architecture",
		},
		{
			name:     "slices key is core",
			config:   validConfig + "\n[package.slices]\n\"core\" = [\"plugin.gd\"]\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "is implicit and may not be declared",
		},
		{
			name:     "slices glob matches nothing",
			config:   validConfig + "\n[package.slices]\n\"linux.x86_64\" = [\"bin/*.so\"]\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "matches no file",
		},
		{
			name:     "slices glob escapes the addon path",
			config:   validConfig + "\n[package.slices]\n\"linux.x86_64\" = [\"../other/x.so\"]\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "must not escape the repository root",
		},
		{
			name:     "slices glob is empty",
			config:   validConfig + "\n[package.slices]\n\"linux.x86_64\" = []\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "names no path",
		},
		{
			name: "two globs claim one file",
			config: validConfig + "\n[package.slices]\n" +
				"\"linux.x86_64\" = [\"bin/addon_linux.so\"]\n\"ios.arm64\" = [\"bin/*.so\"]\n",
			files: map[string]string{
				"addons/addon/plugin.gd":          "extends Node\n",
				"addons/addon/bin/addon_linux.so": "linux",
			},
			wantCode: output.ExitManifest,
			wantText: "is claimed by two slices",
		},
		{
			name:     "a glob claims a file a gdextension entry already claims for another platform",
			config:   validConfig + "\n[package.slices]\n\"android.arm64\" = [\"bin/addon_linux.so\"]\n",
			files:    nativeFiles(linuxExtension),
			wantCode: output.ExitManifest,
			wantText: "is claimed by two slices",
		},
		{
			name:     "a glob claims a gdextension",
			config:   validConfig + "\n[package.slices]\n\"android.arm64\" = [\"addon.gdextension\"]\n",
			files:    nativeFiles(linuxExtension),
			wantCode: output.ExitManifest,
			wantText: "always ships in the \"core\" slice",
		},
		{
			name:   "a libraries value names a missing file",
			config: validConfig,
			files: map[string]string{
				"addons/addon/addon.gdextension": linuxExtension,
				"addons/addon/plugin.gd":         "extends Node\n",
			},
			wantCode: output.ExitManifest,
			wantText: "is not a file in the addon subtree",
		},
		{
			name:   "a libraries value resolves outside the addon path",
			config: validConfig,
			files: nativeFiles(`[configuration]

entry_symbol = "addon_main"

[libraries]

linux.template_release.x86_64 = "res://addons/other/addon_linux.so"
`),
			wantCode: output.ExitManifest,
			wantText: "outside the addon root",
		},
		{
			name:   "the second dependency dictionary key names a missing file",
			config: validConfig,
			files: nativeFiles(`[configuration]

entry_symbol = "addon_main"

[libraries]

ios.template_release.arm64 = "bin/addon_ios.dylib"

[dependencies]

ios.template_release.arm64 = { "bin/libsupport.a" : "", "bin/libabsent.a" : "" }
`),
			wantCode: output.ExitManifest,
			wantText: "libabsent.a",
		},
		{
			name:   "a dependency dictionary key resolves outside the addon path",
			config: validConfig,
			files: nativeFiles(`[configuration]

entry_symbol = "addon_main"

[libraries]

ios.template_release.arm64 = "bin/addon_ios.dylib"

[dependencies]

ios.template_release.arm64 = { "bin/libsupport.a" : "", "res://addons/other/libsupport.a" : "" }
`),
			wantCode: output.ExitManifest,
			wantText: "outside the addon root",
		},
		{
			name:   "a dependency destination is absolute",
			config: validConfig,
			files: nativeFiles(`[configuration]

entry_symbol = "addon_main"

[libraries]

ios.template_release.arm64 = "bin/addon_ios.dylib"

[dependencies]

ios.template_release.arm64 = { "bin/libsupport.a" : "/Frameworks" }
`),
			wantCode: output.ExitManifest,
			wantText: "must be relative",
		},
		{
			name:   "a dependency destination traverses",
			config: validConfig,
			files: nativeFiles(`[configuration]

entry_symbol = "addon_main"

[libraries]

ios.template_release.arm64 = "bin/addon_ios.dylib"

[dependencies]

ios.template_release.arm64 = { "bin/libsupport.a" : "../Frameworks" }
`),
			wantCode: output.ExitManifest,
			wantText: "must not escape",
		},
		{
			name:   "a dependency destination is a resource path",
			config: validConfig,
			files: nativeFiles(`[configuration]

entry_symbol = "addon_main"

[libraries]

ios.template_release.arm64 = "bin/addon_ios.dylib"

[dependencies]

ios.template_release.arm64 = { "bin/libsupport.a" : "res://Frameworks" }
`),
			wantCode: output.ExitManifest,
			wantText: "not a res:// path",
		},
		{
			name:   "a symlink in the addon subtree",
			config: validConfig,
			files:  minimalFiles(),
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.Symlink(
					filepath.Join(root, "addons", "addon", "plugin.gd"),
					filepath.Join(root, "addons", "addon", "alias.gd"),
				))
			},
			wantCode: output.ExitManifest,
			wantText: "alias.gd is a symlink",
		},
		{
			name:     "the version cannot be a file name",
			config:   "[package]\nname = \"addon\"\naddon_path = \"addons/addon\"\nversion = \"1.0/0\"\n",
			files:    minimalFiles(),
			wantCode: output.ExitManifest,
			wantText: "must not contain a path separator",
		},
		{
			name:   "the output directory is not writable",
			config: validConfig,
			files:  minimalFiles(),
			options: func(root string) packager.Options {
				return packager.Options{Directory: root, OutputDirectory: filepath.Join(root, "locked", "dist")}
			},
			setup: func(t *testing.T, root string) {
				locked := filepath.Join(root, "locked")
				require.NoError(t, os.Mkdir(locked, 0o555))
				t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
			},
			wantCode: output.ExitInstall,
			wantText: "creating the output directory",
		},
		{
			name:   "an empty destination is accepted",
			config: validConfig,
			files: nativeFiles(`[configuration]

entry_symbol = "addon_main"

[libraries]

ios.template_release.arm64 = "bin/addon_ios.dylib"

[dependencies]

ios.template_release.arm64 = { "bin/libsupport.a" : "" }
`),
			wantCode: output.ExitOK,
		},
		{
			name:   "a non-empty relative destination is accepted",
			config: validConfig,
			files: nativeFiles(`[configuration]

entry_symbol = "addon_main"

[libraries]

ios.template_release.arm64 = "bin/addon_ios.dylib"

[dependencies]

ios.template_release.arm64 = { "bin/libsupport.a" : "Frameworks" }
`),
			wantCode: output.ExitOK,
		},
		{
			name:   "one platform key in both sections is accepted",
			config: validConfig,
			files: nativeFiles(`[configuration]

entry_symbol = "addon_main"

[libraries]

ios.template_release.arm64 = "bin/addon_ios.dylib"

[dependencies]

ios.template_release.arm64 = { "bin/libsupport.a" : "" }
`),
			wantCode: output.ExitOK,
		},
		{
			name:     "an addon with no gdextension and no extras is accepted",
			config:   validConfig,
			files:    minimalFiles(),
			wantCode: output.ExitOK,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := writeAddon(t, testCase.config, testCase.files)
			if testCase.setup != nil {
				testCase.setup(t, root)
			}
			options := packager.Options{Directory: root, OutputDirectory: t.TempDir()}
			if testCase.options != nil {
				options = testCase.options(root)
			}

			result, err := packager.Package(options)
			if testCase.wantCode == output.ExitOK {
				require.NoError(t, err)
				require.NotNil(t, result)
				return
			}
			require.Error(t, err)
			require.Equal(t, testCase.wantCode, output.CodeFor(err))
			require.Contains(t, err.Error(), testCase.wantText)
			switch testCase.wantCode {
			case output.ExitManifest:
				var manifestError *output.ManifestError
				require.ErrorAs(t, err, &manifestError)
			case output.ExitInstall:
				var installError *output.InstallError
				require.ErrorAs(t, err, &installError)
			}
		})
	}
}

func TestPackageRefusesAnOutputDirectoryInsideTheAddonSubtree(t *testing.T) {
	root := writeAddon(t, validConfig, minimalFiles())

	_, err := packager.Package(packager.Options{
		Directory:       root,
		OutputDirectory: filepath.Join(root, "addons", "addon", "dist"),
	})
	require.Error(t, err)
	require.Equal(t, output.ExitManifest, output.CodeFor(err))
	require.Contains(t, err.Error(), "inside the addon subtree")
}
