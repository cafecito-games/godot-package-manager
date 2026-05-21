package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/assetlib"
	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/project"
	"github.com/cafecito-games/godot-package-manager/internal/tui"
	"github.com/spf13/cobra"
)

type assetLibClient interface {
	Configure(context.Context, string) (assetlib.Configuration, error)
	Search(context.Context, assetlib.SearchOptions) (assetlib.SearchResponse, error)
	GetAsset(context.Context, string) (assetlib.AssetDetail, error)
}

// testAssetLibClient, when non-nil, overrides the public AssetLib API in tests.
var testAssetLibClient assetLibClient

// runAssetLibTUI launches the interactive AssetLib browser.
var runAssetLibTUI = tui.RunAssetLibBrowser

// latestStableGodotVersion is replaceable in tests to avoid live GitHub release lookups.
var latestStableGodotVersion = func(ctx context.Context) (string, error) {
	return assetlib.LatestStableGodotVersion(ctx, "")
}

type assetLibFlags struct {
	dir, godotVersion           string
	category, support, sort     string
	name, sourcePath, installAs string
	maxResults                  int
}

var assetLibIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
var assetLibDownloadHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var assetLibSupportValues = map[string]struct{}{
	"official":  {},
	"featured":  {},
	"community": {},
	"testing":   {},
}

var assetLibSortValues = map[string]struct{}{
	"rating":  {},
	"cost":    {},
	"name":    {},
	"updated": {},
}

func newAssetLibCommand(opts *Options) *cobra.Command {
	flagValues := &assetLibFlags{}
	cmd := &cobra.Command{
		Use:   "assetlib",
		Short: "Search and add addons from Godot AssetLib",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAssetLibInteractive(cmd, opts, flagValues)
		},
	}
	cmd.Flags().StringVar(&flagValues.godotVersion, "godot-version", "", "Godot version filter (for example 4.2)")
	cmd.Flags().StringVar(&flagValues.dir, "dir", "", "start directory for project discovery")
	cmd.AddCommand(newAssetLibSearchCommand(opts))
	cmd.AddCommand(newAssetLibAddCommand(opts))
	return cmd
}

func runAssetLibInteractive(cmd *cobra.Command, opts *Options, flagValues *assetLibFlags) error {
	projectResult, err := loadOptionalProject(flagValues.dir)
	if err != nil {
		return err
	}
	godotVersion := flagValues.godotVersion
	if godotVersion == "" && projectResult.Project != nil {
		godotVersion = project.DetectGodotVersion(projectResult.Project.Root)
	}
	if godotVersion == "" && projectResult.Project == nil {
		godotVersion, err = latestStableGodotVersion(cmd.Context())
		if err != nil {
			return err
		}
	}
	client := assetLibClientFor()
	installDisabledReason := ""
	if projectResult.NotFound != nil {
		installDisabledReason = projectResult.NotFound.Error()
	}
	selection, err := runAssetLibTUI(tui.AssetLibConfig{
		Context:               cmd.Context(),
		InitialGodotVersion:   godotVersion,
		InstallDisabledReason: installDisabledReason,
		Configure:             client.Configure,
		Search:                client.Search,
		Detail:                client.GetAsset,
	})
	if err != nil {
		if projectResult.NotFound != nil && errors.Is(err, tui.ErrAssetLibCancelled) {
			return projectResult.NotFound
		}
		return err
	}
	if projectResult.NotFound != nil {
		return projectResult.NotFound
	}
	spec, err := assetLibSpecFromDetail(selection.Detail, &assetLibFlags{})
	if err != nil {
		return err
	}
	return addAndInstallAssetLibSpec(cmd, opts, projectResult.Project, projectResult.Manifest, spec, selection.AssetID)
}

type optionalProject struct {
	Project  *project.Project
	Manifest *manifest.Manifest
	NotFound error
}

func discoverOptionalProject(dir string) (optionalProject, error) {
	startDir, err := startProjectDir(dir)
	if err != nil {
		return optionalProject{}, err
	}
	discovered, err := project.Discover(startDir)
	if err != nil {
		if errors.Is(err, project.ErrProjectNotFound) {
			return optionalProject{NotFound: err}, nil
		}
		return optionalProject{}, err
	}
	return optionalProject{Project: discovered}, nil
}

