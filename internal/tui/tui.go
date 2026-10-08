// Package tui is the terminal status screen shown while the server runs.
// It intentionally shows only what you need to reach the dashboard: URLs,
// a login link, live gauges and the latest activity.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/events"
)

// Info is the static information displayed.
type Info struct {
	Version      string
	Instance     string
	DashboardURL string
	LoginURL     string
	OpenAIURL    string
	MCPURL       string
	DBPath       string
	LogPath      string
	Password     string // only set on first run, when it was generated
}

type model struct {
	svc   *core.Service
	info  Info
	ctx   context.Context
	sub   <-chan events.Event
	unsub func()

	queued, inflight, online int64
	served, failed           int64
	recent                   []events.Event
	width                    int
	err                      error
	quitting                 bool
	notice                   string
}

type eventMsg events.Event
type tickMsg time.Time
type statsMsg struct{ queued, inflight, online, served, failed int64 }
type loginLinkMsg string
type noticeMsg string

const maxRecent = 8

// Run shows the TUI until the user quits or ctx is cancelled.
func Run(ctx context.Context, svc *core.Service, info Info) error {
	sub, unsub := svc.Bus.Subscribe(256)
	m := model{svc: svc, info: info, ctx: ctx, sub: sub, unsub: unsub, width: 100}
	// Show what already happened (including the previous run) instead of an
	// empty panel until the next event arrives.
	if past, err := svc.RecentEvents(ctx, maxRecent); err == nil {
		for _, e := range past {
			m.recent = append(m.recent, events.Event{Time: time.UnixMilli(e.Ts), Kind: e.Kind, Level: e.Level, Message: e.Message})
		}
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	unsub()
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.waitEvent(), m.fetchStats(), tick())
}

func (m model) waitEvent() tea.Cmd {
	return func() tea.Msg {
		e, ok := <-m.sub
		if !ok {
			return nil
		}
		return eventMsg(e)
	}
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) fetchStats() tea.Cmd {
	svc := m.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		q, i, o, err := svc.QuickCounts(ctx)
		if err != nil {
			return nil
		}
		st, _ := svc.Q.RequestStatsSince(ctx, time.Now().Add(-24*time.Hour).UnixMilli())
		return statsMsg{q, i, o, st.Completed, st.Failed}
	}
}

func (m model) newLoginLink() tea.Cmd {
	svc := m.svc
	return func() tea.Msg {
		tok, err := svc.CreateLoginToken(context.Background(), 15*time.Minute)
		if err != nil {
			return noticeMsg("could not create login link: " + err.Error())
		}
		return loginLinkMsg(svc.LoginLinkURL(tok))
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "o":
			url := m.info.LoginURL
			if url == "" {
				url = m.info.DashboardURL
			}
			if err := OpenBrowser(url); err != nil {
				m.notice = "could not open browser: " + err.Error()
			} else {
				m.notice = "opened dashboard in your browser"
			}
		case "l":
			return m, m.newLoginLink()
		}
	case eventMsg:
		e := events.Event(msg)
		if !e.Ephemeral {
			m.recent = append([]events.Event{e}, m.recent...)
			if len(m.recent) > maxRecent {
				m.recent = m.recent[:maxRecent]
			}
		}
		return m, tea.Batch(m.waitEvent(), m.fetchStats())
	case tickMsg:
		return m, tea.Batch(tick(), m.fetchStats())
	case statsMsg:
		m.queued, m.inflight, m.online, m.served, m.failed = msg.queued, msg.inflight, msg.online, msg.served, msg.failed
	case loginLinkMsg:
		m.info.LoginURL = string(msg)
		m.notice = "new one-time login link generated (valid 15 minutes)"
	case noticeMsg:
		m.notice = string(msg)
	}
	return m, nil
}

