package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/assetlib"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AssetLibSearchFunc searches AssetLib for the interactive browser.
type AssetLibSearchFunc func(context.Context, assetlib.SearchOptions) (assetlib.SearchResponse, error)

// AssetLibDetailFunc fetches AssetLib detail for the interactive browser.
type AssetLibDetailFunc func(context.Context, string) (assetlib.AssetDetail, error)

// AssetLibConfigureFunc fetches AssetLib configuration metadata.
type AssetLibConfigureFunc func(context.Context, string) (assetlib.Configuration, error)

// AssetLibConfig configures the interactive AssetLib browser.
type AssetLibConfig struct {
	Context               context.Context
	InitialGodotVersion   string
	InstallDisabledReason string
	Configure             AssetLibConfigureFunc
	Search                AssetLibSearchFunc
	Detail                AssetLibDetailFunc
}

// AssetLibSelection is the asset chosen by the interactive browser.
type AssetLibSelection struct {
	AssetID string
	Detail  assetlib.AssetDetail
}

type assetLibStage int

const (
	assetLibStageVersion assetLibStage = iota
	assetLibStageSearch
	assetLibStageLoading
	assetLibStageResults
	assetLibStageDetail
	assetLibStageLoadingCategories
	assetLibStageCategories
	assetLibStageError
)

type assetLibRequestKind int

const (
	assetLibRequestNone assetLibRequestKind = iota
	assetLibRequestSearch
	assetLibRequestDetail
	assetLibRequestCategories
)

const (
	assetLibChromeRows        = 12
	assetLibResultCardRows    = 6
	assetLibCategoryCardRows  = 4
	assetLibDefaultMaxResults = 20
	assetLibDefaultFrameWidth = 84
	assetLibFrameOuterWidth   = 6
	assetLibInnerInset        = 8
	assetLibMinContentWidth   = 20
)

// ErrAssetLibCancelled is returned when the user exits the AssetLib browser
// without choosing an installable asset.
var ErrAssetLibCancelled = errors.New("cancelled")

type assetLibState struct {
	godotVersion string
	query        string
	results      []assetlib.AssetSummary
	selectedIdx  int
	page         int
	pages        int
	totalItems   int
	detail       assetlib.AssetDetail
	categories   []assetlib.Category
	categoryIdx  int
	categoryID   string
	categoryName string
}

func newAssetLibState(godotVersion string) *assetLibState {
	return &assetLibState{godotVersion: godotVersion}
}

func (s *assetLibState) setQuery(query string) {
	s.query = query
}

func (s *assetLibState) setResults(results []assetlib.AssetSummary) {
	s.results = results
	s.selectedIdx = 0
	s.page = 0
	s.pages = 0
	s.totalItems = 0
}

func (s *assetLibState) setSearchResponse(response assetlib.SearchResponse) {
	s.results = response.Results
	s.selectedIdx = 0
	s.page = response.Page
	s.pages = response.Pages
	s.totalItems = response.TotalItems
}

func (s *assetLibState) moveSelection(delta int) {
	if len(s.results) == 0 {
		s.selectedIdx = 0
		return
	}
	s.selectedIdx += delta
	if s.selectedIdx < 0 {
		s.selectedIdx = 0
	}
	if s.selectedIdx >= len(s.results) {
		s.selectedIdx = len(s.results) - 1
	}
}

func (s *assetLibState) selected() assetlib.AssetSummary {
	if len(s.results) == 0 {
		return assetlib.AssetSummary{}
	}
	return s.results[s.selectedIdx]
}

func (s *assetLibState) hasNextPage() bool {
	return s.pages > 0 && s.page+1 < s.pages
}

func (s *assetLibState) hasPreviousPage() bool {
	return s.page > 0
}

func (s *assetLibState) setCategories(categories []assetlib.Category) {
	s.categories = categories
	s.categoryIdx = 0
	for index, category := range categories {
		if category.ID == s.categoryID {
			s.categoryIdx = index + 1
			return
		}
	}
}

func (s *assetLibState) moveCategory(delta int) {
	s.categoryIdx += delta
	if s.categoryIdx < 0 {
		s.categoryIdx = 0
	}
	if s.categoryIdx > len(s.categories) {
		s.categoryIdx = len(s.categories)
	}
}

