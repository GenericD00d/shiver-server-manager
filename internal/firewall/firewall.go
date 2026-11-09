// Package firewall adds and removes the manager's rules through ufw or
// firewalld. Raw nftables isn't managed: an allow rule in a separate table
// can't override another firewall's block.
package firewall

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

// Rule is one opened port. Instance "caddy" is Caddy's 80 and 443; "ssh"
// is the SSH port opened before switching ufw on.
type Rule struct {
	Port     int    `json:"port"`
	Proto    string `json:"proto"` // "tcp" or "udp"
	Instance string `json:"instance"`
}

func (r Rule) String() string { return fmt.Sprintf("%d/%s (%s)", r.Port, r.Proto, r.Instance) }

const (
	Caddy = "caddy"
	SSH   = "ssh"
)

// CaddyRules are 80 and 443 over TCP, plus 443 over UDP for HTTP/3.
var CaddyRules = []Rule{{80, "tcp", Caddy}, {443, "tcp", Caddy}, {443, "udp", Caddy}}

// InstanceRules are an instance's voice port over UDP and TCP.
func InstanceRules(id string, webrtcPort int) []Rule {
	return []Rule{{webrtcPort, "udp", id}, {webrtcPort, "tcp", id}}
}

type Firewall interface {
	Name() string
	Active(ctx context.Context) (bool, error)
	Allow(ctx context.Context, r Rule) error  // no-op if already there
	Remove(ctx context.Context, r Rule) error // no-op if already gone
	List(ctx context.Context) ([]Rule, error) // only rules the manager added
}

// None is used when no firewall is on; every call succeeds.
type None struct{}

func (None) Name() string                         { return "none" }
func (None) Active(context.Context) (bool, error) { return false, nil }
func (None) Allow(context.Context, Rule) error    { return nil }
func (None) Remove(context.Context, Rule) error   { return nil }
func (None) List(context.Context) ([]Rule, error) { return nil, nil }

// Detect picks firewalld if it's running, else ufw if active, else "".
func Detect(ctx context.Context, r run.Runner) (string, error) {
	if res, err := r.Run(ctx, run.Cmd{Name: "systemctl", Args: []string{"is-active", "firewalld"}}); err == nil && strings.TrimSpace(string(res.Stdout)) == "active" {
		return "firewalld", nil
	}
	u := UFW{R: r}
	if ok, _ := u.Active(ctx); ok {
		return "ufw", nil
	}
	return "", nil
}

// For returns the backend by name.
func For(name string, r run.Runner) Firewall {
	switch name {
	case "ufw":
		return UFW{R: r}
	case "firewalld":
		Firewalld := Firewalld{R: r}
		return Firewalld
	}
	return None{}
}

// Contains reports whether rules has r.
func Contains(rules []Rule, r Rule) bool { return slices.Contains(rules, r) }

// UFW manages rules through ufw, labelled with comments "sharkord:<id>".
type UFW struct{ R run.Runner }

func (UFW) Name() string { return "ufw" }

func (u UFW) ufw(ctx context.Context, args ...string) (string, error) {
	res, err := u.R.Run(ctx, run.Cmd{Name: "ufw", Args: args})
	return string(res.Stdout), err
}

func (u UFW) Active(ctx context.Context) (bool, error) {
	out, err := u.ufw(ctx, "status")
	if err != nil {
		if strings.Contains(err.Error(), "not installed") {
			return false, nil
		}
		return false, err
	}
	return strings.HasPrefix(strings.TrimSpace(out), "Status: active"), nil
}

func label(instance string) string { return "sharkord:" + instance }

func (u UFW) Allow(ctx context.Context, r Rule) error {
	rules, err := u.List(ctx)
	if err != nil {
		return err
	}
	if Contains(rules, r) {
		return nil
	}
	_, err = u.ufw(ctx, "allow", fmt.Sprintf("%d/%s", r.Port, r.Proto), "comment", label(r.Instance))
	return err
}

