package iam

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"GoBlob/goblob/filer"
	iampb "GoBlob/goblob/pb/iam_pb"
)

// FilerIAMClient reads/writes IAM config in filer KV.
type FilerIAMClient interface {
	KvGet(ctx context.Context, key []byte) ([]byte, error)
	KvPut(ctx context.Context, key []byte, value []byte) error
}

// IdentityAccessManagement manages S3 identities in memory.
type IdentityAccessManagement struct {
	mu         sync.RWMutex
	identities []*Identity

	hashesMu sync.RWMutex
	hashes   map[string]*sync.Pool

	filerClient FilerIAMClient
	logger      *slog.Logger
}

// NewIdentityAccessManagement creates a manager and eagerly loads config when filerClient is set.
func NewIdentityAccessManagement(filerClient FilerIAMClient, logger *slog.Logger) (*IdentityAccessManagement, error) {
	if logger == nil {
		logger = slog.Default()
	}
	iam := &IdentityAccessManagement{
		identities:  make([]*Identity, 0),
		hashes:      make(map[string]*sync.Pool),
		filerClient: filerClient,
		logger:      logger.With("component", "iam"),
	}
	if filerClient != nil {
		if err := iam.LoadFromFiler(context.Background()); err != nil {
			return nil, err
		}
	}
	return iam, nil
}

// ValidateConfiguration validates the input IAM proto config.
func ValidateConfiguration(cfg *iampb.S3ApiConfiguration) error {
	if cfg == nil {
		return nil
	}
	seenAccessKeys := make(map[string]string)
	for _, identity := range cfg.GetIdentities() {
		if identity == nil {
			continue
		}
		if strings.TrimSpace(identity.GetName()) == "" {
			return fmt.Errorf("identity name is required")
		}
		for _, cred := range identity.GetCredentials() {
			if cred == nil {
				continue
			}
			ak := strings.TrimSpace(cred.GetAccessKey())
			sk := strings.TrimSpace(cred.GetSecretKey())
			if ak == "" || sk == "" {
				return fmt.Errorf("identity %q has empty access_key or secret_key", identity.GetName())
			}
			if owner, ok := seenAccessKeys[ak]; ok {
				return fmt.Errorf("duplicate access_key %q in identities %q and %q", ak, owner, identity.GetName())
			}
			seenAccessKeys[ak] = identity.GetName()
		}
	}
	return nil
}

// LookupByAccessKey performs a linear scan through identities.
func (iam *IdentityAccessManagement) LookupByAccessKey(accessKey string) (*Identity, string, bool) {
	iam.mu.RLock()
	defer iam.mu.RUnlock()
	for _, identity := range iam.identities {
		if identity == nil {
			continue
		}
		for _, cred := range identity.Credentials {
			if cred == nil {
				continue
			}
			if cred.AccessKey == accessKey {
				return identity, cred.SecretKey, true
			}
		}
	}
	return nil, "", false
}

// Authenticate validates access key existence and returns identity + secret key.
func (iam *IdentityAccessManagement) Authenticate(accessKey string) AuthResult {
	if accessKey == "" {
		return AuthResult{IsAnon: true}
	}
	identity, secret, ok := iam.LookupByAccessKey(accessKey)
	if !ok {
		return AuthResult{ErrorCode: "InvalidAccessKeyId"}
	}
	return AuthResult{Identity: identity, SecretKey: secret}
}

var readActions = map[S3Action]struct{}{
	S3ActionGetObject:           {},
	S3ActionHeadObject:          {},
	S3ActionListBucket:          {},
	S3ActionListAllMyBuckets:    {},
	S3ActionGetBucketVersioning: {},
	S3ActionGetBucketPolicy:     {},
	S3ActionGetBucketCors:       {},
	S3ActionGetObjectTagging:    {},
}

var writeActions = map[S3Action]struct{}{
	S3ActionPutObject:           {},
	S3ActionDeleteObject:        {},
	S3ActionCreateBucket:        {},
	S3ActionDeleteBucket:        {},
	S3ActionCreateMultipart:     {},
	S3ActionUploadPart:          {},
	S3ActionCompleteMultipart:   {},
	S3ActionAbortMultipart:      {},
	S3ActionPutBucketVersioning: {},
	S3ActionPutBucketPolicy:     {},
	S3ActionDeleteBucketPolicy:  {},
	S3ActionPutBucketCors:       {},
	S3ActionPutObjectTagging:    {},
	S3ActionDeleteObjectTagging: {},
}

// IsAuthorized checks action-level and optional bucket/object scoped grants.
func (iam *IdentityAccessManagement) IsAuthorized(identity *Identity, action S3Action, bucket, object string) bool {
	if identity == nil {
		return false
	}
	a := strings.TrimSpace(string(action))
	if a == "" {
		return false
	}
	for _, grant := range identity.Actions {
		g := strings.TrimSpace(grant)
		if g == "" {
			continue
		}
		if g == string(S3ActionWildcard) || g == string(S3ActionAdmin) {
			return true
		}
		if g == a {
			return true
		}
		if g == "Read" {
			if _, ok := readActions[action]; ok {
				return true
			}
		}
		if g == "Write" {
			if _, ok := writeActions[action]; ok {
				return true
			}
		}
		if strings.HasPrefix(g, a+":") {
			pattern := strings.TrimSpace(g[len(a)+1:])
			if matchBucketObjectPattern(pattern, bucket, object) {
				return true
			}
		}
	}
	return false
}

