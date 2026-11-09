// Package web is the manager's web page: a small HTTP server on the loopback
// address, reached through an SSH tunnel or through Caddy on an admin
// domain. Every action runs the same command the terminal runs.
package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/auth"
)

// Session is a logged-in admin.
type Session struct {
	User     string
	Created  time.Time
	LastSeen time.Time
}

// Sessions are kept in memory: restarting the service logs everyone out.
type Sessions struct {
	mu   sync.Mutex
	m    map[string]*Session
	Now  func() time.Time
	Idle time.Duration
}

func NewSessions() *Sessions {
	return &Sessions{m: map[string]*Session{}, Now: time.Now, Idle: auth.IdleTimeout}
}

func (s *Sessions) New(user string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	s.m[tok] = &Session{User: user, Created: now, LastSeen: now}
	return tok, nil
}

// Get returns the session and marks it used, or nil once it's idle for too
// long (or a day old: a fresh login every day).
func (s *Sessions) Get(tok string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[tok]
	if !ok {
		return nil
	}
	now := s.Now()
	if now.Sub(sess.LastSeen) >= s.Idle || now.Sub(sess.Created) >= 24*time.Hour {
		delete(s.m, tok)
		return nil
	}
	sess.LastSeen = now
	return sess
}

// Peek returns the session without marking it used (for polling).
func (s *Sessions) Peek(tok string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[tok]
	if !ok || s.Now().Sub(sess.LastSeen) >= s.Idle {
		return nil
	}
	return sess
}

func (s *Sessions) Delete(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, tok)
}

// ErrCodeNeeded means PAM asked a second question (a two-factor code).
var ErrCodeNeeded = errors.New("a second code is needed")

// webPrompter answers PAM from a login form: the first hidden prompt gets
// the password, the next one the code. Messages are kept to show.
type webPrompter struct {
	password, code string
	hidden         int
	asked          string
	messages       []string
}

func (p *webPrompter) Hidden(msg string) (string, error) {
	p.hidden++
	switch {
	case p.hidden == 1:
		return p.password, nil
	case p.code != "":
		return p.code, nil
	}
	p.asked = strings.TrimSpace(msg)
	return "", ErrCodeNeeded
}

func (p *webPrompter) Visible(msg string) (string, error) {
	if p.code != "" {
		return p.code, nil
	}
	p.asked = strings.TrimSpace(msg)
	return "", ErrCodeNeeded
}

func (p *webPrompter) Info(msg string)  { p.messages = append(p.messages, msg) }
func (p *webPrompter) Error(msg string) { p.messages = append(p.messages, msg) }