// numbered is one line of "ufw status numbered".
type numbered struct {
	n    int
	rule Rule
}

// [ 1] 40000/udp                  ALLOW IN    Anywhere                   # sharkord:chat-a
// [ 4] 40000/udp (v6)             ALLOW IN    Anywhere (v6)              # sharkord:chat-a
var ufwLine = regexp.MustCompile(`^\[\s*(\d+)\]\s+(\d+)/(tcp|udp)(?:\s+\(v6\))?\s+ALLOW IN\s+.*#\s*sharkord:([a-z][a-z0-9-]{0,19})\s*$`)

func parseNumbered(out string) []numbered {
	var rules []numbered
	for _, line := range strings.Split(out, "\n") {
		m := ufwLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		port, _ := strconv.Atoi(m[2])
		rules = append(rules, numbered{n, Rule{Port: port, Proto: m[3], Instance: m[4]}})
	}
	return rules
}

func (u UFW) List(ctx context.Context) ([]Rule, error) {
	out, err := u.ufw(ctx, "status", "numbered")
	if err != nil {
		return nil, err
	}
	var rules []Rule
	for _, nr := range parseNumbered(out) {
		if !Contains(rules, nr.rule) {
			rules = append(rules, nr.rule)
		}
	}
	return rules, nil
}

// Remove deletes the rule and its (v6) twin, highest number first so the
// numbering doesn't shift under us.
func (u UFW) Remove(ctx context.Context, r Rule) error {
	out, err := u.ufw(ctx, "status", "numbered")
	if err != nil {
		return err
	}
	var nums []int
	for _, nr := range parseNumbered(out) {
		if nr.rule == r {
			nums = append(nums, nr.n)
		}
	}
	slices.Sort(nums)
	slices.Reverse(nums)
	for _, n := range nums {
		if _, err := u.ufw(ctx, "--force", "delete", strconv.Itoa(n)); err != nil {
			return err
		}
	}
	return nil
}

// Enable switches ufw on, allowing the SSH port first so the admin isn't
// locked out.
func (u UFW) Enable(ctx context.Context, sshPorts []int) error {
	for _, p := range sshPorts {
		if err := u.Allow(ctx, Rule{Port: p, Proto: "tcp", Instance: SSH}); err != nil {
			return err
		}
	}
	_, err := u.ufw(ctx, "--force", "enable")
	return err
}

// SSHPorts reads the ports sshd listens on from "sshd -T". None found (or no
// sshd) gives 22, the safe guess.
func SSHPorts(ctx context.Context, r run.Runner) []int {
	res, err := r.Run(ctx, run.Cmd{Name: "sshd", Args: []string{"-T"}})
	var ports []int
	if err == nil {
		for _, line := range strings.Split(string(res.Stdout), "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[0] == "port" {
				if p, err := strconv.Atoi(f[1]); err == nil && !slices.Contains(ports, p) {
					ports = append(ports, p)
				}
			}
		}
	}
	if len(ports) == 0 {
		ports = []int{22}
	}
	return ports
}

// Firewalld manages one service per instance, named sharkord-<id>, in the
// default zone. Caddy uses the built-in http and https services plus
// 443/udp.
type Firewalld struct{ R run.Runner }

func (Firewalld) Name() string { return "firewalld" }

func (f Firewalld) cmd(ctx context.Context, args ...string) (string, error) {
	res, err := f.R.Run(ctx, run.Cmd{Name: "firewall-cmd", Args: args})
	return strings.TrimSpace(string(res.Stdout)), err
}

func (f Firewalld) Active(ctx context.Context) (bool, error) {
	out, err := f.cmd(ctx, "--state")
	return err == nil && out == "running", nil
}

func service(instance string) string { return "sharkord-" + instance }

func (f Firewalld) services(ctx context.Context) ([]string, error) {
	out, err := f.cmd(ctx, "--permanent", "--list-services")
	return strings.Fields(out), err
}

