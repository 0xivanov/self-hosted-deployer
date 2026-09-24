package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/domain"
)

func TestCandidateGenerationsStayWithinBoundAppIdentity(t *testing.T) {
	database := openRepositoryTestDB(t)
	repository := NewCandidateBindingRepository(database)
	ctx := context.Background()
	first, firstState := candidateBindingConfig(t)
	requestID := strings.Repeat("a", 64)
	for _, name := range []string{first.Name, "another-app"} {
		cfg := first
		cfg.Name = name
		cfg.Routing.Domain = ""
		state, err := cfg.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if name == first.Name {
			firstState = state
		}
		_, _, err = repository.Begin(ctx, domain.DeployRequest{AppName: name, RequestID: requestID, RequestedState: state, ReportWithdrawal: true}, "app-"+name, "deployment-"+name, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.conn.ExecContext(ctx, `UPDATE deployment_requests SET state='withdrawn', response_json='{}' WHERE app_name=?`, first.Name); err != nil {
		t.Fatal(err)
	}
	items, err := repository.GenerationsByApp(ctx, first.Name, "app-"+first.Name)
	if err != nil || len(items) != 1 || items[0].RequestedState != firstState || items[0].RequestID != requestID || items[0].State != "withdrawn" {
		t.Fatalf("generation history: %+v %v", items, err)
	}
	items, err = repository.GenerationsByApp(ctx, first.Name, "app-another-app")
	if err != nil || len(items) != 0 {
		t.Fatalf("cross-app history returned: %+v %v", items, err)
	}
}
