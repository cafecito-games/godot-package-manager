package tui

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/assetlib"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

type quittingModel struct{}

func (quittingModel) Init() tea.Cmd {
	return tea.Quit
}

func (quittingModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return quittingModel{}, nil
}

func (quittingModel) View() string {
	return "done"
}

func TestAssetLibStateSelectsResultForConfirmation(t *testing.T) {
	state := newAssetLibState("4.2")
	state.setQuery("dialogue")
	state.setResults([]assetlib.AssetSummary{
		{AssetID: "2598", Title: "Dialogue Engine"},
		{AssetID: "4154", Title: "Dialogue"},
	})
	state.moveSelection(1)

	selected := state.selected()

	require.Equal(t, "4154", selected.AssetID)
	require.Equal(t, "Dialogue", selected.Title)
}

func TestAssetLibStateClampsSelection(t *testing.T) {
	state := newAssetLibState("4.2")
	state.setResults([]assetlib.AssetSummary{{AssetID: "2598"}})

	state.moveSelection(3)
	require.Equal(t, "2598", state.selected().AssetID)

	state.moveSelection(-3)
	require.Equal(t, "2598", state.selected().AssetID)
}

func TestAssetLibStateApplyCategoryHandlesOutOfRangeSelection(t *testing.T) {
	state := newAssetLibState("4.6")
	state.categoryIdx = 1

	require.NotPanics(t, state.applySelectedCategory)
	require.Equal(t, 0, state.categoryIdx)
	require.Empty(t, state.categoryID)
	require.Empty(t, state.categoryName)
}

func TestAssetLibInitialStagePromptsForMissingVersion(t *testing.T) {
	require.Equal(t, assetLibStageVersion, initialAssetLibModel(AssetLibConfig{}).stage)
	require.Equal(t, assetLibStageSearch, initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.2"}).stage)
}

func TestAssetLibProgramUsesAltScreen(t *testing.T) {
	var out bytes.Buffer
	program := newAssetLibProgram(
		quittingModel{},
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(&out),
		tea.WithoutSignals(),
	)

	_, err := program.Run()

	require.NoError(t, err)
	require.Contains(t, out.String(), "\x1b[?1049h")
	require.Contains(t, out.String(), "\x1b[?1049l")
}

func TestAssetLibResultsViewUsesDashboardStyle(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageResults
	model.state.setQuery("dialogue")
	model.state.setSearchResponse(assetlib.SearchResponse{
		Pages:      2,
		TotalItems: 34,
		Results: []assetlib.AssetSummary{
			{
				AssetID:       "2598",
				Title:         "Dialogue Engine",
				Author:        "Rubonnek",
				Category:      "Tools",
				Cost:          "MIT",
				SupportLevel:  "community",
				VersionString: "1.6.0",
				GodotVersion:  "4.2",
				ModifyDate:    "2026-02-27 22:05:18",
			},
		},
	})

	view := model.View()

	require.Contains(t, view, "Godot Asset Library")
	require.Contains(t, view, "version 4.6")
	require.Contains(t, view, "query dialogue")
	require.Contains(t, view, "page 1/2")
	require.Contains(t, view, "01 Dialogue Engine")
	require.Contains(t, view, "Rubonnek")
	require.Contains(t, view, "support community")
	require.Contains(t, view, "20 result cap")
}

func TestAssetLibResultsViewFitsNarrowTerminalWidth(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageResults
	model.height = 24
	model.width = 72
	model.state.setQuery("dialogue")
	model.state.setResults([]assetlib.AssetSummary{
		{
			AssetID:       "2598",
			Title:         "Dialogue Engine",
			Author:        "Rubonnek",
			Category:      "Tools",
			Cost:          "MIT",
			SupportLevel:  "community",
			VersionString: "1.6.0",
			GodotVersion:  "4.2",
			ModifyDate:    "2026-02-27 22:05:18",
		},
	})

	view := model.View()

	require.LessOrEqual(t, lipgloss.Width(view), model.width)
}

func TestAssetLibDetailViewUsesDashboardStyle(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageDetail
	model.state.detail = assetlib.AssetDetail{
		AssetSummary: assetlib.AssetSummary{
			AssetID:       "2598",
			Title:         "Dialogue Engine",
			Author:        "Rubonnek",
			Category:      "Tools",
			Cost:          "MIT",
			VersionString: "1.6.0",
		},
		DownloadURL: "https://example.com/dialogue.zip",
	}

	view := model.View()

	require.Contains(t, view, "Install Asset")
	require.Contains(t, view, "Dialogue Engine")
	require.Contains(t, view, "install_as dialogue_engine")
	require.Contains(t, view, "https://example.com/dialogue.zip")
}

func TestAssetLibDetailViewShowsInvalidInstallName(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageDetail
	model.state.detail = assetlib.AssetDetail{
		AssetSummary: assetlib.AssetSummary{
			AssetID: "2598",
			Title:   "!!!",
		},
		DownloadURL: "https://example.com/dialogue.zip",
	}

	view := model.View()

	require.Contains(t, view, "could not derive addon name")
	require.NotContains(t, view, "custom_name")
}

func TestAssetLibDetailDoesNotInstallWithoutTargetProject(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{
		InitialGodotVersion:   "4.6",
		InstallDisabledReason: "no project.godot found",
	})
	model.stage = assetLibStageDetail
	model.state.detail = assetlib.AssetDetail{
		AssetSummary: assetlib.AssetSummary{
			AssetID: "2598",
			Title:   "Dialogue Engine",
		},
		DownloadURL: "https://example.com/dialogue.zip",
	}

	next, cmd := model.advanceAssetLib()
	nextModel := next.(assetLibModel)

	require.Nil(t, cmd)
	require.Equal(t, assetLibStageDetail, nextModel.stage)
	require.Empty(t, nextModel.selection.AssetID)
	require.Contains(t, nextModel.View(), "no project.godot found")
	require.NotContains(t, nextModel.View(), "enter install")
}