func (s *assetLibState) applySelectedCategory() {
	if s.categoryIdx <= 0 || s.categoryIdx > len(s.categories) {
		s.categoryIdx = 0
		s.categoryID = ""
		s.categoryName = ""
		return
	}
	category := s.categories[s.categoryIdx-1]
	s.categoryID = category.ID
	s.categoryName = category.Name
}

type assetLibModel struct {
	config               AssetLibConfig
	state                *assetLibState
	stage                assetLibStage
	height               int
	width                int
	ctx                  context.Context
	cancel               context.CancelFunc
	versionInput         textinput.Model
	searchInput          textinput.Model
	err                  error
	requestErr           error
	selection            AssetLibSelection
	returnStage          assetLibStage
	previousStage        assetLibStage
	requestSeq           int
	activeRequestID      int
	loadingKind          assetLibRequestKind
	failedKind           assetLibRequestKind
	pendingDetailAssetID string
}

type assetLibSearchMsg struct {
	requestID int
	response  assetlib.SearchResponse
	err       error
}

type assetLibDetailMsg struct {
	requestID int
	detail    assetlib.AssetDetail
	err       error
}

type assetLibCategoriesMsg struct {
	requestID int
	config    assetlib.Configuration
	err       error
}

var (
	assetLibFrameStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("63")).
				Padding(1, 2)
	assetLibTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("81")).
				Bold(true)
	assetLibSubtitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
	assetLibChipStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("238")).
				Foreground(lipgloss.Color("153")).
				Padding(0, 1).
				MarginRight(1)
	assetLibInputStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("238")).
				Padding(0, 1).
				MarginTop(1).
				MarginBottom(1)
	assetLibCardStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("238")).
				Padding(0, 1).
				MarginBottom(1)
	assetLibSelectedCardStyle = lipgloss.NewStyle().
					Border(lipgloss.NormalBorder()).
					BorderForeground(lipgloss.Color("75")).
					Padding(0, 1).
					MarginBottom(1).
					Background(lipgloss.Color("17"))
	assetLibNumberStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("220")).
				Bold(true)
	assetLibNameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("255")).
				Bold(true)
	assetLibMetaStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
	assetLibHelpStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241")).
				MarginTop(1)
)

func initialAssetLibModel(config AssetLibConfig) assetLibModel {
	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	versionInput := textinput.New()
	versionInput.Placeholder = "4.2"
	searchInput := textinput.New()
	searchInput.Placeholder = "Search addons"
	stage := assetLibStageSearch
	if strings.TrimSpace(config.InitialGodotVersion) == "" {
		stage = assetLibStageVersion
		versionInput.Focus()
	} else {
		searchInput.Focus()
	}
	return assetLibModel{
		config:       config,
		state:        newAssetLibState(strings.TrimSpace(config.InitialGodotVersion)),
		stage:        stage,
		height:       24,
		width:        assetLibDefaultFrameWidth + assetLibFrameOuterWidth,
		ctx:          ctx,
		cancel:       cancel,
		versionInput: versionInput,
		searchInput:  searchInput,
	}
}

// RunAssetLibBrowser runs the interactive AssetLib search and selection TUI.
func RunAssetLibBrowser(config AssetLibConfig) (AssetLibSelection, error) {
	if config.Search == nil {
		return AssetLibSelection{}, fmt.Errorf("assetlib search function is required")
	}
	if config.Configure == nil {
		return AssetLibSelection{}, fmt.Errorf("assetlib configure function is required")
	}
	if config.Detail == nil {
		return AssetLibSelection{}, fmt.Errorf("assetlib detail function is required")
	}
	model := initialAssetLibModel(config)
	// Bubble Tea copies model values during Update. The cancel func itself is
	// shared by those copies, so cancelling the initial model tears down any
	// in-flight requests started by later model values.
	defer model.cancel()
	final, err := newAssetLibProgram(model).Run()
	if err != nil {
		return AssetLibSelection{}, err
	}
	finalModel, ok := final.(assetLibModel)
	if !ok {
		return AssetLibSelection{}, fmt.Errorf("unexpected model type from tea program")
	}
	if finalModel.err != nil {
		return AssetLibSelection{}, finalModel.err
	}
	if finalModel.selection.AssetID == "" {
		return AssetLibSelection{}, ErrAssetLibCancelled
	}
	return finalModel.selection, nil
}

