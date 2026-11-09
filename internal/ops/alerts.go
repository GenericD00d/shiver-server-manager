package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
)

// Alerts is where health problems are sent. It lives in a root-only file
// rather than the vault, because the health timer runs without unlocking.
type Alerts struct {
	Webhook string `json:"webhook,omitempty"`
	Email   struct {
		To       string `json:"to,omitempty"`
		From     string `json:"from,omitempty"`
		Server   string `json:"server,omitempty"` // host:port
		User     string `json:"user,omitempty"`
		Password string `json:"password,omitempty"`
	} `json:"email"`
}

func (s *Sys) LoadAlerts() Alerts {
	var a Alerts
	if b, err := os.ReadFile(s.Paths.AlertsFile); err == nil {
		_ = json.Unmarshal(b, &a)
	}
	return a
}

func (s *Sys) SaveAlerts(a Alerts) error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.Paths.AlertsFile, b, 0o600, nil)
}

// AlertText describes what appeared and cleared.
func AlertText(host string, appeared, cleared []Notice) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s on %s:\n", paths.AppName, host)
	for _, n := range appeared {
		fmt.Fprintf(&b, "- %s: %s\n", strings.ToUpper(n.Level), n.Message)
	}
	for _, n := range cleared {
		fmt.Fprintf(&b, "- fixed: %s\n", n.Message)
	}
	return b.String()
}

// Send delivers an alert by webhook and/or email. The webhook body carries
// both "text" (Slack, Mattermost) and "content" (Discord).
func (a Alerts) Send(ctx context.Context, subject, text string) error {
	var errs []error
	if a.Webhook != "" {
		body, _ := json.Marshal(map[string]string{"text": text, "content": text})
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Webhook, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			var resp *http.Response
			if resp, err = http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode >= 300 {
					err = fmt.Errorf("webhook answered %s", resp.Status)
				}
			}
		}
		errs = append(errs, err)
	}
	if e := a.Email; e.To != "" && e.Server != "" {
		host, _, err := net.SplitHostPort(e.Server)
		if err != nil {
			return errors.Join(append(errs, fmt.Errorf("email server %q: %w", e.Server, err))...)
		}
		from := e.From
		if from == "" {
			from = e.User
		}
		msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s", from, e.To, subject, strings.ReplaceAll(text, "\n", "\r\n"))
		var auth smtp.Auth
		if e.User != "" {
			auth = smtp.PlainAuth("", e.User, e.Password, host)
		}
		errs = append(errs, smtp.SendMail(e.Server, auth, from, strings.Split(e.To, ","), []byte(msg)))
	}
	return errors.Join(errs...)
}

// Configured reports whether any alert channel is set.
func (a Alerts) Configured() bool {
	return a.Webhook != "" || (a.Email.To != "" && a.Email.Server != "")
}
