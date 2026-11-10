package ops

import (
	"context"
	"testing"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func TestBackupPolicy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 13, 0, 0, 0, time.Local)
	h.s.St.Instances["chat-a"].CreatedAt = now.AddDate(0, 0, -30)
	h.s.Now = func() time.Time { return now }

	if due := h.s.DueBackups(); len(due) != 1 {
		t.Fatalf("a server with no backups owes one: %v", due)
	}
	for i := 0; i < 4; i++ {
		now = now.Add(time.Minute)
		if _, err := h.s.Backup(ctx, "chat-a"); err != nil {
			t.Fatal(err)
		}
	}
	if due := h.s.DueBackups(); len(due) != 0 {
		t.Fatalf("just backed up: %v", due)
	}
	v, _ := h.s.BackupPolicy("chat-a")
	if !v.Default || v.KeepCount != 14 || v.Backups != 4 || v.Next == nil || v.Next.Hour() != 0 {
		t.Fatalf("%+v", v)
	}

	p := state.BackupPolicy{Times: []string{"03:30"}, Days: []string{"mon"}, KeepCount: 2}
	if plan, _ := h.s.PlanBackupPolicy("chat-a", p); len(plan) != 2 {
		t.Fatalf("plan %v", plan)
	}
	removed, err := h.s.SetBackupPolicy("chat-a", p)
	if err != nil || len(removed) != 2 {
		t.Fatalf("%v %v", removed, err)
	}
	now = now.Add(time.Minute)
	h.s.Backup(ctx, "chat-a")
	if list, _ := h.s.Backups("chat-a"); len(list) != 2 {
		t.Fatalf("keep 2: %d", len(list))
	}

	list, _ := h.s.Backups("chat-a")
	if err := h.s.DeleteBackup("chat-a", list[0].File); err != nil {
		t.Fatal(err)
	}
	if list, _ := h.s.Backups("chat-a"); len(list) != 1 {
		t.Fatalf("after delete: %d", len(list))
	}

	h.s.SetBackupPolicy("chat-a", state.BackupPolicy{Off: true})
	now = now.AddDate(0, 0, 10)
	if due := h.s.DueBackups(); len(due) != 0 {
		t.Fatalf("backups are off: %v", due)
	}
}
