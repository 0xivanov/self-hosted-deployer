package domain

// RuntimeCheckpoint is the durable activation state for one pending deploy.
// IntentJSON and GateJSON contain only serialized control-plane metadata.
type RuntimeCheckpoint struct {
	AppName    string
	RequestID  string
	Stage      string
	IntentJSON string
	GateJSON   string
}