func TestAssetLibSearchCommandUsesConfiguredContextAndCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := initialAssetLibModel(AssetLibConfig{
		Context:             parent,
		InitialGodotVersion: "4.6",
		Search: func(ctx context.Context, _ assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			if err := ctx.Err(); err != nil {
				return assetlib.SearchResponse{}, err
			}
			return assetlib.SearchResponse{}, fmt.Errorf("context was not canceled")
		},
	})
	model.searchInput.SetValue("dialogue")
	next, cmd := model.advanceAssetLib()
	nextModel := next.(assetLibModel)
	_, _ = nextModel.updateKey(tea.KeyMsg{Type: tea.KeyEsc})

	msg := cmd().(assetLibSearchMsg)

	require.ErrorIs(t, msg.err, context.Canceled)
}

func TestAssetLibIgnoresUnsolicitedSearchResponse(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageCategories
	model.state.setCategories([]assetlib.Category{{ID: "5", Name: "Tools"}})

	next, cmd := model.Update(assetLibSearchMsg{
		response: assetlib.SearchResponse{Results: []assetlib.AssetSummary{{AssetID: "2598"}}},
	})
	nextModel := next.(assetLibModel)

	require.Nil(t, cmd)
	require.Equal(t, assetLibStageCategories, nextModel.stage)
	require.Empty(t, nextModel.state.results)
}

func TestAssetLibSearchErrorShowsRecoverableError(t *testing.T) {
	apiErr := fmt.Errorf("assetlib unavailable")
	model := initialAssetLibModel(AssetLibConfig{
		InitialGodotVersion: "4.6",
		Search: func(context.Context, assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			return assetlib.SearchResponse{}, apiErr
		},
	})
	model.searchInput.SetValue("dialogue")
	next, cmd := model.advanceAssetLib()
	nextModel := next.(assetLibModel)

	msg := cmd().(assetLibSearchMsg)
	next, followup := nextModel.Update(msg)
	nextModel = next.(assetLibModel)

	require.Nil(t, followup)
	require.Equal(t, assetLibStageError, nextModel.stage)
	require.Empty(t, nextModel.err)
	require.Contains(t, nextModel.View(), "assetlib unavailable")
	require.Contains(t, nextModel.View(), "enter retry")
}

