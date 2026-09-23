package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/db"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	deployerv1 "github.com/0xivanov/self-hosted-deployer/internal/proto/deployer/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type EnvironmentBundleRepository interface {
	Create(context.Context, domain.EnvironmentBundle) error
	Find(context.Context, string, string) (domain.EnvironmentBundle, error)
	DeleteByApp(context.Context, string) error
}

type EnvironmentBundleService struct {
	deployerv1.UnimplementedEnvironmentServiceServer
	bundles EnvironmentBundleRepository
	cipher  SecretCipher
}

func NewEnvironmentBundleService(bundles EnvironmentBundleRepository, cipher SecretCipher) *EnvironmentBundleService {
	return &EnvironmentBundleService{bundles: bundles, cipher: cipher}
}

func (s *EnvironmentBundleService) CreateEnvironmentBundle(ctx context.Context, req *deployerv1.CreateEnvironmentBundleRequest) (*deployerv1.CreateEnvironmentBundleResponse, error) {
	if err := requireCaller(ctx, CallerAdmin); err != nil {
		return nil, err
	}
	if s.bundles == nil || s.cipher == nil {
		return nil, status.Error(codes.FailedPrecondition, "environment bundles are not configured")
	}
	appName, revision := strings.TrimSpace(req.GetAppName()), req.GetRevision()
	if !appconfig.ValidAppName(appName) || !validEnvironmentRevision(revision) || !validEnvironmentValues(req.GetValues()) {
		return nil, status.Error(codes.InvalidArgument, "invalid environment bundle")
	}
	values := cloneEnvironmentValues(req.GetValues())
	payload, err := json.Marshal(struct {
		Purpose, AppName, Revision string
		Values                     map[string]string
	}{"deployer.environment.v1", appName, revision, values})
	if err != nil {
		return nil, status.Error(codes.Internal, "encode environment bundle")
	}
	existing, findErr := s.bundles.Find(ctx, appName, revision)
	if findErr == nil {
		return s.sameEnvironment(existing, payload, values)
	}
	if !errors.Is(findErr, db.ErrNotFound) {
		return nil, status.Error(codes.Internal, "read environment bundle")
	}
	ciphertext, err := s.cipher.Encrypt(string(payload))
	if err != nil {
		return nil, status.Error(codes.Internal, "encrypt environment bundle")
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	namesJSON, _ := json.Marshal(names)
	now := time.Now().UTC()
	bundle := domain.EnvironmentBundle{AppName: appName, Revision: revision, NamesJSON: string(namesJSON), Ciphertext: ciphertext, CreatedAt: now}
	if err := s.bundles.Create(ctx, bundle); err != nil {
		if old, lookupErr := s.bundles.Find(ctx, appName, revision); lookupErr == nil {
			return s.sameEnvironment(old, payload, values)
		}
		if errors.Is(err, db.ErrEnvironmentBundleLimit) {
			return nil, status.Error(codes.ResourceExhausted, "environment revision limit reached")
		}
		return nil, status.Error(codes.Internal, "save environment bundle")
	}
	return &deployerv1.CreateEnvironmentBundleResponse{Bundle: environmentMetadata(appName, revision, values, now)}, nil
}

func validEnvironmentRevision(revision string) bool {
	return len(revision) == 64 && strings.Trim(revision, "0123456789abcdef") == ""
}
func validEnvironmentValues(values map[string]string) bool {
	if len(values) > 64 {
		return false
	}
	total := 0
	for name, value := range values {
		if appconfig.ValidateSecretName(name) != nil || len(name) > 128 || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 || len(value) > 8192 {
			return false
		}
		total += len(name) + len(value)
		if total > 32768 {
			return false
		}
	}
	return true
}
func cloneEnvironmentValues(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		other, ok := b[key]
		if !ok || other != value {
			return false
		}
	}
	return true
}
func environmentMetadata(app, revision string, values map[string]string, created time.Time) *deployerv1.EnvironmentBundleMetadata {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return &deployerv1.EnvironmentBundleMetadata{AppName: app, Revision: revision, Names: names, CreatedAt: created.UTC().Format(time.RFC3339Nano)}
}

func resolveEnvironmentBundle(ctx context.Context, bundles EnvironmentBundleRepository, cipher SecretCipher, appName, revision string) (map[string]string, error) {
	if bundles == nil || cipher == nil {
		return nil, status.Error(codes.FailedPrecondition, "environment bundles are not configured")
	}
	bundle, err := bundles.Find(ctx, appName, revision)
	if errors.Is(err, db.ErrNotFound) {
		return nil, status.Error(codes.FailedPrecondition, "environment revision is not staged")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "read environment bundle")
	}
	plain, err := cipher.Decrypt(bundle.Ciphertext)
	if err != nil {
		return nil, status.Error(codes.Internal, "decrypt environment bundle")
	}
	var payload struct {
		Purpose, AppName, Revision string
		Values                     map[string]string
	}
	if len(plain) > 256<<10 || json.Unmarshal([]byte(plain), &payload) != nil || payload.Purpose != "deployer.environment.v1" || payload.AppName != appName || payload.Revision != revision || !validEnvironmentValues(payload.Values) {
		return nil, status.Error(codes.FailedPrecondition, "environment revision is invalid")
	}
	return payload.Values, nil
}

func (s *EnvironmentBundleService) sameEnvironment(row domain.EnvironmentBundle, payload []byte, values map[string]string) (*deployerv1.CreateEnvironmentBundleResponse, error) {
	plain, err := s.cipher.Decrypt(row.Ciphertext)
	if err != nil {
		return nil, status.Error(codes.Internal, "read environment bundle")
	}
	if subtle.ConstantTimeCompare([]byte(plain), payload) != 1 {
		return nil, status.Error(codes.AlreadyExists, "environment revision is immutable; use a new revision")
	}
	return &deployerv1.CreateEnvironmentBundleResponse{Bundle: environmentMetadata(row.AppName, row.Revision, values, row.CreatedAt)}, nil
}