func newAssetLibProgram(model tea.Model, opts ...tea.ProgramOption) *tea.Program {
	options := append([]tea.ProgramOption{tea.WithAltScreen()}, opts...)
	return tea.NewProgram(model, options...)
}

func (m assetLibModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m assetLibModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.updateKey(msg)
	case assetLibSearchMsg:
		if !m.acceptsRequest(msg.requestID) {
			return m, nil
		}
		if msg.err != nil {
			return m.handleRequestError(msg.err), nil
		}
		m.state.setSearchResponse(msg.response)
		m.stage = assetLibStageResults
		m.finishRequest()
		return m, nil
	case assetLibDetailMsg:
		if !m.acceptsRequest(msg.requestID) {
			return m, nil
		}
		if msg.err != nil {
			return m.handleRequestError(msg.err), nil
		}
		m.state.detail = msg.detail
		m.pendingDetailAssetID = ""
		m.stage = assetLibStageDetail
		m.finishRequest()
		return m, nil
	case assetLibCategoriesMsg:
		if !m.acceptsRequest(msg.requestID) {
			return m, nil
		}
		if msg.err != nil {
			return m.handleRequestError(msg.err), nil
		}
		m.state.setCategories(msg.config.Categories)
		m.stage = assetLibStageCategories
		m.finishRequest()
		return m, nil
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		return m, nil
	default:
		var cmd tea.Cmd
		switch m.stage {
		case assetLibStageVersion:
			m.versionInput, cmd = m.versionInput.Update(msg)
		case assetLibStageSearch:
			m.searchInput, cmd = m.searchInput.Update(msg)
		}
		return m, cmd
	}
}

func (m assetLibModel) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c", "esc":
		m.cancel()
		m.err = ErrAssetLibCancelled
		return m, tea.Quit
	case "up":
		switch m.stage {
		case assetLibStageResults:
			m.state.moveSelection(-1)
		case assetLibStageCategories:
			m.state.moveCategory(-1)
		}
	case "down":
		switch m.stage {
		case assetLibStageResults:
			m.state.moveSelection(1)
		case assetLibStageCategories:
			m.state.moveCategory(1)
		}
	case "n":
		if m.stage == assetLibStageResults && m.state.hasNextPage() {
			return m.startSearchRequest(assetLibStageResults, m.state.page+1)
		}
	case "p":
		if m.stage == assetLibStageResults && m.state.hasPreviousPage() {
			return m.startSearchRequest(assetLibStageResults, m.state.page-1)
		}
	case "b":
		if m.stage == assetLibStageDetail {
			m.stage = assetLibStageResults
		}
		if m.stage == assetLibStageCategories {
			m.stage = m.returnStage
			if m.stage == assetLibStageLoading || m.stage == assetLibStageLoadingCategories {
				m.stage = assetLibStageSearch
			}
		}
		if m.stage == assetLibStageError {
			m.requestErr = nil
			m.failedKind = assetLibRequestNone
			m.stage = m.previousStage
			if m.stage == assetLibStageLoading || m.stage == assetLibStageLoadingCategories || m.stage == assetLibStageError {
				m.stage = assetLibStageSearch
			}
		}
	case "f":
		if m.stage == assetLibStageSearch || m.stage == assetLibStageResults {
			m.returnStage = m.stage
			if m.stage == assetLibStageSearch {
				m.state.setQuery(strings.TrimSpace(m.searchInput.Value()))
			}
			if len(m.state.categories) > 0 {
				m.stage = assetLibStageCategories
				return m, nil
			}
			return m.startCategoriesRequest()
		}
	case "enter":
		return m.advanceAssetLib()
	}

	var cmd tea.Cmd
	switch m.stage {
	case assetLibStageVersion:
		m.versionInput, cmd = m.versionInput.Update(key)
	case assetLibStageSearch:
		m.searchInput, cmd = m.searchInput.Update(key)
	}
	return m, cmd
}