func TestRunAssetLibBrowserReturnsCancelledSentinel(t *testing.T) {
	var out bytes.Buffer
	program := newAssetLibProgram(
		initialAssetLibModel(AssetLibConfig{
			InitialGodotVersion: "4.6",
			Configure: func(context.Context, string) (assetlib.Configuration, error) {
				return assetlib.Configuration{}, nil
			},
			Search: func(context.Context, assetlib.SearchOptions) (assetlib.SearchResponse, error) {
				return assetlib.SearchResponse{}, nil
			},
			Detail: func(context.Context, string) (assetlib.AssetDetail, error) {
				return assetlib.AssetDetail{}, nil
			},
		}),
		tea.WithInput(strings.NewReader("\x1b")),
		tea.WithOutput(&out),
		tea.WithoutSignals(),
	)

	final, err := program.Run()
	require.NoError(t, err)
	finalModel := final.(assetLibModel)

	require.ErrorIs(t, finalModel.err, ErrAssetLibCancelled)
}

func TestAssetLibResultsViewLimitsRowsToTerminalHeight(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageResults
	model.height = 18
	model.state.setQuery("dialogue")
	var results []assetlib.AssetSummary
	for index := 1; index <= 12; index++ {
		results = append(results, assetlib.AssetSummary{
			AssetID:       strconv.Itoa(2500 + index),
			Title:         fmt.Sprintf("Addon %02d", index),
			Author:        "Author",
			Category:      "Tools",
			VersionString: "1.0.0",
			GodotVersion:  "4.6",
		})
	}
	model.state.setResults(results)

	view := model.View()

	require.Contains(t, view, "showing 1-1 of 12")
	require.Contains(t, view, "01 Addon 01")
	require.NotContains(t, view, "02 Addon 02")
	require.NotContains(t, view, "12 Addon 12")
	require.LessOrEqual(t, lipgloss.Height(view), model.height)
}

func TestAssetLibResultsViewLimitsRowsBeforeWindowSize(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageResults
	model.state.setQuery("dialogue")
	var results []assetlib.AssetSummary
	for index := 1; index <= 12; index++ {
		results = append(results, assetlib.AssetSummary{
			AssetID:       strconv.Itoa(2500 + index),
			Title:         fmt.Sprintf("Addon %02d", index),
			Author:        "Author",
			Category:      "Tools",
			VersionString: "1.0.0",
			GodotVersion:  "4.6",
		})
	}
	model.state.setResults(results)

	view := model.View()

	require.Contains(t, view, "showing 1-2 of 12")
	require.Contains(t, view, "02 Addon 02")
	require.NotContains(t, view, "03 Addon 03")
	require.LessOrEqual(t, lipgloss.Height(view), model.height)
}

func TestAssetLibResultsViewScrollsWithSelection(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageResults
	model.height = 18
	model.state.setQuery("dialogue")
	var results []assetlib.AssetSummary
	for index := 1; index <= 12; index++ {
		results = append(results, assetlib.AssetSummary{
			AssetID:       strconv.Itoa(2500 + index),
			Title:         fmt.Sprintf("Addon %02d", index),
			Author:        "Author",
			Category:      "Tools",
			VersionString: "1.0.0",
			GodotVersion:  "4.6",
		})
	}
	model.state.setResults(results)
	model.state.moveSelection(8)

	view := model.View()

	require.Contains(t, view, "showing 9-9 of 12")
	require.Contains(t, view, "09 Addon 09")
	require.NotContains(t, view, "01 Addon 01")
	require.NotContains(t, view, "12 Addon 12")
	require.LessOrEqual(t, lipgloss.Height(view), model.height)
}

