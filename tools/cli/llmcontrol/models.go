package llmcontrol

type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// RunningInfo describes a model a machine is currently running (fleet view).
type RunningInfo struct {
	Name      string `json:"name"`
	Alias     string `json:"alias,omitempty"`
	Status    string `json:"status"`
	StartTime string `json:"start_time,omitempty"`
	Port      int    `json:"port"`
}

// ModelItem is the subset of a model entry the CLI displays.
type ModelItem struct {
	Name   string `json:"name"`
	Alias  string `json:"alias,omitempty"`
	Port   *int   `json:"port,omitempty"`
	Active bool   `json:"active"`
}

// FleetMachine is one machine in the coordinator's fleet view: the
// coordinator itself ("local") first, then its workers.
type FleetMachine struct {
	Machine string       `json:"machine"`
	URL     string       `json:"url,omitempty"`
	Status  string       `json:"status"`
	Running *RunningInfo `json:"running,omitempty"`
	Models  []ModelItem  `json:"models"`
	Error   string       `json:"error,omitempty"`
}

// FleetResponse wraps GET /api/v1/fleet's data.
type FleetResponse struct {
	Machines []FleetMachine `json:"machines"`
}