var (
	accent  = lipgloss.Color("#f5b942")
	muted   = lipgloss.Color("#8b98a9")
	green   = lipgloss.Color("#4ade80")
	red     = lipgloss.Color("#f0616d")
	blue    = lipgloss.Color("#7aa2f7")
	textCol = lipgloss.Color("#e6edf3")

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	dimStyle   = lipgloss.NewStyle().Foreground(muted)
	labelStyle = lipgloss.NewStyle().Foreground(muted).Width(12)
	valueStyle = lipgloss.NewStyle().Foreground(textCol)
	urlStyle   = lipgloss.NewStyle().Foreground(blue).Underline(true)
	okStyle    = lipgloss.NewStyle().Foreground(green).Bold(true)
	warnStyle  = lipgloss.NewStyle().Foreground(accent)
	errStyle   = lipgloss.NewStyle().Foreground(red)
	boxStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#2a3342")).Padding(0, 1)
	keyStyle   = lipgloss.NewStyle().Foreground(textCol).Background(lipgloss.Color("#2a3342")).Padding(0, 1)
)

func (m model) View() string {
	if m.quitting {
		return "Shutting down Switchboard…\n"
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("◉ "+m.info.Instance) + dimStyle.Render("  switchboard "+m.info.Version+"  ·  ") + okStyle.Render("running") + dimStyle.Render("  ·  up "+m.svc.Uptime().String()) + "\n\n")

	row := func(label, value string) {
		b.WriteString(labelStyle.Render(label) + value + "\n")
	}
	row("Dashboard", urlStyle.Render(m.info.DashboardURL))
	if m.info.LoginURL != "" {
		row("Login link", urlStyle.Render(m.info.LoginURL)+dimStyle.Render("  (one-time)"))
	}
	row("OpenAI API", valueStyle.Render(m.info.OpenAIURL))
	row("MCP", valueStyle.Render(m.info.MCPURL))
	row("Database", dimStyle.Render(m.info.DBPath))
	if m.info.LogPath != "" {
		row("Log", dimStyle.Render(m.info.LogPath))
	}
	if m.info.Password != "" {
		b.WriteString("\n" + warnStyle.Render("First run: dashboard password is ") + keyStyle.Render(m.info.Password) + warnStyle.Render("  — change it in Settings or with `switchboard auth set-password`.") + "\n")
	}

	b.WriteString("\n")
	gauge := func(label string, v int64, style lipgloss.Style) string {
		return boxStyle.Render(style.Render(fmt.Sprintf("%d", v)) + " " + dimStyle.Render(label))
	}
	onlineStyle := okStyle
	if m.online == 0 {
		onlineStyle = errStyle
	}
	queuedStyle := valueStyle
	if m.queued > 0 {
		queuedStyle = warnStyle
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
		gauge("queued", m.queued, queuedStyle), " ",
		gauge("in flight", m.inflight, lipgloss.NewStyle().Foreground(blue)), " ",
		gauge("channels online", m.online, onlineStyle), " ",
		gauge("served 24h", m.served, okStyle), " ",
		gauge("failed 24h", m.failed, errStyle),
	) + "\n\n")

	if m.online == 0 {
		b.WriteString(warnStyle.Render("No agent is connected. ") + dimStyle.Render("Connect one: `switchboard mcp add claude-code` or see Agents in the dashboard.") + "\n\n")
	}

	b.WriteString(dimStyle.Render("Recent activity") + "\n")
	if len(m.recent) == 0 {
		b.WriteString(dimStyle.Render("  (waiting for events)") + "\n")
	}
	for _, e := range m.recent {
		style := valueStyle
		switch e.Level {
		case events.LevelWarn:
			style = warnStyle
		case events.LevelError:
			style = errStyle
		}
		line := fmt.Sprintf("  %s  %-22s %s", dimStyle.Render(e.Time.Format("15:04:05")), dimStyle.Render(e.Kind), style.Render(truncate(e.Message, m.width-40)))
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	if m.notice != "" {
		b.WriteString(dimStyle.Render("› "+m.notice) + "\n")
	}
	b.WriteString(keyStyle.Render("o") + dimStyle.Render(" open dashboard  ") + keyStyle.Render("l") + dimStyle.Render(" new login link  ") + keyStyle.Render("q") + dimStyle.Render(" quit"))
	b.WriteString("\n")
	return b.String()
}

func truncate(s string, n int) string {
	if n < 20 {
		n = 20
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// OpenBrowser opens url with the platform's default browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
