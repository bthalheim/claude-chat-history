// chat-browser: a terminal UI for browsing Claude Code chat history.
//
// Usage: chat-browser [flags] [dir]
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	titleSt  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("62")).Padding(0, 1)
	helpSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	selSt    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("237"))
	dimSt    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	userSt   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	asstSt   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	toolSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("178"))
	thinkSt  = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("243"))
	mdHeadSt = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
)

type mode int

const (
	modeList mode = iota
	modeView
)

type model struct {
	dir          string
	items        []item
	shown        []int // indexes into items after filtering
	cursor, top  int
	filter       textinput.Model
	filtering    bool
	mode         mode
	vp           viewport.Model
	cur          *item
	entries      []entry
	showTools    bool
	showThinking bool
	promptLines  []int
	w, h         int
	err          error
	st           store
	sync         map[string]syncState
	msg          string
	confirm      *item // session awaiting an overwrite confirmation
}

func newModel(dir string, st store) model {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "search sessions…"
	m := model{dir: dir, st: st, filter: ti, vp: viewport.New(80, 20)}
	m.items, m.err = loadItems(dir)
	m.refreshSync()
	m.applyFilter()
	return m
}

func (m *model) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.shown = m.shown[:0]
	for i, it := range m.items {
		if q == "" || strings.Contains(it.search, q) {
			m.shown = append(m.shown, i)
		}
	}
	if m.cursor >= len(m.shown) {
		m.cursor = max(0, len(m.shown)-1)
	}
	m.clampTop()
}

func (m model) listHeight() int { return max(1, m.h-4) }

func (m *model) clampTop() {
	lh := m.listHeight()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+lh {
		m.top = m.cursor - lh + 1
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m *model) open() {
	if len(m.shown) == 0 {
		return
	}
	it := m.items[m.shown[m.cursor]]
	m.cur = &it
	m.entries = nil
	if !it.isMD {
		m.entries, _ = parseSession(it.path)
	}
	m.mode = modeView
	m.render()
	m.vp.GotoTop()
}

// render rebuilds the viewport content for the open item.
func (m *model) render() {
	w := max(20, m.w-2)
	var sb strings.Builder
	line := 0
	add := func(s string) {
		sb.WriteString(s)
		sb.WriteByte('\n')
		line += strings.Count(s, "\n") + 1
	}
	m.promptLines = nil
	if m.cur.isMD {
		b, err := os.ReadFile(m.cur.path)
		if err != nil {
			add(err.Error())
		}
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimRight(l, "\r")
			if strings.HasPrefix(l, "#") {
				l = mdHeadSt.Render(l)
			}
			add(lipgloss.NewStyle().Width(w).Render(l))
		}
		m.vp.SetContent(sb.String())
		return
	}
	body := lipgloss.NewStyle().Width(w - 2)
	for _, e := range m.entries {
		ts := ""
		if !e.ts.IsZero() {
			ts = dimSt.Render("  " + e.ts.Local().Format("15:04:05"))
		}
		switch e.kind {
		case kindUser:
			m.promptLines = append(m.promptLines, line)
			add(userSt.Render("▌ You") + ts)
			add(indent(body.Render(e.text)))
			add("")
		case kindAssistant:
			add(asstSt.Render("▌ Claude") + ts)
			add(indent(body.Render(e.text)))
			add("")
		case kindThinking:
			if m.showThinking {
				add(indent(thinkSt.Render(body.Render("thinking: " + e.text))))
				add("")
			}
		case kindToolUse:
			if m.showTools {
				add(indent(toolSt.Render(ansi.Truncate("⚙ "+e.text, w-2, "…"))))
			}
		case kindToolResult:
			if m.showTools {
				add(indent(dimSt.Render(ansi.Truncate("  ↳ "+oneLine(e.text), w-2, "…"))))
				add("")
			}
		}
	}
	if len(m.entries) == 0 {
		add(dimSt.Render("(no messages)"))
	}
	m.vp.SetContent(sb.String())
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.vp.Width, m.vp.Height = msg.Width, max(1, msg.Height-2)
		m.filter.Width = max(10, msg.Width-6)
		m.clampTop()
		if m.mode == modeView {
			y := m.vp.YOffset
			m.render()
			m.vp.SetYOffset(y)
		}
		return m, nil
	case tea.KeyMsg:
		if m.mode == modeList {
			return m.updateList(msg)
		}
		return m.updateView(msg)
	}
	return m, nil
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch msg.String() {
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.applyFilter()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.applyFilter()
		return m, cmd
	}
	if m.confirm != nil {
		m.handleConfirm(msg.String())
		return m, nil
	}
	m.msg = ""
	n := len(m.shown)
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "s":
		if n > 0 {
			m.restore(m.items[m.shown[m.cursor]], false)
		}
	case "S":
		m.restoreAllSafe()
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(max(0, n-1), m.cursor+1)
	case "pgup":
		m.cursor = max(0, m.cursor-m.listHeight())
	case "pgdown":
		m.cursor = min(max(0, n-1), m.cursor+m.listHeight())
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = max(0, n-1)
	case "/":
		m.filtering = true
		return m, m.filter.Focus()
	case "esc":
		m.filter.SetValue("")
		m.applyFilter()
	case "r":
		m.items, m.err = loadItems(m.dir)
		m.refreshSync()
		m.applyFilter()
	case "enter", "right", "l":
		m.open()
	}
	m.clampTop()
	return m, nil
}