func (f Firewalld) ports(ctx context.Context, args ...string) ([]string, error) {
	out, err := f.cmd(ctx, append([]string{"--permanent"}, args...)...)
	return strings.Fields(out), err
}

func (f Firewalld) Allow(ctx context.Context, r Rule) error {
	rules, err := f.List(ctx)
	if err != nil {
		return err
	}
	if Contains(rules, r) {
		return nil
	}
	pp := fmt.Sprintf("%d/%s", r.Port, r.Proto)
	if r.Instance == Caddy {
		switch pp {
		case "80/tcp":
			_, err = f.cmd(ctx, "--permanent", "--add-service=http")
		case "443/tcp":
			_, err = f.cmd(ctx, "--permanent", "--add-service=https")
		default:
			_, err = f.cmd(ctx, "--permanent", "--add-port="+pp)
		}
	} else {
		svc := service(r.Instance)
		all, _ := f.cmd(ctx, "--permanent", "--get-services")
		if !slices.Contains(strings.Fields(all), svc) {
			if _, err := f.cmd(ctx, "--permanent", "--new-service="+svc); err != nil {
				return err
			}
		}
		if _, err := f.cmd(ctx, "--permanent", "--service="+svc, "--add-port="+pp); err != nil {
			return err
		}
		_, err = f.cmd(ctx, "--permanent", "--add-service="+svc)
	}
	if err != nil {
		return err
	}
	_, err = f.cmd(ctx, "--reload")
	return err
}

func (f Firewalld) Remove(ctx context.Context, r Rule) error {
	rules, err := f.List(ctx)
	if err != nil {
		return err
	}
	if !Contains(rules, r) {
		return nil
	}
	pp := fmt.Sprintf("%d/%s", r.Port, r.Proto)
	if r.Instance == Caddy {
		switch pp {
		case "80/tcp":
			_, err = f.cmd(ctx, "--permanent", "--remove-service=http")
		case "443/tcp":
			_, err = f.cmd(ctx, "--permanent", "--remove-service=https")
		default:
			_, err = f.cmd(ctx, "--permanent", "--remove-port="+pp)
		}
	} else {
		svc := service(r.Instance)
		if _, err = f.cmd(ctx, "--permanent", "--service="+svc, "--remove-port="+pp); err == nil {
			left, _ := f.ports(ctx, "--service="+svc, "--get-ports")
			if len(left) == 0 {
				_, _ = f.cmd(ctx, "--permanent", "--remove-service="+svc)
				_, err = f.cmd(ctx, "--permanent", "--delete-service="+svc)
			}
		}
	}
	if err != nil {
		return err
	}
	_, err = f.cmd(ctx, "--reload")
	return err
}

func (f Firewalld) List(ctx context.Context) ([]Rule, error) {
	svcs, err := f.services(ctx)
	if err != nil {
		return nil, err
	}
	var rules []Rule
	for _, s := range svcs {
		switch {
		case s == "http":
			rules = append(rules, Rule{80, "tcp", Caddy})
		case s == "https":
			rules = append(rules, Rule{443, "tcp", Caddy})
		case strings.HasPrefix(s, "sharkord-"):
			ports, err := f.ports(ctx, "--service="+s, "--get-ports")
			if err != nil {
				return nil, err
			}
			for _, p := range ports {
				if r, ok := parsePort(p, strings.TrimPrefix(s, "sharkord-")); ok {
					rules = append(rules, r)
				}
			}
		}
	}
	ports, err := f.ports(ctx, "--list-ports")
	if err != nil {
		return nil, err
	}
	for _, p := range ports {
		if p == "443/udp" {
			rules = append(rules, Rule{443, "udp", Caddy})
		}
	}
	return rules, nil
}

func parsePort(s, instance string) (Rule, bool) {
	p, proto, ok := strings.Cut(s, "/")
	n, err := strconv.Atoi(p)
	if !ok || err != nil || (proto != "tcp" && proto != "udp") {
		return Rule{}, false
	}
	return Rule{Port: n, Proto: proto, Instance: instance}, true
}
