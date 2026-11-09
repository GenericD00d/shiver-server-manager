package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary act as the external program: when
// RUN_HELPER is set it behaves as asked and exits.
func TestMain(m *testing.M) {
	switch os.Getenv("RUN_HELPER") {
	case "":
		os.Exit(m.Run())
	case "echo":
		fmt.Print(strings.Join(os.Args[1:], " "))
		os.Exit(0)
	case "env":
		fmt.Print(strings.Join(os.Environ(), "\n"))
		os.Exit(0)
	case "fail":
		fmt.Fprint(os.Stderr, "bad key hunter2")
		os.Exit(3)
	case "sleep":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
}

func self(t *testing.T) string {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestExecPassesArgsAndLogs(t *testing.T) {
	var logged []string
	e := &Exec{Log: func(l string) { logged = append(logged, l) }}
	res, err := e.Run(context.Background(), Cmd{Name: self(t), Args: []string{"a b", "c"}, Env: []string{"RUN_HELPER=echo"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "a b c" {
		t.Fatalf("stdout %q", res.Stdout)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "'a b' c exit=0") {
		t.Fatalf("log %q", logged)
	}
}

func TestExecFixedEnvironment(t *testing.T) {
	t.Setenv("LD_PRELOAD", "/evil.so")
	e := &Exec{}
	res, err := e.Run(context.Background(), Cmd{Name: self(t), Env: []string{"RUN_HELPER=env"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(res.Stdout), "LD_PRELOAD") {
		t.Fatal("caller's environment leaked into the command")
	}
	if !strings.Contains(string(res.Stdout), "LC_ALL=C") {
		t.Fatalf("base environment missing: %q", res.Stdout)
	}
}

func TestExecRedactsSecrets(t *testing.T) {
	var logged []string
	e := &Exec{Log: func(l string) { logged = append(logged, l) }}
	e.AddSecret([]byte("hunter2"))
	_, err := e.Run(context.Background(), Cmd{Name: self(t), Args: []string{"--x=hunter2"}, Env: []string{"RUN_HELPER=fail"}})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode != 3 {
		t.Fatalf("got %v, want exit code 3", err)
	}
	if strings.Contains(err.Error(), "hunter2") || strings.Contains(logged[0], "hunter2") {
		t.Fatalf("secret leaked: %q / %q", err, logged[0])
	}
}

func TestExecTimeout(t *testing.T) {
	e := &Exec{}
	_, err := e.Run(context.Background(), Cmd{Name: self(t), Env: []string{"RUN_HELPER=sleep"}, Timeout: 200 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v, want timeout", err)
	}
}

func TestExecRefusesRelativePaths(t *testing.T) {
	e := &Exec{}
	if _, err := e.Run(context.Background(), Cmd{Name: "./thing"}); err == nil {
		t.Fatal("relative path accepted")
	}
}

func TestFakeRecordsAndAnswers(t *testing.T) {
	f := (&Fake{}).On("ufw status numbered", FakeResponse{Stdout: "Status: active"}).
		On("cryptsetup", FakeResponse{ExitCode: 2, Stderr: "no key"})
	res, err := f.Run(context.Background(), Cmd{Name: "ufw", Args: []string{"status", "numbered"}})
	if err != nil || string(res.Stdout) != "Status: active" {
		t.Fatalf("%v %q", err, res.Stdout)
	}
	_, err = f.Run(context.Background(), Cmd{Name: "cryptsetup", Args: []string{"open", "--key-file=-"}, Stdin: strings.NewReader("k3y")})
	if err == nil {
		t.Fatal("want error")
	}
	if got := f.Lines(); len(got) != 2 || got[1] != "cryptsetup open --key-file=-" {
		t.Fatalf("lines %q", got)
	}
	if string(f.Calls[1].Stdin) != "k3y" {
		t.Fatal("stdin not recorded")
	}
	if err := f.CheckNoSecretInArgs([]byte("k3y")); err != nil {
		t.Fatal(err)
	}
	f.Run(context.Background(), Cmd{Name: "bad", Args: []string{"k3y"}})
	if err := f.CheckNoSecretInArgs([]byte("k3y")); err == nil {
		t.Fatal("secret in args not caught")
	}
}
