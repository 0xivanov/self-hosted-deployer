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
	ID         string
	Name       string
	Status     string
	LabelsJSON string
	CreatedAt  time.Time
	UpdatedAt  time.Time
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
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
