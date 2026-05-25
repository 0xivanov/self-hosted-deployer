package domain

import "time"

type AdminToken struct {
	TokenHash  string
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

type AgentToken struct {
	TokenHash  string
	NodeID     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

type JoinToken struct {
	TokenHash        string
	IntendedNodeName string
	LabelsJSON       string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	UsedAt           *time.Time
}

type Node struct {
	ID                 string
	Name               string
	Status             string
	LabelsJSON         string
	Hostname           string
	Arch               string
	OS                 string
	Kernel             string
	WireGuardIP        string
	WireGuardPublicKey string
	LastSeenAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type App struct {
	ID               string
	Name             string
	Image            string
	DesiredStateJSON string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
}

type Deployment struct {
	ID            string
	AppID         string
	Status        string
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Secret struct {
	AppID      string
	Name       string
	Ciphertext string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Route struct {
	ID         string
	AppID      string
	Domain     string
	TargetPort int
	Status     string
	TLSEnabled bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type EventSeverity string

const (
	EventSeverityInfo    EventSeverity = "info"
	EventSeverityWarning EventSeverity = "warning"
	EventSeverityError   EventSeverity = "error"
)

type EventType string

const (
	EventTypeNodeJoined         EventType = "node.joined"
	EventTypeNodeOnline         EventType = "node.online"
	EventTypeNodeOffline        EventType = "node.offline"
	EventTypeNodeRemoved        EventType = "node.removed"
	EventTypeAppDeployStarted   EventType = "app.deploy.started"
	EventTypeAppDeploySucceeded EventType = "app.deploy.succeeded"
	EventTypeAppDeployFailed    EventType = "app.deploy.failed"
	EventTypeAppHealthDegraded  EventType = "app.health.degraded"
	EventTypeAppHealthRecovered EventType = "app.health.recovered"
	EventTypeRouteDegraded      EventType = "route.degraded"
	EventTypeRouteRecovered     EventType = "route.recovered"
	EventTypeSecretCreated      EventType = "secret.created"
	EventTypeSecretUpdated      EventType = "secret.updated"
	EventTypeSecretDeleted      EventType = "secret.deleted"
)

type Event struct {
	ID           string
	CreatedAt    time.Time
	Type         EventType
	Severity     EventSeverity
	Message      string
	AppID        string
	NodeID       string
	DeploymentID string
	MetadataJSON string
}

type EventCursor struct {
	CreatedAt time.Time
	ID        string
}

type EventFilter struct {
	AppID       string
	NodeID      string
	Type        EventType
	Severity    EventSeverity
	Since       *time.Time
	After       *EventCursor
	OldestFirst bool
	Limit       int
}