func (m assetLibModel) advanceAssetLib() (tea.Model, tea.Cmd) {
	switch m.stage {
	case assetLibStageVersion:
		version := strings.TrimSpace(m.versionInput.Value())
		if version == "" {
			return m, nil
		}
		m.state.godotVersion = version
		m.stage = assetLibStageSearch
		m.versionInput.Blur()
		m.searchInput.Focus()
		return m, nil
	case assetLibStageSearch:
		query := strings.TrimSpace(m.searchInput.Value())
		if query == "" && m.state.categoryID == "" {
			return m, nil
		}
		m.state.setQuery(query)
		return m.startSearchRequest(assetLibStageSearch, 0)
	case assetLibStageResults:
		selected := m.state.selected()
		if selected.AssetID == "" {
			m.stage = assetLibStageSearch
			return m, nil
		}
		return m.startDetailRequest(selected.AssetID)
	case assetLibStageDetail:
		if m.config.InstallDisabledReason != "" || assetlib.ManifestNameFromTitle(m.state.detail.Title) == "" {
			return m, nil
		}
		m.selection = AssetLibSelection{
			AssetID: m.state.detail.AssetID,
			Detail:  m.state.detail,
		}
		return m, tea.Quit
	case assetLibStageCategories:
		m.state.applySelectedCategory()
		if m.returnStage == assetLibStageSearch {
			m.state.setQuery(strings.TrimSpace(m.searchInput.Value()))
		}
		if m.state.query != "" || m.state.categoryID != "" {
			return m.startSearchRequest(assetLibStageCategories, 0)
		}
		m.stage = assetLibStageSearch
		return m, nil
	case assetLibStageError:
		return m.retryFailedRequest()
	default:
		return m, nil
	}
}

func (m assetLibModel) startSearchRequest(previous assetLibStage, page int) (tea.Model, tea.Cmd) {
	m.stage = assetLibStageLoading
	m.state.page = page
	m.startRequest(assetLibRequestSearch, previous)
	return m, m.searchCmd()
}

func (m assetLibModel) startDetailRequest(assetID string) (tea.Model, tea.Cmd) {
	m.stage = assetLibStageLoading
	m.pendingDetailAssetID = assetID
	m.startRequest(assetLibRequestDetail, assetLibStageResults)
	return m, m.detailCmd(assetID)
}

func (m assetLibModel) startCategoriesRequest() (tea.Model, tea.Cmd) {
	m.stage = assetLibStageLoadingCategories
	m.startRequest(assetLibRequestCategories, m.returnStage)
	return m, m.categoriesCmd()
}

func (m *assetLibModel) startRequest(kind assetLibRequestKind, previous assetLibStage) {
	m.requestSeq++
	m.activeRequestID = m.requestSeq
	m.loadingKind = kind
	m.previousStage = previous
	m.failedKind = assetLibRequestNone
	m.requestErr = nil
}

func (m assetLibModel) acceptsRequest(requestID int) bool {
	return requestID != 0 && requestID == m.activeRequestID
}

func (m *assetLibModel) finishRequest() {
	m.activeRequestID = 0
	m.loadingKind = assetLibRequestNone
}

func (m assetLibModel) handleRequestError(err error) assetLibModel {
	m.requestErr = err
	m.failedKind = m.loadingKind
	m.finishRequest()
	m.stage = assetLibStageError
	return m
}

func (m assetLibModel) retryFailedRequest() (tea.Model, tea.Cmd) {
	switch m.failedKind {
	case assetLibRequestSearch:
		return m.startSearchRequest(m.previousStage, m.state.page)
	case assetLibRequestDetail:
		if m.pendingDetailAssetID == "" {
			return m, nil
		}
		return m.startDetailRequest(m.pendingDetailAssetID)
	case assetLibRequestCategories:
		return m.startCategoriesRequest()
	default:
		return m, nil
	}
}

func (m assetLibModel) searchCmd() tea.Cmd {
	query := m.state.query
	version := m.state.godotVersion
	category := m.state.categoryID
	page := m.state.page
	search := m.config.Search
	ctx := m.ctx
	requestID := m.activeRequestID
	return func() tea.Msg {
		response, err := search(ctx, assetlib.SearchOptions{
			Query:        query,
			GodotVersion: version,
			Category:     category,
			MaxResults:   assetLibDefaultMaxResults,
			Page:         page,
		})
		return assetLibSearchMsg{requestID: requestID, response: response, err: err}
	}
}

