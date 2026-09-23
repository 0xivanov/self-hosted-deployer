package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"time"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"github.com/0xivanov/self-hosted-deployer/internal/registryauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type RegistryCredentialRepository interface {
	Create(context.Context, domain.RegistryCredential) error
	Find(context.Context, string, string) (domain.RegistryCredential, error)
	ListByApp(context.Context, string) ([]domain.RegistryCredential, error)
	DeleteByApp(context.Context, string) error
}
type RegistryCredentialResolver interface {
	ResolveRegistryCredential(context.Context, string, string, string) (registryauth.Credential, error)
}
type RegistryCredentialRuntime interface {
	ReconcileWithRegistry(context.Context, appconfig.Config, map[string]string, string, *registryauth.Credential) error
}
type RegistryCredentialService struct {
	deployerv1.UnimplementedRegistryCredentialServiceServer
	repo   RegistryCredentialRepository
	cipher SecretCipher
}

func NewRegistryCredentialService(repo RegistryCredentialRepository, cipher SecretCipher) *RegistryCredentialService {
	return &RegistryCredentialService{repo: repo, cipher: cipher}
}

// The authenticated ciphertext includes a domain separator and all row identity
// fields. Copying ciphertext across applications or revisions cannot rebind it.
type registryCredentialPayload struct {
	Purpose  string `json:"purpose"`
	AppName  string `json:"app_name"`
	Revision string `json:"revision"`
	Registry string `json:"registry"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func credentialPayload(c registryauth.Credential) registryCredentialPayload {
	return registryCredentialPayload{"deployer.registry.v1", c.AppName, c.Revision, c.Registry, c.Username, c.Password}
}
func (s *RegistryCredentialService) configured() error {
	if s == nil || s.repo == nil || s.cipher == nil {
		return status.Error(codes.FailedPrecondition, "registry credentials are not configured")
	}
	return nil
}
func (s *RegistryCredentialService) decode(row domain.RegistryCredential) (registryauth.Credential, error) {
	var p registryCredentialPayload
	plaintext, err := s.cipher.Decrypt(row.Ciphertext)
	if err != nil || len(plaintext) > 20000 || json.Unmarshal([]byte(plaintext), &p) != nil || p.Purpose != "deployer.registry.v1" || p.AppName != row.AppName || p.Revision != row.Revision || p.Registry != row.Registry {
		return registryauth.Credential{}, status.Error(codes.FailedPrecondition, "registry credential cannot be resolved")
	}
	c := registryauth.Credential{AppName: p.AppName, Revision: p.Revision, Registry: p.Registry, Username: p.Username, Password: p.Password}
	if c.Validate() != nil {
		return registryauth.Credential{}, status.Error(codes.FailedPrecondition, "registry credential cannot be resolved")
	}
	return c, nil
}
func registryMetadata(row domain.RegistryCredential) *deployerv1.RegistryCredentialMetadata {
	return &deployerv1.RegistryCredentialMetadata{AppName: row.AppName, Revision: row.Revision, Registry: row.Registry, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
}
func (s *RegistryCredentialService) CreateRegistryCredential(ctx context.Context, req *deployerv1.CreateRegistryCredentialRequest) (*deployerv1.CreateRegistryCredentialResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if err := s.configured(); err != nil {
		return nil, err
	}
	c := registryauth.Credential{AppName: req.GetAppName(), Revision: req.GetRevision(), Registry: req.GetRegistry(), Username: req.GetUsername(), Password: req.GetPassword()}
	if c.Validate() != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid registry credential")
	}
	payload, _ := json.Marshal(credentialPayload(c))
	// Client-generated revision makes a lost-reply retry idempotent. A revision
	// never changes value; rotation must choose a new revision.
	existing, err := s.repo.Find(ctx, c.AppName, c.Revision)
	if err == nil {
		return s.sameCredential(existing, payload)
	}
	if !errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.Internal, "read registry credential")
	}
	ciphertext, err := s.cipher.Encrypt(string(payload))
	if err != nil {
		return nil, status.Error(codes.Internal, "encrypt registry credential")
	}
	row := domain.RegistryCredential{AppName: c.AppName, Revision: c.Revision, Registry: c.Registry, Ciphertext: ciphertext, CreatedAt: time.Now().UTC()}
	if err = s.repo.Create(ctx, row); err != nil {
		// Another retry may have inserted the same revision concurrently.
		if old, lookupErr := s.repo.Find(ctx, c.AppName, c.Revision); lookupErr == nil {
			return s.sameCredential(old, payload)
		}
		if errors.Is(err, db.ErrRegistryCredentialLimit) {
			return nil, status.Error(codes.ResourceExhausted, "registry credential revision limit reached")
		}
		return nil, status.Error(codes.Internal, "save registry credential")
	}
	return &deployerv1.CreateRegistryCredentialResponse{Credential: registryMetadata(row)}, nil
}
func (s *RegistryCredentialService) sameCredential(row domain.RegistryCredential, payload []byte) (*deployerv1.CreateRegistryCredentialResponse, error) {
	c, err := s.decode(row)
	if err != nil {
		return nil, err
	}
	previous, _ := json.Marshal(credentialPayload(c))
	if subtle.ConstantTimeCompare(previous, payload) != 1 {
		return nil, status.Error(codes.AlreadyExists, "credential revision is immutable; use a new revision")
	}
	return &deployerv1.CreateRegistryCredentialResponse{Credential: registryMetadata(row)}, nil
}
func (s *RegistryCredentialService) ListRegistryCredentials(ctx context.Context, req *deployerv1.ListRegistryCredentialsRequest) (*deployerv1.ListRegistryCredentialsResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if err := s.configured(); err != nil {
		return nil, err
	}
	if !registryauth.ValidAppName(req.GetAppName()) {
		return nil, status.Error(codes.InvalidArgument, "invalid app name")
	}
	rows, err := s.repo.ListByApp(ctx, req.GetAppName())
	if err != nil {
		return nil, status.Error(codes.Internal, "list registry credentials")
	}
	response := &deployerv1.ListRegistryCredentialsResponse{}
	for _, row := range rows {
		response.Credentials = append(response.Credentials, registryMetadata(row))
	}
	return response, nil
}
func (s *RegistryCredentialService) ResolveRegistryCredential(ctx context.Context, appName, revision, image string) (registryauth.Credential, error) {
	if err := s.configured(); err != nil {
		return registryauth.Credential{}, err
	}
	if !registryauth.ValidAppName(appName) || !registryauth.ValidRevision(revision) {
		return registryauth.Credential{}, status.Error(codes.InvalidArgument, "invalid registry credential reference")
	}
	row, err := s.repo.Find(ctx, appName, revision)
	if err != nil {
		return registryauth.Credential{}, status.Error(codes.FailedPrecondition, "registry credential is unavailable for this app")
	}
	c, err := s.decode(row)
	if err != nil {
		return registryauth.Credential{}, err
	}
	if c.ValidateFor(appName, revision, image) != nil {
		return registryauth.Credential{}, status.Error(codes.FailedPrecondition, "registry credential does not match this app and image")
	}
	return c, nil
}

func resolveRuntimeRegistry(ctx context.Context, runtime AppRuntime, resolver RegistryCredentialResolver, cfg appconfig.Config) (*registryauth.Credential, error) {
	if cfg.ImagePullCredential == "" {
		return nil, nil
	}
	if _, ok := runtime.(RegistryCredentialRuntime); !ok || resolver == nil {
		return nil, status.Error(codes.FailedPrecondition, "private image pulling is unavailable")
	}
	c, err := resolver.ResolveRegistryCredential(ctx, cfg.Name, cfg.ImagePullCredential, cfg.Image)
	if err != nil {
		return nil, err
	}
	if c.ValidateFor(cfg.Name, cfg.ImagePullCredential, cfg.Image) != nil {
		return nil, status.Error(codes.FailedPrecondition, "invalid registry credential binding")
	}
	return &c, nil
}
func reconcileRuntime(ctx context.Context, runtime AppRuntime, cfg appconfig.Config, values map[string]string, revision string, credential *registryauth.Credential) error {
	if cfg.ImagePullCredential == "" {
		return runtime.Reconcile(ctx, cfg, values, revision)
	}
	private, ok := runtime.(RegistryCredentialRuntime)
	if !ok || credential == nil {
		return status.Error(codes.FailedPrecondition, "private image pulling is unavailable")
	}
	return private.ReconcileWithRegistry(ctx, cfg, values, revision, credential)
}
