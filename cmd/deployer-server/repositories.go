package main

import (
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/server"
)

func newRepositories(database *db.Db) server.Repositories {
	return server.Repositories{
		Health:      db.NewHealthRepository(database),
		AdminTokens: db.NewAdminTokenRepository(database),
		AgentTokens: db.NewAgentTokenRepository(database),
		JoinTokens:  db.NewJoinTokenRepository(database),
		Nodes:       db.NewNodeRepository(database),
	}
}