func (m assetLibModel) categoriesCmd() tea.Cmd {
	configure := m.config.Configure
	ctx := m.ctx
	requestID := m.activeRequestID
	return func() tea.Msg {
		config, err := configure(ctx, "addon")
		return assetLibCategoriesMsg{requestID: requestID, config: config, err: err}
	}
}

func (m assetLibModel) detailCmd(assetID string) tea.Cmd {
	detail := m.config.Detail
	ctx := m.ctx
	requestID := m.activeRequestID
	return func() tea.Msg {
		result, err := detail(ctx, assetID)
		return assetLibDetailMsg{requestID: requestID, detail: result, err: err}
	}
}

func (m assetLibModel) View() string {
	switch m.stage {
	case assetLibStageVersion:
		return m.renderFrame(
			"Godot Asset Library",
			"Choose the Godot version used for AssetLib search filtering.",
			m.renderChips("version required")+"\n"+
				m.renderInput("Godot version  "+m.versionInput.View())+"\n"+
				m.renderHelp("enter confirm", "esc cancel"),
		)
	case assetLibStageSearch:
		return m.renderFrame(
			"Godot Asset Library",
			"Search curated addons and install the selected asset into your project.",
			m.renderChips(m.filterChips("type addons")...)+"\n"+
				m.renderInput("Find addons  "+m.searchInput.View())+"\n"+
				m.renderHelp("enter search", "f categories", "20 result cap", "esc cancel"),
		)
	case assetLibStageLoading:
		return m.renderFrame(
			"Godot Asset Library",
			"Loading AssetLib results...",
			m.renderChips(m.filterChips("query "+fallbackText(m.state.query, "all"))...)+"\n"+
				m.renderCard("Fetching metadata from AssetLib."),
		)
	case assetLibStageLoadingCategories:
		return m.renderFrame(
			"Categories",
			"Loading AssetLib categories...",
			m.renderChips(m.filterChips()...)+"\n"+
				m.renderCard("Fetching category metadata from AssetLib."),
		)
	case assetLibStageResults:
		return m.renderResults()
	case assetLibStageDetail:
		return m.renderDetail()
	case assetLibStageCategories:
		return m.renderCategories()
	case assetLibStageError:
		return m.renderError()
	default:
		return ""
	}
}

func (m assetLibModel) renderResults() string {
	if len(m.state.results) == 0 {
		return m.renderFrame(
			"Godot Asset Library",
			"No assets matched this search.",
			m.renderChips(m.filterChips("query "+fallbackText(m.state.query, "all"))...)+"\n"+
				m.renderCard("No results. Try a broader search term.")+"\n"+
				m.renderHelp("enter search again", "f categories", "20 result cap", "esc cancel"),
		)
	}
	var rows []string
	start, end := m.visibleResultRange()
	for index, result := range m.state.results[start:end] {
		resultIndex := start + index
		rows = append(rows, m.renderResultCard(resultIndex, result))
	}
	rangeLabel := m.resultRangeLabel(start, end)
	return m.renderFrame(
		"Godot Asset Library",
		"Browse results and open an asset before installing.",
		m.renderChips(m.filterChips("query "+fallbackText(m.state.query, "all"), m.pageLabel(), rangeLabel)...)+"\n"+
			strings.Join(rows, "\n")+"\n"+
			m.renderHelp(m.resultHelpLabels()...),
	)
}

func (m assetLibModel) renderCategories() string {
	var rows []string
	start, end := m.visibleCategoryRange()
	for index := start; index < end; index++ {
		if index == 0 {
			rows = append(rows, m.renderCategoryCard(0, "All categories", "Clear category filter"))
			continue
		}
		category := m.state.categories[index-1]
		rows = append(rows, m.renderCategoryCard(index, category.Name, "category "+category.ID))
	}
	rangeLabel := fmt.Sprintf("showing %d-%d of %d", start+1, end, len(m.state.categories)+1)
	return m.renderFrame(
		"Categories",
		"Choose a category to browse, or search within that category.",
		m.renderChips(m.filterChips(rangeLabel)...)+"\n"+
			strings.Join(rows, "\n")+"\n"+
			m.renderHelp("up/down select", "enter apply", "b back", "esc cancel"),
	)
}

