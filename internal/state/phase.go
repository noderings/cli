package state

// Phase is an installation phase persisted in state.json.
type Phase string

const (
	PhaseCurrent         Phase = "current"
	PhaseK3s             Phase = "k3s"
	PhaseCalico          Phase = "calico"
	PhaseLiqo            Phase = "liqo"
	PhasePeering         Phase = "peering"
	PhaseOffloading      Phase = "offloading"
	PhaseInboundPeering  Phase = "inbound_peering"
	PhaseOperatorInstall Phase = "operator_install"
	PhaseComplete        Phase = "complete"
	PhaseFailed          Phase = "failed"
)

// CheckpointStatus is the status of an installation checkpoint.
type CheckpointStatus string

const (
	CheckpointStatusSuccess CheckpointStatus = "success"
	CheckpointStatusFailed  CheckpointStatus = "failed"
)
