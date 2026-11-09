package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/GenericD00d/shiver-server-manager/internal/auth"
)

func snap() Snapshot {
	return Snapshot{User: "alice",
		Notices: []string{"chat-a isn't running (failed); check its logs, or run 'repair' and a very long tail that goes past the edge of an 80 column terminal"},
		Servers: []Server{{ID: "a-twenty-char-server", Running: "not answering", Domain: "a-rather-long-subdomain.example.com", Cert: "89 days", Location: "/srv/sharkord-disk2/sharkord"}}}
}

func key(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	return tea.KeyPressMsg{Code: rune(k[0]), Text: k}
}

func press(m model, keys ...string) model {
	for _, k := range keys {
		next, _ := m.Update(key(k))
		m = next.(model)
	}
	return m
}

func TestEveryScreenFits80Columns(t *testing.T) {
	m := model{snap: snap(), section: -1, width: 80, height: 24, lastSeen: time.Now()}
	check := func(m model) {
		for _, line := range strings.Split(m.View().Content, "\n") {
			if w := lipgloss.Width(line); w > 80 {
				t.Errorf("line is %d wide: %q", w, line)
			}
		}
		if n := strings.Count(m.View().Content, "\n"); n > 24 {
			t.Errorf("screen has %d lines", n)
		}
	}
	check(m)
	for i := range Sections {
		sm := press(m, string(rune('1'+i)))
		check(sm)
		for range Sections[i].Actions { // scrolled to the end, too
			sm = press(sm, "down")
		}
		check(sm)
	}
}

func TestNavigation(t *testing.T) {
	m := model{snap: snap(), section: -1, width: 80, lastSeen: time.Now()}
	m = press(m, "2") // Owner tokens
	if m.section != 1 {
		t.Fatalf("section %d", m.section)
	}
	m = press(m, "esc", "enter") // back to the list, then the same section again
	if m.section != 1 {
		t.Fatalf("section %d after esc/enter", m.section)
	}
	m = press(m, "enter")
	if m.res.action.Title != "Show a secret token" || !m.res.action.Reauth {
		t.Fatalf("chose %+v", m.res.action.Title)
	}
}

func TestIdleLock(t *testing.T) {
	m := model{snap: snap(), section: -1, width: 80, lastSeen: time.Now().Add(-auth.IdleTimeout)}
	next, _ := m.Update(tickMsg(time.Now()))
	if !next.(model).res.idle {
		t.Fatal("not locked after the idle timeout")
	}
}

func TestEveryActionDoesSomething(t *testing.T) {
	for _, s := range Sections {
		for _, a := range s.Actions {
			if a.Form == nil && a.Run == nil {
				t.Errorf("%s > %s does nothing", s.Title, a.Title)
			}
			if a.Destructive && a.Confirm == "" {
				t.Errorf("%s > %s is destructive without a confirmation", s.Title, a.Title)
			}
		}
	}
}