func TestAssetLibCategoryDrawerAppliesCategory(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{
		InitialGodotVersion: "4.6",
		Search: func(_ context.Context, _ assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			return assetlib.SearchResponse{}, nil
		},
	})
	model.stage = assetLibStageCategories
	model.state.setCategories([]assetlib.Category{
		{ID: "5", Name: "Tools"},
		{ID: "6", Name: "Scripts"},
	})
	model.state.moveCategory(1)

	next, cmd := model.advanceAssetLib()
	nextModel := next.(assetLibModel)

	require.NotNil(t, cmd)
	require.Equal(t, assetLibStageLoading, nextModel.stage)
	require.Equal(t, "5", nextModel.state.categoryID)
	require.Equal(t, "Tools", nextModel.state.categoryName)
	require.Contains(t, nextModel.View(), "category Tools")
}

func TestAssetLibSearchCommandIncludesSelectedCategory(t *testing.T) {
	var got assetlib.SearchOptions
	model := initialAssetLibModel(AssetLibConfig{
		InitialGodotVersion: "4.6",
		Search: func(_ context.Context, opts assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			got = opts
			return assetlib.SearchResponse{}, nil
		},
	})
	model.state.setQuery("dialogue")
	model.state.categoryID = "5"
	model.state.categoryName = "Tools"

	msg := model.searchCmd()().(assetLibSearchMsg)

	require.NoError(t, msg.err)
	require.Equal(t, "dialogue", got.Query)
	require.Equal(t, "5", got.Category)
	require.Equal(t, "4.6", got.GodotVersion)
}

func TestAssetLibResultsCanFetchNextPage(t *testing.T) {
	var got assetlib.SearchOptions
	model := initialAssetLibModel(AssetLibConfig{
		InitialGodotVersion: "4.6",
		Search: func(_ context.Context, opts assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			got = opts
			return assetlib.SearchResponse{Page: opts.Page, Pages: 3}, nil
		},
	})
	model.stage = assetLibStageResults
	model.state.setQuery("dialogue")
	model.state.setSearchResponse(assetlib.SearchResponse{
		Page:    0,
		Pages:   3,
		Results: []assetlib.AssetSummary{{AssetID: "2598", Title: "Dialogue Engine"}},
	})

	next, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	nextModel := next.(assetLibModel)

	require.NotNil(t, cmd)
	require.Equal(t, assetLibStageLoading, nextModel.stage)

	msg := cmd().(assetLibSearchMsg)
	require.NoError(t, msg.err)
	require.Equal(t, 1, got.Page)
}

func TestAssetLibResultsCanFetchPreviousPage(t *testing.T) {
	var got assetlib.SearchOptions
	model := initialAssetLibModel(AssetLibConfig{
		InitialGodotVersion: "4.6",
		Search: func(_ context.Context, opts assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			got = opts
			return assetlib.SearchResponse{Page: opts.Page, Pages: 3}, nil
		},
	})
	model.stage = assetLibStageResults
	model.state.setQuery("dialogue")
	model.state.setSearchResponse(assetlib.SearchResponse{
		Page:    2,
		Pages:   3,
		Results: []assetlib.AssetSummary{{AssetID: "2598", Title: "Dialogue Engine"}},
	})

	next, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	nextModel := next.(assetLibModel)

	require.NotNil(t, cmd)
	require.Equal(t, assetLibStageLoading, nextModel.stage)

	msg := cmd().(assetLibSearchMsg)
	require.NoError(t, msg.err)
	require.Equal(t, 1, got.Page)
}

func TestAssetLibCanBrowseSelectedCategoryWithoutQuery(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.state.categoryID = "5"
	model.state.categoryName = "Tools"

	next, cmd := model.advanceAssetLib()
	nextModel := next.(assetLibModel)

	require.NotNil(t, cmd)
	require.Equal(t, assetLibStageLoading, nextModel.stage)
	require.Equal(t, "", nextModel.state.query)
}

