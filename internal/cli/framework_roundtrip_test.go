package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

const frameworkInfoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>SwiftGodot</string>
	<key>CFBundleIdentifier</key>
	<string>com.cafecito.SwiftGodot</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>SwiftGodot</string>
	<key>CFBundlePackageType</key>
	<string>FMWK</string>
	<key>CFBundleShortVersionString</key>
	<string>1.0</string>
	<key>CFBundleVersion</key>
	<string>1</string>
</dict>
</plist>
`

func TestMacOSFrameworkRoundTripsThroughPackageAndInstall(t *testing.T) {
	withHost(t, hostA)

	addonRepo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(addonRepo, "gpm-package.toml"), []byte(`
[package]
name       = "SwiftGodot"
addon_path = "addons/SwiftGodot"
version    = "1.0.0"
`), 0o644))

	addonRoot := filepath.Join(addonRepo, "addons", "SwiftGodot")
	frameworkDir := filepath.Join(addonRoot, "bin", "macos_arm64", "SwiftGodot.framework")
	versionADir := filepath.Join(frameworkDir, "Versions", "A")
	require.NoError(t, os.MkdirAll(filepath.Join(versionADir, "Resources"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(versionADir, "Headers"), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(addonRoot, "plugin.cfg"), []byte("[plugin]\nname=\"SwiftGodot\"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(addonRoot, "SwiftGodot.gdextension"), []byte(`[configuration]
entry_symbol = "swift_godot_init"
compatibility_minimum = "4.3"