func matchBucketObjectPattern(pattern, bucket, object string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	target := bucket
	if object != "" {
		target += "/" + object
	}
	ok, err := path.Match(pattern, target)
	if err == nil && ok {
		return true
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		if bucket == prefix || strings.HasPrefix(target, prefix+"/") {
			return true
		}
	}
	return false
}

// LoadFromFiler reloads config from filer KV.
func (iam *IdentityAccessManagement) LoadFromFiler(ctx context.Context) error {
	if iam.filerClient == nil {
		return nil
	}
	data, err := iam.filerClient.KvGet(ctx, IAMConfigKey)
	if err != nil {
		if errors.Is(err, filer.ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return iam.Reload(&iampb.S3ApiConfiguration{})
		}
		return fmt.Errorf("kv get iam config: %w", err)
	}
	if len(data) == 0 {
		return iam.Reload(&iampb.S3ApiConfiguration{})
	}

	cfg := &iampb.S3ApiConfiguration{}
	if err := protojson.Unmarshal(data, cfg); err != nil {
		// Backward-compatible fallback in case data was stored as protobuf binary.
		if err2 := proto.Unmarshal(data, cfg); err2 != nil {
			return fmt.Errorf("decode iam config: %w", err)
		}
	}
	return iam.Reload(cfg)
}

// SaveToFiler persists current config to filer KV.
func (iam *IdentityAccessManagement) SaveToFiler(ctx context.Context) error {
	if iam.filerClient == nil {
		return nil
	}
	cfg := iam.GetConfiguration()
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal iam config: %w", err)
	}
	if err := iam.filerClient.KvPut(ctx, IAMConfigKey, data); err != nil {
		return fmt.Errorf("kv put iam config: %w", err)
	}
	return nil
}

// Reload atomically swaps in-memory identities and HMAC pools.
func (iam *IdentityAccessManagement) Reload(cfg *iampb.S3ApiConfiguration) error {
	if err := ValidateConfiguration(cfg); err != nil {
		return err
	}
	if cfg == nil {
		cfg = &iampb.S3ApiConfiguration{}
	}

	identities := make([]*Identity, 0, len(cfg.GetIdentities()))
	hashes := make(map[string]*sync.Pool)

	for _, pbIdentity := range cfg.GetIdentities() {
		if pbIdentity == nil {
			continue
		}
		identity := &Identity{
			Name:        pbIdentity.GetName(),
			Credentials: make([]*Credential, 0, len(pbIdentity.GetCredentials())),
			Actions:     append([]string(nil), pbIdentity.GetActions()...),
		}
		for _, pbCred := range pbIdentity.GetCredentials() {
			if pbCred == nil {
				continue
			}
			cred := &Credential{AccessKey: pbCred.GetAccessKey(), SecretKey: pbCred.GetSecretKey()}
			identity.Credentials = append(identity.Credentials, cred)

			secret := []byte(cred.SecretKey)
			hashes[cred.AccessKey] = &sync.Pool{
				New: func() any {
					return hmac.New(sha256.New, secret)
				},
			}
		}
		identities = append(identities, identity)
	}

	iam.mu.Lock()
	iam.identities = identities
	iam.mu.Unlock()

	iam.hashesMu.Lock()
	iam.hashes = hashes
	iam.hashesMu.Unlock()

	iam.logger.Info("reloaded iam configuration", "identities", len(identities))
	return nil
}

// GetConfiguration returns a deep-copied proto snapshot.
func (iam *IdentityAccessManagement) GetConfiguration() *iampb.S3ApiConfiguration {
	iam.mu.RLock()
	defer iam.mu.RUnlock()

	cfg := &iampb.S3ApiConfiguration{Identities: make([]*iampb.Identity, 0, len(iam.identities))}
	for _, identity := range iam.identities {
		if identity == nil {
			continue
		}
		pbIdentity := &iampb.Identity{
			Name:        identity.Name,
			Actions:     append([]string(nil), identity.Actions...),
			Credentials: make([]*iampb.Credential, 0, len(identity.Credentials)),
		}
		for _, cred := range identity.Credentials {
			if cred == nil {
				continue
			}
			pbIdentity.Credentials = append(pbIdentity.Credentials, &iampb.Credential{
				AccessKey: cred.AccessKey,
				SecretKey: cred.SecretKey,
			})
		}
		cfg.Identities = append(cfg.Identities, pbIdentity)
	}
	return cfg
}

// GetHmacPool returns the pool for a given access key.
func (iam *IdentityAccessManagement) GetHmacPool(accessKey string) *sync.Pool {
	iam.hashesMu.RLock()
	defer iam.hashesMu.RUnlock()
	return iam.hashes[accessKey]
}

// DeriveSigningKey derives the SigV4 signing key.
func DeriveSigningKey(secretKey, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}