func TestAssetLibCategoryDrawerViewListsCategories(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageCategories
	model.state.setCategories([]assetlib.Category{
		{ID: "5", Name: "Tools"},
		{ID: "6", Name: "Scripts"},
	})

	view := model.View()

	require.Contains(t, view, "Categories")
	require.Contains(t, view, "All categories")
	require.Contains(t, view, "Tools")
	require.Contains(t, view, "Scripts")
}

func TestAssetLibCategoryDrawerMovesSelectionWithArrowKeys(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageCategories
	model.state.setCategories([]assetlib.Category{
		{ID: "5", Name: "Tools"},
		{ID: "6", Name: "Scripts"},
	})

	next, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyDown})
	nextModel := next.(assetLibModel)

	require.Nil(t, cmd)
	require.Equal(t, 1, nextModel.state.categoryIdx)

	next, cmd = nextModel.updateKey(tea.KeyMsg{Type: tea.KeyUp})
	nextModel = next.(assetLibModel)

	require.Nil(t, cmd)
	require.Equal(t, 0, nextModel.state.categoryIdx)
}

func TestAssetLibCategoryDrawerSearchesWithinTypedQuery(t *testing.T) {
	var got assetlib.SearchOptions
	model := initialAssetLibModel(AssetLibConfig{
		InitialGodotVersion: "4.6",
		Search: func(_ context.Context, opts assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			got = opts
			return assetlib.SearchResponse{}, nil
		},
	})
	model.searchInput.SetValue("dialogue")
	model.state.setCategories([]assetlib.Category{{ID: "5", Name: "Tools"}})

	next, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	categoryModel := next.(assetLibModel)

	require.Nil(t, cmd)
	require.Equal(t, assetLibStageCategories, categoryModel.stage)

	categoryModel.state.moveCategory(1)
	next, cmd = categoryModel.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	nextModel := next.(assetLibModel)

	require.NotNil(t, cmd)
	require.Equal(t, assetLibStageLoading, nextModel.stage)

	msg := cmd().(assetLibSearchMsg)
	require.NoError(t, msg.err)
	require.Equal(t, "dialogue", got.Query)
	require.Equal(t, "5", got.Category)
}

func TestAssetLibCategoryDrawerLoadsCategories(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{
		InitialGodotVersion: "4.6",
		Configure: func(_ context.Context, assetType string) (assetlib.Configuration, error) {
			require.Equal(t, "addon", assetType)
			return assetlib.Configuration{Categories: []assetlib.Category{{ID: "5", Name: "Tools"}}}, nil
		},
	})

	next, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	nextModel := next.(assetLibModel)

	require.NotNil(t, cmd)
	require.Equal(t, assetLibStageLoadingCategories, nextModel.stage)

	msg := cmd().(assetLibCategoriesMsg)
	require.NoError(t, msg.err)
	require.Equal(t, "Tools", msg.config.Categories[0].Name)
}

func TestAssetLibCategoryViewLimitsRowsToTerminalHeight(t *testing.T) {
	model := initialAssetLibModel(AssetLibConfig{InitialGodotVersion: "4.6"})
	model.stage = assetLibStageCategories
	model.height = 18
	var categories []assetlib.Category
	for index := 1; index <= 12; index++ {
		categories = append(categories, assetlib.Category{
			ID:   strconv.Itoa(index),
			Name: fmt.Sprintf("Category %02d", index),
		})
	}
	model.state.setCategories(categories)

	view := model.View()

	require.Contains(t, view, "showing 1-1 of 13")
	require.Contains(t, view, "All categories")
	require.NotContains(t, view, "Category 01")
	require.LessOrEqual(t, lipgloss.Height(view), model.height)

	model.state.moveCategory(8)
	view = model.View()

	require.Contains(t, view, "showing 9-9 of 13")
	require.Contains(t, view, "Category 08")
	require.NotContains(t, view, "All categories")
	require.LessOrEqual(t, lipgloss.Height(view), model.height)
}
