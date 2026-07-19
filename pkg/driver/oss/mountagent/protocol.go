package mountagent

const SocketPath = "/var/run/geesefs-mount-agent/geesefs.sock"

type MountRequest struct {
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	Target          string `json:"target"`
	CredentialFile  string `json:"credentialFile"`
	AddressingStyle string `json:"addressingStyle,omitempty"`
}

type Response struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}
