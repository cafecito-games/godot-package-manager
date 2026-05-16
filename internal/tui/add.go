package tui

import (
	"fmt"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// wizardState holds the addon fields collected by the wizard. Its methods are
// pure so they can be unit-tested without a terminal.
type wizardState struct {
	source manifest.SourceType
	values map[string]string
}

func newWizardState() *wizardState {
	return &wizardState{values: map[string]string{}}
}

// setSource records the chosen source type.
func (wizard *wizardState) setSource(source manifest.SourceType) { wizard.source = source }

// set records a field value.
func (wizard *wizardState) set(field, value string) { wizard.values[field] = value }

// fieldOrder returns the input fields to prompt for, given the source type.
func (wizard *wizardState) fieldOrder() []string {
	switch wizard.source {
	case manifest.SourceGit:
		return []string{"name", "url", "version", "source_path", "install_as"}
	case manifest.SourceArchive:
		return []string{"name", "url", "source_path", "install_as"}
	case manifest.SourceGitHubRelease:
		return []string{"name", "repo", "version", "asset", "source_path", "install_as"}
	default:
		return nil
	}
}

// spec assembles a manifest.AddonSpec from the collected values.
func (wizard *wizardState) spec() manifest.AddonSpec {
	return manifest.AddonSpec{
		Name:       wizard.values["name"],
		Source:     wizard.source,
		URL:        wizard.values["url"],
		Repo:       wizard.values["repo"],
		Version:    wizard.values["version"],
		Asset:      wizard.values["asset"],
		SourcePath: wizard.values["source_path"],
		InstallAs:  wizard.values["install_as"],
	}
}

// RunAddWizard runs the interactive Bubble Tea wizard and returns the spec the
// user assembled.
func RunAddWizard() (manifest.AddonSpec, error) {
	state, err := runProgram()
	if err != nil {
		return manifest.AddonSpec{}, err
	}
	spec := state.spec()
	addons := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{spec.Name: spec}}
	if err := addons.Validate(); err != nil {
		return manifest.AddonSpec{}, fmt.Errorf("wizard produced invalid addon: %w", err)
	}
	return spec, nil
}

// model is the Bubble Tea model wrapping wizardState.
type model struct {
	state     *wizardState
	stage     int // 0 = pick source, 1 = fill fields, 2 = done
	sourceIdx int
	fieldIdx  int
	input     textinput.Model
	err       error
}

var sourceChoices = []manifest.SourceType{
	manifest.SourceGit, manifest.SourceGitHubRelease, manifest.SourceArchive,
}

func initialModel() model {
	field := textinput.New()
	field.Focus()
	return model{state: newWizardState(), input: field}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	switch key.String() {
	case "ctrl+c", "esc":
		m.err = fmt.Errorf("cancelled")
		return m, tea.Quit
	case "up":
		if m.stage == 0 && m.sourceIdx > 0 {
			m.sourceIdx--
		}
	case "down":
		if m.stage == 0 && m.sourceIdx < len(sourceChoices)-1 {
			m.sourceIdx++
		}
	case "enter":
		return m.advance()
	}
	if m.stage == 1 {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// advance moves the wizard forward when the user presses enter.
func (m model) advance() (tea.Model, tea.Cmd) {
	switch m.stage {
	case 0:
		m.state.setSource(sourceChoices[m.sourceIdx])
		m.stage = 1
		m.input.SetValue("")
		return m, nil
	case 1:
		fields := m.state.fieldOrder()
		m.state.set(fields[m.fieldIdx], m.input.Value())
		m.fieldIdx++
		if m.fieldIdx >= len(fields) {
			m.stage = 2
			return m, tea.Quit
		}
		m.input.SetValue("")
		return m, nil
	}
	return m, tea.Quit
}

func (m model) View() string {
	switch m.stage {
	case 0:
		view := "Select addon source type:\n\n"
		for index, choice := range sourceChoices {
			cursor := "  "
			if index == m.sourceIdx {
				cursor = "> "
			}
			view += cursor + string(choice) + "\n"
		}
		return view + "\n(up/down to move, enter to select, esc to cancel)\n"
	case 1:
		field := m.state.fieldOrder()[m.fieldIdx]
		return fmt.Sprintf("Enter %s:\n\n%s\n\n(enter to confirm, esc to cancel)\n", field, m.input.View())
	default:
		return "Done.\n"
	}
}

// runProgram runs the Bubble Tea program and returns the collected state.
func runProgram() (*wizardState, error) {
	final, err := tea.NewProgram(initialModel()).Run()
	if err != nil {
		return nil, err
	}
	finalModel := final.(model)
	if finalModel.err != nil {
		return nil, finalModel.err
	}
	return finalModel.state, nil
}
