package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func recoveryHubConfig() config.ServerConfig {
	return config.ServerConfig{K3sWireGuardIP: "10.81.0.1", WireGuardHubPublicKey: validWireGuardPublicKey, WireGuardEndpoint: "hub.example.test:51820"}
}

func TestRestoreHubPeersUsesSavedNodesWithoutEnrollment(t *testing.T) {
	ctx := context.Background()
	nodes := db.NewNodeRepository(openTestDB(t))
	now := time.Now().UTC()
	err := nodes.Create(ctx, domain.Node{ID: "saved-worker", Name: "saved-worker", Status: nodeStatusOffline, LabelsJSON: "{}", WireGuardIP: "10.81.0.2", WireGuardPublicKey: validWireGuardPublicKey, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	peers := &recordingPeerSynchronizer{}
	if err := restoreHubPeers(ctx, recoveryHubConfig(), nodes, peers); err != nil {
		t.Fatal(err)
	}
	if peers.calls != 1 || len(peers.lastNodes) != 1 || peers.lastNodes[0].WireGuardIP != "10.81.0.2" {
		t.Fatalf("saved worker was not restored: %#v", peers)
	}
}

func TestRestoreHubPeersSkipsUnconfiguredLegacyServer(t *testing.T) {
	peers := &recordingPeerSynchronizer{}
	if err := restoreHubPeers(context.Background(), config.ServerConfig{}, nil, peers); err != nil {
		t.Fatal(err)
	}
	if peers.calls != 0 {
		t.Fatal("unconfigured interface was modified")
	}
}

type failingRecoveryNodes struct{ NodeRepository }

func (failingRecoveryNodes) List(context.Context) ([]domain.Node, error) {
	return nil, errors.New("database unavailable")
}

type failingRecoveryPeers struct{}

func (failingRecoveryPeers) SyncPeers(ctx context.Context, _ []domain.Node) error {
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("missing deadline")
	}
	return errors.New("interface unavailable")
}
func TestRestoreHubPeersPropagatesFailures(t *testing.T) {
	peers := &recordingPeerSynchronizer{}
	if err := restoreHubPeers(context.Background(), recoveryHubConfig(), failingRecoveryNodes{}, peers); err == nil {
		t.Fatal("expected database error")
	}
	if peers.calls != 0 {
		t.Fatal("modified peers after failed database read")
	}
	nodes := db.NewNodeRepository(openTestDB(t))
	if err := restoreHubPeers(context.Background(), recoveryHubConfig(), nodes, failingRecoveryPeers{}); err == nil || err.Error() != "interface unavailable" {
		t.Fatalf("unexpected error: %v", err)
	}
}