func (m model) updateView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		m.handleConfirm(msg.String())
		return m, nil
	}
	m.msg = ""
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "s":
		m.restore(*m.cur, false)
		return m, nil
	case "q", "esc", "backspace", "left", "h":
		m.mode = modeList
		return m, nil
	case "t":
		m.showTools = !m.showTools
		m.render()
		return m, nil
	case "T":
		m.showThinking = !m.showThinking
		m.render()
		return m, nil
	case "n":
		for _, l := range m.promptLines {
			if l > m.vp.YOffset {
				m.vp.SetYOffset(l)
				break
			}
		}
		return m, nil
	case "p":
		for i := len(m.promptLines) - 1; i >= 0; i-- {
			if m.promptLines[i] < m.vp.YOffset {
				m.vp.SetYOffset(m.promptLines[i])
				break
			}
		}
		return m, nil
	case "g", "home":
		m.vp.GotoTop()
		return m, nil
	case "G", "end":
		m.vp.GotoBottom()
		return m, nil
	case "j":
		m.vp.LineDown(1)
		return m, nil
	case "k":
		m.vp.LineUp(1)
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.w == 0 {
		return "loading…"
	}
	if m.mode == modeView {
		return m.viewReader()
	}
	var sb strings.Builder
	head := fmt.Sprintf("Chat History · %s · %d/%d", m.dir, len(m.shown), len(m.items))
	sb.WriteString(titleSt.Render(ansi.Truncate(head, m.w-2, "…")) + "\n")
	switch {
	case m.filtering || m.filter.Value() != "":
		sb.WriteString(m.filter.View() + "\n")
	case m.err != nil:
		sb.WriteString("error: " + m.err.Error() + "\n")
	default:
		sb.WriteString(helpSt.Render(ansi.Truncate("press / to search · restore target: "+m.st.projectDir, m.w, "…")) + "\n")
	}
	lh := m.listHeight()
	for row := 0; row < lh; row++ {
		i := m.top + row
		if i >= len(m.shown) {
			sb.WriteString("\n")
			continue
		}
		it := m.items[m.shown[i]]
		kind, unit, sync := "chat", "msgs", m.sync[it.id].label()
		if it.isMD {
			kind, unit, sync = "log ", "entries", ""
		}
		s := fmt.Sprintf(" %s  %s  %-8s %3d %-7s %s", it.ts.Local().Format("2006-01-02 15:04"), kind, sync, it.count, unit, it.title)
		s = ansi.Truncate(s, m.w, "…")
		if i == m.cursor {
			s = selSt.Width(m.w).Render(s)
		}
		sb.WriteString(s + "\n")
	}
	sb.WriteString(m.footer("↑/↓ move · enter open · / search · s restore · S restore all · r reload · q quit"))
	return sb.String()
}

func (m model) viewReader() string {
	head := fmt.Sprintf("%s · %s", m.cur.ts.Local().Format("2006-01-02 15:04"), m.cur.title)
	foot := fmt.Sprintf("%3d%% · ↑/↓ scroll · n/p prompt · t tools[%s] · T thinking[%s] · s restore · esc back",
		int(m.vp.ScrollPercent()*100), onOff(m.showTools), onOff(m.showThinking))
	return titleSt.Render(ansi.Truncate(head, m.w-2, "…")) + "\n" + m.vp.View() + "\n" + m.footer(foot)
}

// footer shows the pending status/prompt message, or the key help.
func (m model) footer(help string) string {
	if m.msg != "" {
		return selSt.Render(ansi.Truncate(m.msg, m.w, "…"))
	}
	return helpSt.Render(ansi.Truncate(help, m.w, "…"))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