[libraries]
macos.template_release.arm64 = "res://addons/SwiftGodot/bin/macos_arm64/SwiftGodot.framework"
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(versionADir, "Resources", "Info.plist"), []byte(frameworkInfoPlist), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(versionADir, "Headers", "SwiftGodot.h"), []byte("int swift_godot_init(void);\n"), 0o644))

	binaryPath := filepath.Join(versionADir, "SwiftGodot")
	signedFramework := buildAndSignTestFrameworkIfDarwin(t, frameworkDir, binaryPath)

	require.NoError(t, os.Symlink("A", filepath.Join(frameworkDir, "Versions", "Current")))
	require.NoError(t, os.Symlink("Versions/Current/SwiftGodot", filepath.Join(frameworkDir, "SwiftGodot")))
	require.NoError(t, os.Symlink("Versions/Current/Resources", filepath.Join(frameworkDir, "Resources")))
	require.NoError(t, os.Symlink("Versions/Current/Headers", filepath.Join(frameworkDir, "Headers")))

	if signedFramework {
		runCodesign(t, "--force", "-s", "-", frameworkDir)
		runCodesign(t, "--verify", "--strict", frameworkDir)
	}

	distDir := t.TempDir()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := Execute([]string{"package", "--dir", addonRepo, "--out", distDir}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		assetPath := filepath.Join(distDir, filepath.Base(request.URL.Path))
		data, err := os.ReadFile(assetPath)
		if err != nil {
			http.Error(writer, "missing asset", http.StatusNotFound)
			return
		}
		_, _ = writer.Write(data)
	}))
	defer server.Close()

	gameRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(gameRoot, "project.godot"), nil, 0o644))
	manifestBody := fmt.Sprintf(`[project]
platforms = ["macos.arm64"]

[addons.SwiftGodot]
source  = "archive"
url     = "%s/dist/SwiftGodot.zip"
index   = "%s/dist/%s"
version = "1.0.0"
`, server.URL, server.URL, slice.IndexFileName)
	require.NoError(t, os.WriteFile(filepath.Join(gameRoot, "addons.toml"), []byte(manifestBody), 0o644))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"install", "--dir", gameRoot}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())
	requestsAfterFirstInstall := requests.Load()
	require.Greater(t, requestsAfterFirstInstall, int32(0))

	installedFramework := filepath.Join(gameRoot, "addons", "SwiftGodot", "bin", "macos_arm64", "SwiftGodot.framework")
	expectedLinks := map[string]string{
		filepath.Join(installedFramework, "Versions", "Current"): "A",
		filepath.Join(installedFramework, "SwiftGodot"):          "Versions/Current/SwiftGodot",
		filepath.Join(installedFramework, "Resources"):           "Versions/Current/Resources",
		filepath.Join(installedFramework, "Headers"):             "Versions/Current/Headers",
	}
	for linkPath, wantTarget := range expectedLinks {
		info, err := os.Lstat(linkPath)
		require.NoError(t, err)
		require.NotZero(t, info.Mode()&os.ModeSymlink, "%s must remain a symlink", linkPath)
		gotTarget, err := os.Readlink(linkPath)
		require.NoError(t, err)
		require.Equal(t, filepath.FromSlash(wantTarget), gotTarget)
	}

	if signedFramework {
		runCodesign(t, "--verify", "--strict", installedFramework)
		runCodesign(t, "--force", "-s", "-", installedFramework)
		runCodesign(t, "--verify", "--strict", installedFramework)
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"list", "--dir", gameRoot}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())
	require.Contains(t, stdout.String(), "[x] SwiftGodot")

	// A repeat install with intact files and symlinks must hit the fast path without fetching.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"install", "--dir", gameRoot}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())
	require.Equal(t, requestsAfterFirstInstall, requests.Load())

	// Deleting one framework symlink marks the addon incomplete and triggers repair on install.
	topBinaryLink := filepath.Join(installedFramework, "SwiftGodot")
	require.NoError(t, os.Remove(topBinaryLink))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"list", "--dir", gameRoot}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())
	require.Contains(t, stdout.String(), "[ ] SwiftGodot")

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"install", "--dir", gameRoot}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())
	require.Greater(t, requests.Load(), requestsAfterFirstInstall)
	restoredInfo, err := os.Lstat(topBinaryLink)
	require.NoError(t, err)
	require.NotZero(t, restoredInfo.Mode()&os.ModeSymlink)

	// Replacing a recorded symlink with an escaping symlink also invalidates state and repairs.
	requestsBeforeEscapeRepair := requests.Load()
	currentVersionLink := filepath.Join(installedFramework, "Versions", "Current")
	require.NoError(t, os.Remove(currentVersionLink))
	require.NoError(t, os.Symlink("/etc", currentVersionLink))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"install", "--dir", gameRoot}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())
	require.Greater(t, requests.Load(), requestsBeforeEscapeRepair)
	repairedTarget, err := os.Readlink(currentVersionLink)
	require.NoError(t, err)
	require.Equal(t, "A", repairedTarget)
}

func buildAndSignTestFrameworkIfDarwin(t *testing.T, _ string, binaryPath string) bool {
	t.Helper()
	if runtime.GOOS == "darwin" {
		compilerPath, compilerErr := exec.LookPath("cc")
		_, codesignErr := exec.LookPath("codesign")
		if compilerErr == nil && codesignErr == nil {
			sourceFile := filepath.Join(t.TempDir(), "swiftgodot.c")
			require.NoError(t, os.WriteFile(sourceFile, []byte("int swift_godot_init(void) { return 0; }\n"), 0o644))
			command := exec.Command(
				compilerPath,
				"-dynamiclib",
				"-install_name", "@rpath/SwiftGodot.framework/Versions/A/SwiftGodot",
				sourceFile,
				"-o", binaryPath,
			)
			commandOutput, err := command.CombinedOutput()
			require.NoError(t, err, string(commandOutput))
			return true
		}
	}
	require.NoError(t, os.WriteFile(binaryPath, []byte("macho-stub"), 0o755))
	return false
}

func runCodesign(t *testing.T, arguments ...string) {
	t.Helper()
	command := exec.Command("codesign", arguments...)
	commandOutput, err := command.CombinedOutput()
	require.NoError(t, err, string(commandOutput))
}
