package state

import "testing"

func TestReconcilePhase(t *testing.T) {
	cases := []struct {
		k3s, calico, liqo, peering, complete bool
		want                                 Phase
	}{
		{false, false, false, false, false, PhaseCurrent},
		{true, false, false, false, false, PhaseK3s},
		{true, true, false, false, false, PhaseCalico},
		{true, true, true, false, false, PhaseLiqo},
		{true, true, true, true, false, PhasePeering},
		{true, true, true, true, true, PhaseComplete},
	}
	for _, tc := range cases {
		got := ReconcilePhase(tc.k3s, tc.calico, tc.liqo, tc.peering, tc.complete)
		if got != tc.want {
			t.Fatalf("ReconcilePhase(...) = %s want %s", got, tc.want)
		}
	}
}

func TestHasSuccessfulCheckpoint(t *testing.T) {
	m := NewManager(t.TempDir(), "test-agent")
	m.AddCheckpoint(PhaseInboundPeering, CheckpointStatusSuccess, "")
	m.AddCheckpoint(PhaseOperatorInstall, CheckpointStatusFailed, "")

	if !m.HasSuccessfulCheckpoint(PhaseInboundPeering) {
		t.Fatal("expected inbound_peering to be successful")
	}
	if m.HasSuccessfulCheckpoint(PhaseOperatorInstall) {
		t.Fatal("failed operator_install must not count as successful")
	}
	if m.HasSuccessfulCheckpoint(PhaseCalico) {
		t.Fatal("missing checkpoint must not count as successful")
	}
}

func TestClearCheckpoints(t *testing.T) {
	m := NewManager(t.TempDir(), "test-agent")
	m.AddCheckpoint(PhaseInboundPeering, CheckpointStatusSuccess, "")
	m.AddCheckpoint(PhaseOperatorInstall, CheckpointStatusSuccess, "")
	m.AddCheckpoint(PhaseOperatorInstall, CheckpointStatusFailed, "")
	m.ClearCheckpoints(PhaseOperatorInstall)

	if m.HasSuccessfulCheckpoint(PhaseOperatorInstall) {
		t.Fatal("expected operator_install checkpoints cleared")
	}
	if !m.HasSuccessfulCheckpoint(PhaseInboundPeering) {
		t.Fatal("expected inbound_peering checkpoint preserved")
	}
}

func TestRollbackToLastCheckpoint(t *testing.T) {
	m := NewManager(t.TempDir(), "test-agent")
	m.AddCheckpoint(PhaseK3s, CheckpointStatusSuccess, "")
	m.AddCheckpoint(PhaseCalico, CheckpointStatusSuccess, "")
	m.AddCheckpoint(PhaseLiqo, CheckpointStatusFailed, "")
	m.SetPhase(PhaseFailed)

	cp, err := m.RollbackToLastCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if cp.Phase != PhaseCalico {
		t.Fatalf("got phase %s want calico", cp.Phase)
	}
	if m.GetState().Phase != PhaseCalico {
		t.Fatalf("state phase %s", m.GetState().Phase)
	}
	if len(m.GetState().Checkpoints) != 2 {
		t.Fatalf("checkpoints=%d want 2", len(m.GetState().Checkpoints))
	}
}

func TestRegisterScopePersist(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, "test-agent")
	m.SetRegisterScope(RegisterScope{
		DisableOffloading:   true,
		SkipOperatorInstall: true,
		HypervisorDriver:    "virtfusion",
		VNCGatewayNamespace: "custom-vnc",
		OffloadNamespaces:   []string{"extra"},
	})
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(dir, "test-agent")
	if err := m2.Load(); err != nil {
		t.Fatal(err)
	}
	got := m2.GetRegisterScope()
	if got == nil || !got.DisableOffloading || !got.SkipOperatorInstall || got.VNCGatewayNamespace != "custom-vnc" || got.HypervisorDriver != "virtfusion" {
		t.Fatalf("persisted scope=%+v", got)
	}
}
