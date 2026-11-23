package verify

// Status is the outcome of a single check.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusWarn Status = "warn"
	StatusSkip Status = "skip"
)

// Section identifiers used in reports and --section filtering.
const (
	SectionKubernetes = "kubernetes"
	SectionCalico     = "calico"
	SectionLiqo       = "liqo"
	SectionPeering    = "peering"
	SectionOffloading = "offloading"
	SectionAgent      = "agent"
	SectionOperator   = "operator"
)

// AllSections is the ordered list of verify subsections.
var AllSections = []string{
	SectionKubernetes,
	SectionCalico,
	SectionLiqo,
	SectionPeering,
	SectionOffloading,
	SectionAgent,
	SectionOperator,
}

// Check is one named assertion inside a section.
type Check struct {
	Section string `json:"section"`
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message"`
}

// SectionResult groups checks under a subsection name.
type SectionResult struct {
	Name   string  `json:"name"`
	Checks []Check `json:"checks"`
}

// Report is the full provider-cluster verification result.
type Report struct {
	AgentID  string          `json:"agent_id,omitempty"`
	Sections []SectionResult `json:"sections"`
}

// FailedCount returns how many checks have StatusFail.
func (r *Report) FailedCount() int {
	n := 0
	for _, sec := range r.Sections {
		for _, c := range sec.Checks {
			if c.Status == StatusFail {
				n++
			}
		}
	}
	return n
}

// Passed reports whether no check failed (warn/skip are OK).
func (r *Report) Passed() bool {
	return r.FailedCount() == 0
}

// FlatChecks returns all checks in report order.
func (r *Report) FlatChecks() []Check {
	var out []Check
	for _, sec := range r.Sections {
		out = append(out, sec.Checks...)
	}
	return out
}