func (m assetLibModel) renderCategoryCard(index int, name, meta string) string {
	number := fmt.Sprintf("%02d", index+1)
	body := assetLibNumberStyle.Render(number) + " " + assetLibNameStyle.Render(name) + "\n" + assetLibMetaStyle.Render(meta)
	if index == m.state.categoryIdx {
		return m.renderSelectedCard(body)
	}
	return m.renderCard(body)
}

func (m assetLibModel) filterChips(extra ...string) []string {
	chips := []string{"version " + m.state.godotVersion}
	if m.state.categoryName != "" {
		chips = append(chips, "category "+m.state.categoryName)
	}
	chips = append(chips, extra...)
	return chips
}

func (m assetLibModel) visibleResultRange() (int, int) {
	total := len(m.state.results)
	if total == 0 {
		return 0, 0
	}
	return centerWindow(m.state.selectedIdx, total, m.maxVisibleResults())
}

func (m assetLibModel) visibleCategoryRange() (int, int) {
	total := len(m.state.categories) + 1
	return centerWindow(m.state.categoryIdx, total, m.maxVisibleCategories())
}

func centerWindow(selectedIdx, total, maxVisible int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if maxVisible >= total {
		return 0, total
	}
	start := selectedIdx - maxVisible/2
	if start < 0 {
		start = 0
	}
	if start+maxVisible > total {
		start = total - maxVisible
	}
	return start, start + maxVisible
}

func (m assetLibModel) maxVisibleResults() int {
	if m.height <= 0 {
		return 2
	}
	available := m.height - assetLibChromeRows
	if available < 5 {
		return 1
	}
	maxVisible := available / assetLibResultCardRows
	if maxVisible < 1 {
		return 1
	}
	return maxVisible
}

func (m assetLibModel) maxVisibleCategories() int {
	if m.height <= 0 {
		return 2
	}
	available := m.height - assetLibChromeRows
	if available < 5 {
		return 1
	}
	maxVisible := available / assetLibCategoryCardRows
	if maxVisible < 1 {
		return 1
	}
	return maxVisible
}

func (m assetLibModel) renderResultCard(index int, result assetlib.AssetSummary) string {
	number := fmt.Sprintf("%02d", index+1)
	title := assetLibNumberStyle.Render(number) + " " + assetLibNameStyle.Render(result.Title)
	meta := strings.Join(nonEmpty([]string{
		result.Author,
		result.Category,
		result.Cost,
		"version " + fallbackText(result.VersionString, result.Version),
		"Godot " + result.GodotVersion,
		"support " + result.SupportLevel,
		shortDate(result.ModifyDate),
	}), "  ")
	body := title + "\n" + assetLibMetaStyle.Render(meta)
	if result.AssetID != "" {
		body += "\n" + assetLibSubtitleStyle.Render("asset "+result.AssetID)
	}
	if index == m.state.selectedIdx {
		return m.renderSelectedCard(body)
	}
	return m.renderCard(body)
}

func (m assetLibModel) renderDetail() string {
	detail := m.state.detail
	installAs := assetlib.ManifestNameFromTitle(detail.Title)
	lines := nonEmpty([]string{assetLibNameStyle.Render(detail.Title), "author " + detail.Author, "category " + detail.Category})
	if installAs != "" {
		lines = append(lines, "install_as "+installAs)
	} else {
		lines = append(lines, "could not derive addon name from AssetLib title")
	}
	lines = append(lines, nonEmpty([]string{detail.DownloadURL})...)
	var help string
	switch {
	case m.config.InstallDisabledReason != "":
		lines = append(lines, m.config.InstallDisabledReason)
		help = m.renderHelp("b back", "esc cancel")
	case installAs == "":
		help = m.renderHelp("b back", "esc cancel")
	default:
		help = m.renderHelp("enter install", "b back", "esc cancel")
	}
	body := m.renderChips(
		"asset "+detail.AssetID,
		"version "+fallbackText(detail.VersionString, detail.Version),
		"license "+fallbackText(detail.Cost, "unknown"),
	) + "\n" +
		m.renderCard(strings.Join(lines, "\n")) + "\n" +
		help
	return m.renderFrame("Install Asset", "Confirm the AssetLib download before writing addons.toml.", body)
}

