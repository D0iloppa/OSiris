package docker

// Wire types for the subset of the Engine API this driver uses. Field names
// match the Engine API's JSON exactly (Docker's API is not Go-idiomatic
// casing) — these are intentionally 1:1 with the wire format rather than
// pulled from the moby/moby module, to avoid the dependency (see client.go
// doc comment). None of these types are exported outside this package.

type networkSummary struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type imageSummary struct {
	ID string `json:"Id"`
}

type volumeCreateRequest struct {
	Name string `json:"Name"`
}

type mountSpec struct {
	Type         string        `json:"Type"` // "volume" | "bind" | "tmpfs"
	Source       string        `json:"Source,omitempty"`
	Target       string        `json:"Target"`
	ReadOnly     bool          `json:"ReadOnly,omitempty"`
	TmpfsOptions *tmpfsOptions `json:"TmpfsOptions,omitempty"`
}

type tmpfsOptions struct {
	SizeBytes int64 `json:"SizeBytes,omitempty"`
}

type restartPolicy struct {
	Name string `json:"Name"`
}

type healthcheckSpec struct {
	Test        []string `json:"Test"`
	Interval    int64    `json:"Interval,omitempty"` // nanoseconds
	Timeout     int64    `json:"Timeout,omitempty"`  // nanoseconds
	Retries     int      `json:"Retries,omitempty"`
	StartPeriod int64    `json:"StartPeriod,omitempty"` // nanoseconds
}

type endpointSettings struct {
	Aliases []string `json:"Aliases,omitempty"`
}

type networkingConfig struct {
	EndpointsConfig map[string]endpointSettings `json:"EndpointsConfig"`
}

type hostConfig struct {
	Memory         int64         `json:"Memory,omitempty"`
	NanoCPUs       int64         `json:"NanoCpus,omitempty"`
	RestartPolicy  restartPolicy `json:"RestartPolicy"`
	ReadonlyRootfs bool          `json:"ReadonlyRootfs"`
	CapDrop        []string      `json:"CapDrop,omitempty"`
	SecurityOpt    []string      `json:"SecurityOpt,omitempty"`
	Mounts         []mountSpec   `json:"Mounts,omitempty"`
	NetworkMode    string        `json:"NetworkMode,omitempty"`
}

type containerCreateRequest struct {
	Image            string            `json:"Image"`
	Env              []string          `json:"Env,omitempty"`
	Labels           map[string]string `json:"Labels,omitempty"`
	User             string            `json:"User,omitempty"`
	Healthcheck      *healthcheckSpec  `json:"Healthcheck,omitempty"`
	HostConfig       hostConfig        `json:"HostConfig"`
	NetworkingConfig *networkingConfig `json:"NetworkingConfig,omitempty"`
}

type containerCreateResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

type containerListItem struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"` // running/created/exited/restarting/...
	Labels map[string]string `json:"Labels"`
}

type containerInspect struct {
	ID    string `json:"Id"`
	State struct {
		Status string `json:"Status"`
		Health *struct {
			Status string `json:"Status"`
		} `json:"Health,omitempty"`
	} `json:"State"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}
