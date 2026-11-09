//go:build linux

package sharkord

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// FirstStart runs Sharkord directly as the instance's user, reads its output
// until the token appears and /healthz answers, then stops it. This is the
// one external program not run through internal/run, because it streams
// output and runs as another user.
func FirstStart(ctx context.Context, o FirstStartOpts) (string, error) {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(o.Binary)
	cmd.Dir = o.DataPath
	cmd.Env = append([]string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "HOME=" + o.DataPath}, o.Env...)
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: o.UID, Gid: o.GID, Groups: []uint32{}},
		Setpgid:    true,
		Pdeathsig:  syscall.SIGKILL,
	}
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return "", fmt.Errorf("start Sharkord: %w", err)
	}
	pw.Close()

	var mu sync.Mutex
	var out strings.Builder
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := pr.Read(buf)
			mu.Lock()
			if out.Len() < 1<<20 {
				out.Write(buf[:n])
			}
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	stop := func() {
		pgid := -cmd.Process.Pid
		_ = syscall.Kill(pgid, syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(pgid, syscall.SIGKILL)
			<-exited
		}
		pr.Close()
		<-readDone
	}
	output := func() string {
		mu.Lock()
		defer mu.Unlock()
		return out.String()
	}

	deadline := time.After(timeout)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	var healthySince time.Time
	for {
		select {
		case <-ctx.Done():
			stop()
			return "", ctx.Err()
		case err := <-exited:
			pr.Close()
			<-readDone
			tok, _ := CaptureToken(output())
			return "", fmt.Errorf("Sharkord stopped during its first start (%v):\n%s", err, redact(lastLines(output(), 20), tok))
		case <-deadline:
			stop()
			tok, _ := CaptureToken(output())
			return "", fmt.Errorf("Sharkord's first start didn't finish within %s:\n%s", timeout, redact(lastLines(output(), 20), tok))
		case <-tick.C:
			if healthySince.IsZero() && Healthz(ctx, o.HTTPPort) == nil {
				healthySince = time.Now()
			}
			tok, found := CaptureToken(output())
			if !healthySince.IsZero() && found {
				stop()
				return tok, nil
			}
			// Healthy for a while with no token: the database existed already.
			if !healthySince.IsZero() && time.Since(healthySince) > 15*time.Second {
				stop()
				return "", ErrNoToken
			}
		}
	}
}

func redact(s, tok string) string {
	if tok == "" {
		return s
	}
	return strings.ReplaceAll(s, tok, "[redacted]")
}
