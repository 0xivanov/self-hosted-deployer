package main

import (
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/server"
)

func newRepositories(database *db.Db) server.Repositories {
	return server.Repositories{
		CandidateBindings:    db.NewCandidateBindingRepository(database),
		CandidateCheckpoints: db.NewRuntimeCheckpointRepository(database),
		CandidateFinalizer:   db.NewCandidateFinalizationRepository(database),
		DeploymentRequests:   db.NewDeploymentRequestRepository(database),
		RegistryCredentials:  db.NewRegistryCredentialRepository(database),
		EnvironmentBundles:   db.NewEnvironmentBundleRepository(database),
		Health:               db.NewHealthRepository(database),
		AdminTokens:          db.NewAdminTokenRepository(database),
		AgentTokens:          db.NewAgentTokenRepository(database),
		JoinTokens:           db.NewJoinTokenRepository(database),
		Nodes:                db.NewNodeRepository(database),
		Apps:                 db.NewAppRepository(database),
		Deployments:          db.NewDeploymentRepository(database),
		Routes:               db.NewRouteRepository(database),
		Secrets:              db.NewSecretRepository(database),
		Events:               db.NewEventRepository(database),
	}
}