func loadOptionalProject(dir string) (optionalProject, error) {
	result, err := discoverOptionalProject(dir)
	if err != nil || result.NotFound != nil {
		return result, err
	}
	addonManifest, err := manifest.Load(result.Project.ManifestPath)
	if err != nil {
		return optionalProject{}, &output.ManifestError{Err: err}
	}
	if err := addonManifest.Validate(); err != nil {
		return optionalProject{}, err
	}
	result.Manifest = addonManifest
	return result, nil
}

func startProjectDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return workingDir, nil
}

func newAssetLibSearchCommand(opts *Options) *cobra.Command {
	flagValues := &assetLibFlags{maxResults: 20}
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search Godot AssetLib addons",
		Args:  usageExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAssetLibSearch(cmd, opts, flagValues, args[0])
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&flagValues.godotVersion, "godot-version", "", "Godot version filter (for example 4.2)")
	flags.StringVar(&flagValues.category, "category", "", "AssetLib category id or name")
	flags.StringVar(&flagValues.support, "support", "", "support level: official, featured, community, or testing")
	flags.StringVar(&flagValues.sort, "sort", "", "sort order: rating, cost, name, or updated")
	flags.IntVar(&flagValues.maxResults, "max-results", 20, "maximum results to fetch (1-500)")
	flags.StringVar(&flagValues.dir, "dir", "", "start directory for project discovery")
	return cmd
}

func newAssetLibAddCommand(opts *Options) *cobra.Command {
	flagValues := &assetLibFlags{}
	cmd := &cobra.Command{
		Use:   "add <asset-id>",
		Short: "Add and install one AssetLib addon by id",
		Args:  usageExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAssetLibAdd(cmd, opts, flagValues, args[0])
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&flagValues.name, "name", "", "addon name (table key under [addons])")
	flags.StringVar(&flagValues.sourcePath, "source-path", "", "subdirectory within the downloaded asset to install")
	flags.StringVar(&flagValues.installAs, "install-as", "", "install directory name (default: addon name)")
	flags.StringVar(&flagValues.dir, "dir", "", "start directory for project discovery")
	return cmd
}

func runAssetLibSearch(cmd *cobra.Command, opts *Options, flagValues *assetLibFlags, query string) error {
	if flagValues.maxResults < 1 || flagValues.maxResults > 500 {
		return &UsageError{Err: fmt.Errorf("--max-results must be between 1 and 500")}
	}
	if err := validateAssetLibSearchFilters(flagValues); err != nil {
		return err
	}
	projectResult, err := discoverOptionalProject(flagValues.dir)
	if err != nil {
		return err
	}
	godotVersion := flagValues.godotVersion
	if godotVersion == "" && projectResult.Project != nil {
		godotVersion = project.DetectGodotVersion(projectResult.Project.Root)
	}
	if godotVersion == "" && projectResult.Project == nil {
		godotVersion, err = latestStableGodotVersion(cmd.Context())
		if err != nil {
			return err
		}
	}
	if godotVersion == "" {
		return &UsageError{Err: fmt.Errorf("--godot-version is required when project.godot config/features does not include a Godot version")}
	}
	client := assetLibClientFor()
	category, err := resolveAssetLibCategory(cmd.Context(), client, flagValues.category)
	if err != nil {
		return err
	}
	results, err := client.Search(cmd.Context(), assetlib.SearchOptions{
		Query:        query,
		GodotVersion: godotVersion,
		Category:     category,
		Support:      flagValues.support,
		Sort:         flagValues.sort,
		MaxResults:   flagValues.maxResults,
	})
	if err != nil {
		return err
	}
	return output.Render(cmd.OutOrStdout(), opts.JSON, results.Results, func() {
		if opts.Quiet {
			return
		}
		if len(results.Results) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no AssetLib results matched")
			return
		}
		for _, result := range results.Results {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %s  %s  %s  Godot %s\n",
				result.AssetID, result.Title, result.Author, result.Category, result.Cost, result.VersionString, result.GodotVersion)
		}
	})
}

func runAssetLibAdd(cmd *cobra.Command, opts *Options, flagValues *assetLibFlags, assetID string) error {
	assetID = strings.TrimSpace(assetID)
	if !assetLibIDPattern.MatchString(assetID) {
		return &UsageError{Err: fmt.Errorf("asset id %q must be a positive integer", assetID)}
	}
	discovered, addonManifest, err := loadProject(flagValues.dir)
	if err != nil {
		return err
	}
	detail, err := assetLibClientFor().GetAsset(cmd.Context(), assetID)
	if err != nil {
		return err
	}
	spec, err := assetLibSpecFromDetail(detail, flagValues)
	if err != nil {
		return err
	}
	return addAndInstallAssetLibSpec(cmd, opts, discovered, addonManifest, spec, assetID)
}