func (m assetLibModel) renderError() string {
	message := "AssetLib request failed"
	if m.requestErr != nil {
		message = m.requestErr.Error()
	}
	return m.renderFrame(
		"AssetLib Error",
		"The AssetLib request failed.",
		m.renderChips(m.filterChips()...)+"\n"+
			m.renderCard(message)+"\n"+
			m.renderHelp("enter retry", "b back", "esc cancel"),
	)
}

func (m assetLibModel) renderFrame(title, subtitle, body string) string {
	header := assetLibTitleStyle.Render(title)
	if subtitle != "" {
		header += "\n" + assetLibSubtitleStyle.Render(subtitle)
	}
	return assetLibFrameStyle.Width(m.frameWidth()).Render(header + "\n\n" + body)
}

func (m assetLibModel) frameWidth() int {
	if m.width <= 0 {
		return assetLibDefaultFrameWidth
	}
	width := m.width - assetLibFrameOuterWidth
	if width < assetLibMinContentWidth {
		return assetLibMinContentWidth
	}
	if width > assetLibDefaultFrameWidth {
		return assetLibDefaultFrameWidth
	}
	return width
}

func (m assetLibModel) innerWidth() int {
	width := m.frameWidth() - assetLibInnerInset
	if width < assetLibMinContentWidth {
		return assetLibMinContentWidth
	}
	return width
}

func (m assetLibModel) renderInput(body string) string {
	return assetLibInputStyle.Width(m.innerWidth()).Render(body)
}

func (m assetLibModel) renderCard(body string) string {
	return assetLibCardStyle.Width(m.innerWidth()).Render(body)
}

func (m assetLibModel) renderSelectedCard(body string) string {
	return assetLibSelectedCardStyle.Width(m.innerWidth()).Render(body)
}

func (m assetLibModel) renderChips(labels ...string) string {
	var lines []string
	var current []string
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		chip := assetLibChipStyle.Render(m.truncateChipLabel(label))
		candidate := append(append([]string{}, current...), chip)
		if len(current) == 0 || lipgloss.Width(lipgloss.JoinHorizontal(lipgloss.Top, candidate...)) <= m.frameWidth() {
			current = candidate
			continue
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, current...))
		current = []string{chip}
	}
	if len(current) > 0 {
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, current...))
	}
	return strings.Join(lines, "\n")
}

func (m assetLibModel) renderHelp(labels ...string) string {
	return assetLibHelpStyle.Width(m.frameWidth()).Render(strings.Join(labels, "  "))
}

func (m assetLibModel) pageLabel() string {
	if m.state.pages <= 1 {
		return ""
	}
	return fmt.Sprintf("page %d/%d", m.state.page+1, m.state.pages)
}

func (m assetLibModel) resultHelpLabels() []string {
	labels := []string{"up/down select", "enter details", "f categories"}
	if m.state.hasNextPage() || m.state.hasPreviousPage() {
		labels = append(labels, "n/p pages")
	}
	return append(labels, "20 result cap", "esc cancel")
}

func (m assetLibModel) resultRangeLabel(start, end int) string {
	total := len(m.state.results)
	if m.state.totalItems > 0 {
		total = m.state.totalItems
	}
	offset := 0
	if m.state.page > 0 {
		offset = m.state.page * assetLibDefaultMaxResults
	}
	displayStart := offset + start + 1
	displayEnd := offset + end
	if total > 0 && displayEnd > total {
		displayEnd = total
	}
	return fmt.Sprintf("showing %d-%d of %d", displayStart, displayEnd, total)
}

func (m assetLibModel) truncateChipLabel(label string) string {
	const chipOuterWidth = 5
	width := m.frameWidth() - chipOuterWidth
	if width < assetLibMinContentWidth {
		width = assetLibMinContentWidth
	}
	return truncatePlain(label, width)
}

func truncatePlain(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= len("...") {
		return strings.Repeat(".", width)
	}
	limit := width - len("...")
	var builder strings.Builder
	for _, r := range value {
		next := builder.String() + string(r)
		if lipgloss.Width(next) > limit {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String() + "..."
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func fallbackText(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "unknown"
}

func shortDate(value string) string {
	if len(value) >= len("2006-01-02") {
		return value[:len("2006-01-02")]
	}
	return value
}
