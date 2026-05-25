package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SecretRepository interface {
	Set(ctx context.Context, secret domain.Secret) error
	Find(ctx context.Context, appID string, name string) (domain.Secret, error)
	ListNamesByApp(ctx context.Context, appID string) ([]string, error)
	Delete(ctx context.Context, appID string, name string) error
}

type SecretCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

type requiredSecretNotSetError struct {
	name string
}

func (e requiredSecretNotSetError) Error() string {
	return fmt.Sprintf("required secret %q is not set", e.name)
}

type SecretServiceConfig struct {
	Apps    AppRepository
	Secrets SecretRepository
	Cipher  SecretCipher
	Runtime AppRuntime
}

type SecretService struct {
	deployerv1.UnimplementedSecretServiceServer
	apps    AppRepository
	secrets SecretRepository
	cipher  SecretCipher
	runtime AppRuntime
	now     func() time.Time
}

func NewSecretService(cfg SecretServiceConfig) SecretService {
	return SecretService{
		apps:    cfg.Apps,
		secrets: cfg.Secrets,
		cipher:  cfg.Cipher,
		runtime: cfg.Runtime,
		now:     time.Now,
	}
}

func (s SecretService) SetSecret(ctx context.Context, req *deployerv1.SetSecretRequest) (*deployerv1.SetSecretResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if err := s.requireConfigured(); err != nil {
		return nil, err
	}
	app, name, err := s.appAndSecretName(ctx, req.GetAppName(), req.GetName())
	if err != nil {
		return nil, err
	}
	ciphertext, err := s.cipher.Encrypt(req.GetValue())
	if err != nil {
		return nil, status.Error(codes.Internal, "encrypt secret")
	}
	now := s.now().UTC()
	if err := s.secrets.Set(ctx, domain.Secret{
		AppID:      app.ID,
		Name:       name,
		Ciphertext: ciphertext,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		return nil, status.Error(codes.Internal, "set secret")
	}
	if err := s.reconcileReferencedSecret(ctx, app, name); err != nil {
		return nil, err
	}
	return &deployerv1.SetSecretResponse{}, nil
}

func (s SecretService) ListSecrets(ctx context.Context, req *deployerv1.ListSecretsRequest) (*deployerv1.ListSecretsResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if err := s.requireConfigured(); err != nil {
		return nil, err
	}
	app, err := s.activeApp(ctx, req.GetAppName())
	if err != nil {
		return nil, err
	}
	names, err := s.secrets.ListNamesByApp(ctx, app.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "list secrets")
	}
	return &deployerv1.ListSecretsResponse{Names: names}, nil
}

func (s SecretService) DeleteSecret(ctx context.Context, req *deployerv1.DeleteSecretRequest) (*deployerv1.DeleteSecretResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if err := s.requireConfigured(); err != nil {
		return nil, err
	}
	app, name, err := s.appAndSecretName(ctx, req.GetAppName(), req.GetName())
	if err != nil {
		return nil, err
	}
	cfg, err := appconfig.FromJSON(app.DesiredStateJSON)
	if err != nil {
		return nil, status.Error(codes.Internal, "decode desired state")
	}
	if referencesSecret(cfg.Secrets, name) {
		return nil, status.Error(codes.FailedPrecondition, "secret is referenced by app configuration")
	}
	if err := s.secrets.Delete(ctx, app.ID, name); errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "secret not found")
	} else if err != nil {
		return nil, status.Error(codes.Internal, "delete secret")
	}
	return &deployerv1.DeleteSecretResponse{}, nil
}

func (s SecretService) appAndSecretName(ctx context.Context, appName string, name string) (domain.App, string, error) {
	app, err := s.activeApp(ctx, appName)
	if err != nil {
		return domain.App{}, "", err
	}
	name = strings.TrimSpace(name)
	if err := appconfig.ValidateSecretName(name); err != nil {
		return domain.App{}, "", status.Error(codes.InvalidArgument, err.Error())
	}
	return app, name, nil
}

func (s SecretService) activeApp(ctx context.Context, appName string) (domain.App, error) {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return domain.App{}, status.Error(codes.InvalidArgument, "app_name is required")
	}
	app, err := s.apps.FindActiveByName(ctx, appName)
	if errors.Is(err, db.ErrNotFound) {
		return domain.App{}, status.Error(codes.NotFound, "app not found")
	}
	if err != nil {
		return domain.App{}, status.Error(codes.Internal, "get app")
	}
	return app, nil
}

func (s SecretService) requireConfigured() error {
	if s.secrets == nil || s.cipher == nil {
		return status.Error(codes.FailedPrecondition, "secret management is not configured")
	}
	return nil
}

func (s SecretService) reconcileReferencedSecret(ctx context.Context, app domain.App, name string) error {
	if s.runtime == nil {
		return nil
	}
	cfg, err := appconfig.FromJSON(app.DesiredStateJSON)
	if err != nil {
		return status.Error(codes.Internal, "decode desired state")
	}
	if !referencesSecret(cfg.Secrets, name) {
		return nil
	}
	secretValues, secretRevision, err := resolveSecretValues(ctx, s.secrets, s.cipher, app.ID, cfg.Secrets)
	if err != nil {
		var missing requiredSecretNotSetError
		if errors.As(err, &missing) {
			return nil
		}
		return err
	}
	if err := s.runtime.Reconcile(ctx, cfg, secretValues, secretRevision); err != nil {
		return status.Error(codes.Internal, "apply updated app secret")
	}
	return nil
}

func resolveSecretValues(ctx context.Context, secrets SecretRepository, cipher SecretCipher, appID string, names []string) (map[string]string, string, error) {
	values := make(map[string]string, len(names))
	if len(names) == 0 {
		return values, "", nil
	}
	if secrets == nil || cipher == nil {
		return nil, "", status.Error(codes.FailedPrecondition, "secret management is not configured")
	}
	revision := sha256.New()
	for _, name := range names {
		secret, err := secrets.Find(ctx, appID, name)
		if errors.Is(err, db.ErrNotFound) {
			return nil, "", requiredSecretNotSetError{name: name}
		}
		if err != nil {
			return nil, "", status.Error(codes.Internal, "read app secret")
		}
		value, err := cipher.Decrypt(secret.Ciphertext)
		if err != nil {
			return nil, "", status.Error(codes.Internal, "decrypt app secret")
		}
		values[name] = value
		_, _ = revision.Write([]byte(name))
		_, _ = revision.Write([]byte{0})
		_, _ = revision.Write([]byte(secret.Ciphertext))
		_, _ = revision.Write([]byte{0})
	}
	return values, hex.EncodeToString(revision.Sum(nil)), nil
}

func referencesSecret(names []string, name string) bool {
	for _, configured := range names {
		if configured == name {
			return true
		}
	}
	return false
}