func addAndInstallAssetLibSpec(
	cmd *cobra.Command,
	opts *Options,
	discovered *project.Project,
	addonManifest *manifest.Manifest,
	spec manifest.AddonSpec,
	assetID string,
) error {
	if _, exists := addonManifest.Addons[spec.Name]; exists {
		return &UsageError{Err: fmt.Errorf("addon %q already exists; pass --name to choose a different manifest key", spec.Name)}
	}
	single := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{spec.Name: spec}}
	if err := single.Validate(); err != nil {
		return err
	}
	addonManifest.Addons[spec.Name] = spec
	if err := addonManifest.Save(discovered.ManifestPath); err != nil {
		return err
	}
	runner := NewRunner(discovered.AddonsDir, discovered.LockPath)
	if testFetcherFor != nil {
		runner.FetcherFor = testFetcherFor
	}
	results, err := runner.InstallAddons(cmd.Context(), addonManifest, []string{spec.Name}, ModeInstall)
	if err != nil {
		delete(addonManifest.Addons, spec.Name)
		if rollbackErr := addonManifest.Save(discovered.ManifestPath); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	return output.Render(cmd.OutOrStdout(), opts.JSON, results, func() {
		if opts.Quiet {
			return
		}
		for _, result := range results {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "added and installed %s from AssetLib asset %s\n", result.Name, assetID)
		}
	})
}

func assetLibSpecFromDetail(detail assetlib.AssetDetail, flagValues *assetLibFlags) (manifest.AddonSpec, error) {
	if detail.Type != "" && detail.Type != "addon" {
		return manifest.AddonSpec{}, &UsageError{Err: fmt.Errorf("AssetLib asset %s is type %q, not addon", detail.AssetID, detail.Type)}
	}
	if detail.DownloadURL == "" {
		return manifest.AddonSpec{}, &UsageError{Err: fmt.Errorf("AssetLib asset %s has no download_url", detail.AssetID)}
	}
	if err := validateAssetLibDownloadURL(detail.DownloadURL); err != nil {
		return manifest.AddonSpec{}, err
	}
	name := flagValues.name
	if name == "" {
		name = assetlib.ManifestNameFromTitle(detail.Title)
	}
	if name == "" {
		return manifest.AddonSpec{}, &UsageError{Err: fmt.Errorf("could not derive addon name from AssetLib title %q; pass --name", detail.Title)}
	}
	spec := manifest.AddonSpec{
		Name:       name,
		Source:     manifest.SourceArchive,
		URL:        detail.DownloadURL,
		Version:    detail.VersionString,
		SourcePath: flagValues.sourcePath,
		InstallAs:  flagValues.installAs,
	}
	if assetLibDownloadHashPattern.MatchString(detail.DownloadHash) {
		spec.Checksum = detail.DownloadHash
	}
	return spec, nil
}

func resolveAssetLibCategory(ctx context.Context, client assetLibClient, category string) (string, error) {
	if category == "" {
		return "", nil
	}
	if _, err := strconv.Atoi(category); err == nil {
		return category, nil
	}
	config, err := client.Configure(ctx, "addon")
	if err != nil {
		return "", err
	}
	for _, candidate := range config.Categories {
		if strings.EqualFold(candidate.Name, category) {
			return candidate.ID, nil
		}
	}
	return "", &UsageError{Err: fmt.Errorf("unknown AssetLib category %q", category)}
}

func validateAssetLibSearchFilters(flagValues *assetLibFlags) error {
	if flagValues.support != "" {
		if _, ok := assetLibSupportValues[flagValues.support]; !ok {
			return &UsageError{Err: fmt.Errorf("--support must be one of: official, featured, community, testing")}
		}
	}
	if flagValues.sort != "" {
		if _, ok := assetLibSortValues[flagValues.sort]; !ok {
			return &UsageError{Err: fmt.Errorf("--sort must be one of: rating, cost, name, updated")}
		}
	}
	return nil
}

func validateAssetLibDownloadURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return &UsageError{Err: fmt.Errorf("AssetLib download_url %q is not a valid URL: %w", rawURL, err)}
	}
	switch parsed.Scheme {
	case "http", "https":
		return nil
	default:
		return &UsageError{Err: fmt.Errorf("AssetLib download_url %q must use http:// or https://", rawURL)}
	}
}

func assetLibClientFor() assetLibClient {
	if testAssetLibClient != nil {
		return testAssetLibClient
	}
	return assetlib.NewClient("")
}
