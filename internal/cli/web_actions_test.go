package cli

import (
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/web"
)

// Every web action must build a command line the CLI accepts: the web page
// and the commands can't drift apart.
func TestEveryWebActionIsARealCommand(t *testing.T) {
	sample := web.Params{
		"id": "chat-a", "domain": "a.example.com", "tls": "letsencrypt", "encrypt": "true",
		"path": "/srv/x", "file": "/var/backups/x.age", "out": "/tmp/x.age", "to": "/srv/x", "device": "/dev/sdb1",
		"location": "/srv/x", "source": "/home/me/.config/sharkord", "user": "alice", "key": "DEBUG", "value": "true",
		"mode": "status", "memory": "2048", "cpu": "100", "size": "20", "confirmId": "chat-a", "confirm": "remove everything",
		"passphrase": "a passphrase", "exportPassphrase": "an export passphrase", "newPassphrase": "a new passphrase",
		"recoveryKey": "AGE-SECRET-KEY-1X", "token": "t", "webhook": "https://hooks.example.com/x", "emailTo": "a@example.com",
		"smtp": "mail.example.com:587", "includeToken": "true", "testMode": "true", "force": "true", "skipDnsCheck": "true", "makeDefault": "true", "check": "off",
	}
	app := &App{}
	root := app.newRoot()
	for name := range web.Actions {
		_, args, _, err := web.Build(name, sample)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		args = append(args, "--yes", "--passphrase-stdin", "--acting-user", "alice")
		cmd, rest, err := root.Find(args)
		if err != nil || cmd == root {
			t.Errorf("%s: %q isn't a command: %v", name, strings.Join(args, " "), err)
			continue
		}
		if err := cmd.ParseFlags(rest); err != nil {
			t.Errorf("%s: %q: %v", name, strings.Join(args, " "), err)
			continue
		}
		if cmd.Args != nil {
			if err := cmd.Args(cmd, cmd.Flags().Args()); err != nil {
				t.Errorf("%s: %q: %v", name, strings.Join(args, " "), err)
			}
		}
	}
}
