package systemd

import (
	"context"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/testutil"
)

const marker = "Managed by shiver-server-manager"

func TestDropinsGolden(t *testing.T) {
	testutil.Golden(t, "data.conf", DataDropin(marker, "chat-a", "/srv/sharkord-disk2/sharkord", "/srv/sharkord-disk2/sharkord/chat-a"))
	testutil.Golden(t, "data-home.conf", DataDropin(marker, "chat-h", "/home/sharkord", "/home/sharkord/chat-h"))
	testutil.Golden(t, "encrypted.conf", EncryptedDropin(marker, "chat-a", "/srv/sharkord-disk2/sharkord/chat-a"))
	testutil.Golden(t, "limits.conf", LimitsDropin(marker, "chat-a", 2048, 200))
	testutil.Golden(t, "limits-cpu.conf", LimitsDropin(marker, "chat-a", 0, 50))
}

func TestStatus(t *testing.T) {
	f := (&run.Fake{}).On("systemctl show sharkord@chat-a.service -p ActiveState -p SubState -p UnitFileState -p ConditionResult",
		run.FakeResponse{Stdout: "ActiveState=inactive\nSubState=dead\nUnitFileState=enabled\nConditionResult=no\n"})
	st, err := Systemd{R: f}.Status(context.Background(), Unit("chat-a"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Active != "inactive" || st.Enabled != "enabled" || !st.ConditionFailed {
		t.Fatalf("%+v", st)
	}
}
