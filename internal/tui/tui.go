// Package tui is the manager's terminal menu. It's a front end on the
// commands: every action collects its inputs with a form, shows what will
// change, and runs the same command an admin could type, in-process, on the
// plain terminal.
package tui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/GenericD00d/shiver-server-manager/internal/auth"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
)

// Server is one row of the main screen.
type Server struct {
	ID, Status, Running, Domain, Cert, Location string
}

// Snapshot is what the main screen shows.
type Snapshot struct {
	User      string
	Servers   []Server
	Notices   []string
	Backups   map[string][]string // server -> backup files, newest last
	Settings  []string            // setting names the menu offers
	Locations []string
	// DefaultLocation is where new and imported servers go.
	DefaultLocation string
}

// Host is the manager underneath: the CLI app.
type Host interface {
	Login() error
	Logout()
	Snapshot() (Snapshot, error)
	// Run runs a command in-process on the terminal and returns its exit
	// code. reauth asks for the password again first.
	Run(args []string, reauth bool) int
}

var (
	grey   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	bold   = lipgloss.NewStyle().Bold(true)
	redSty = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// Run shows the login, then the menu, until the admin quits. After the idle
// timeout it goes back to the login.
func Run(h Host) error {
	for {
		if err := h.Login(); err != nil {
			return err
		}
		for {
			snap, err := h.Snapshot()
			if err != nil {
				return err
			}
			res, err := mainScreen(snap)
			if err != nil {
				return err
			}
			if res.idle {
				h.Logout()
				fmt.Println("\nLocked after 10 idle minutes. Log in again to carry on.")
				break
			}
			if res.quit {
				h.Logout()
				return nil
			}
			if res.help {
				if err := showHelp(); err != nil {
					return err
				}
				continue
			}
			perform(h, snap, res.action)
		}
	}
}

// perform collects an action's inputs, confirms, runs it, and waits.
func perform(h Host, snap Snapshot, a Action) {
	if a.Run != nil {
		a.Run(h, snap)
		return
	}
	args, ok, err := a.Form(snap)
	if err != nil || !ok {
		if err != nil {
			fmt.Println("Error:", err)
			pause()
		}
		return
	}
	if a.Confirm != "" || a.Destructive {
		text := a.Confirm
		if text == "" {
			text = a.Title + "?"
		}
		if !confirm(fmt.Sprintf(text, argsForText(args)...), a.Destructive) {
			return
		}
		if !hasFlag(args, "--yes") && a.PassYes {
			args = append(args, "--yes")
		}
	}
	fmt.Printf("\n%s\n\n", grey.Render("$ "+paths.Cmd+" "+strings.Join(args, " ")))
	code := h.Run(args, a.Reauth)
	if code == 0 {
		fmt.Println("\nDone.")
	} else {
		fmt.Printf("\nThat didn't finish (exit code %d).\n", code)
	}
	pause()
}

func argsForText(args []string) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a
	}
	return out
}

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

func pause() {
	fmt.Print(grey.Render("Press Enter to go back to the menu."))
	bufio.NewReader(os.Stdin).ReadString('\n')
}

// mainScreen is the menu's home: notices, servers, sections.

type result struct {
	action           Action
	quit, idle, help bool
}

type tickMsg time.Time

type model struct {
	snap     Snapshot
	section  int // -1 = section list
	cursor   int
	lastSeen time.Time
	res      result
	width    int
	height   int
}

func mainScreen(snap Snapshot) (result, error) {
	m := model{snap: snap, section: -1, lastSeen: time.Now(), width: 80, height: 24}
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return result{}, err
	}
	return final.(model).res, nil
}

func tick() tea.Cmd {
	return tea.Tick(15*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd { return tick() }

func (m model) items() int {
	if m.section < 0 {
		return len(Sections)
	}
	return len(Sections[m.section].Actions)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if time.Since(m.lastSeen) >= auth.IdleTimeout {
			m.res.idle = true
			return m, tea.Quit
		}
		return m, tick()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		m.lastSeen = time.Now()
		switch k := msg.String(); k {
		case "ctrl+c", "q":
			if m.section >= 0 && k == "q" {
				m.section, m.cursor = -1, 0
				return m, nil
			}
			m.res.quit = true
			return m, tea.Quit
		case "esc", "left", "backspace":
			if m.section >= 0 {
				m.cursor, m.section = m.section, -1
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < m.items()-1 {
				m.cursor++
			}
		case "?", "h":
			m.res.help = true
			return m, tea.Quit
		case "enter", "right", "space":
			if m.section < 0 {
				m.section, m.cursor = m.cursor, 0
				return m, nil
			}
			m.res.action = Sections[m.section].Actions[m.cursor]
			return m, tea.Quit
		default:
			if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
				n := int(k[0] - '1')
				if m.section < 0 && n < len(Sections) {
					m.section, m.cursor = n, 0
				} else if m.section >= 0 && n < m.items() {
					m.cursor = n
				}
			}
		}
	}
	return m, nil
}

// clip cuts a line to w columns, keeping colour codes intact.
func clip(s string, w int) string {
	if w <= 0 {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

func (m model) View() tea.View {
	var b strings.Builder
	w := m.width
	b.WriteString(bold.Render(paths.AppName) + grey.Render("  logged in as "+m.snap.User) + "\n")
	for i, n := range m.snap.Notices {
		if i == 4 {
			b.WriteString(grey.Render(fmt.Sprintf("  …and %d more (System > Health check)", len(m.snap.Notices)-4)) + "\n")
			break
		}
		b.WriteString(clip("! "+n, w) + "\n")
	}
	b.WriteString("\n")
	if len(m.snap.Servers) == 0 {
		b.WriteString(grey.Render("No servers yet: Servers > Create a server.") + "\n")
	} else {
		b.WriteString(grey.Render(fmt.Sprintf("%-14s %-12s %-28s %-10s %s", "SERVER", "RUNNING", "DOMAIN", "CERT", "LOCATION")) + "\n")
		for _, s := range m.snap.Servers {
			b.WriteString(clip(fmt.Sprintf("%-14s %-12s %-28s %-10s %s", s.ID, s.Running, s.Domain, s.Cert, s.Location), w) + "\n")
		}
	}
	b.WriteString("\n")
	if m.section < 0 {
		for i, sec := range Sections {
			line := fmt.Sprintf("%d) %-13s %s", i+1, sec.Title, grey.Render(sec.Summary))
			if i == m.cursor {
				line = bold.Render("> "+fmt.Sprintf("%d) %-13s", i+1, sec.Title)) + " " + grey.Render(sec.Summary)
			} else {
				line = "  " + line
			}
			b.WriteString(clip(line, w) + "\n")
		}
		b.WriteString("\n" + grey.Render("↑/↓ move · enter open · 1-7 jump · ? help · q quit") + "\n")
	} else {
		sec := Sections[m.section]
		b.WriteString(bold.Render(sec.Title) + "\n")
		// Scroll the list so the cursor stays on screen.
		used := strings.Count(b.String(), "\n")
		rows := max(m.height-used-3, 3)
		start := 0
		if m.cursor >= rows {
			start = m.cursor - rows + 1
		}
		end := min(start+rows, len(sec.Actions))
		for i := start; i < end; i++ {
			a := sec.Actions[i]
			prefix := "  "
			title := a.Title
			if i == m.cursor {
				prefix = "> "
				title = bold.Render(title)
			}
			b.WriteString(clip(prefix+title+"  "+grey.Render(a.Desc), w) + "\n")
		}
		b.WriteString("\n" + grey.Render("↑/↓ move · enter choose · esc back · ? help · ctrl+c quit") + "\n")
	}
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}
