package lti

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/Tencent/WeKnora/internal/utils"
)

// maxKeysetSize bounds how large a cached JWKS document may be.
const maxKeysetSize = 1 << 20 // 1 MiB

// jwksFetchTimeout bounds a single platform JWKS fetch so a stalled platform
// cannot pin a launch request's goroutine indefinitely.
const jwksFetchTimeout = 8 * time.Second

type keysetResolver struct {
	regs   RegistrationStore
	client *http.Client

	mu    sync.Mutex
	cache map[uint64]keyfunc.Keyfunc
}

// NewKeysetResolver builds a per-registration verification-key resolver that
// caches keyfuncs in memory and refreshes on kid miss / rotation. Platform
// JWKS responses are persisted into the registration row so restarts do not
// require an immediate refetch. When no client is given, a default with an
// explicit timeout is used so a stalled platform cannot pin the request.
func NewKeysetResolver(regs RegistrationStore, client *http.Client) KeysetResolver {
	if client == nil {
		client = &http.Client{Timeout: jwksFetchTimeout}
	}
	return &keysetResolver{regs: regs, client: client, cache: make(map[uint64]keyfunc.Keyfunc)}
}

func (r *keysetResolver) Resolve(ctx context.Context, reg *Registration) (keyfunc.Keyfunc, error) {
	r.mu.Lock()
	kf := r.cache[reg.ID]
	r.mu.Unlock()
	if kf != nil {
		return kf, nil
	}
	return r.build(ctx, reg)
}

func (r *keysetResolver) Refresh(ctx context.Context, reg *Registration) (keyfunc.Keyfunc, error) {
	r.mu.Lock()
	delete(r.cache, reg.ID)
	r.mu.Unlock()
	if reg.JWKSURI != "" {
		if err := r.fetchAndPersist(ctx, reg); err != nil {
			return nil, err
		}
	}
	return r.build(ctx, reg)
}

func (r *keysetResolver) build(ctx context.Context, reg *Registration) (keyfunc.Keyfunc, error) {
	raw := reg.PublicKeyset
	if raw == "" {
		if reg.JWKSURI == "" {
			return nil, ErrRegistrationNoKeyset
		}
		if err := r.fetchAndPersist(ctx, reg); err != nil {
			return nil, err
		}
		raw = reg.PublicKeyset
	}
	storage := jwkset.NewMemoryStorage()
	if err := populateKeyset(ctx, storage, raw); err != nil {
		return nil, err
	}
	kf, err := keyfunc.New(keyfunc.Options{Storage: storage})
	if err != nil {
		return nil, fmt.Errorf("lti: build keyfunc: %w", err)
	}
	r.mu.Lock()
	r.cache[reg.ID] = kf
	r.mu.Unlock()
	return kf, nil
}

// fetchAndPersist downloads and stores the platform's public keyset, replacing
// any stale cached copy.
func (r *keysetResolver) fetchAndPersist(ctx context.Context, reg *Registration) error {
	if err := utils.ValidateURLForSSRF(reg.JWKSURI); err != nil {
		return fmt.Errorf("lti: unsafe jwks_uri: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reg.JWKSURI, nil)
	if err != nil {
		return fmt.Errorf("lti: build jwks request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("lti: fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lti: fetch jwks: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxKeysetSize))
	if err != nil {
		return fmt.Errorf("lti: read jwks: %w", err)
	}
	if err := validateKeysetJSON(body); err != nil {
		return fmt.Errorf("lti: invalid jwks: %w", err)
	}
	now := time.Now()
	if err := r.regs.SaveKeyset(ctx, reg.ID, string(body), now); err != nil {
		return fmt.Errorf("lti: persist jwks: %w", err)
	}
	reg.PublicKeyset = string(body)
	reg.KeysetFetchedAt = &now
	return nil
}

// validateKeysetJSON ensures the fetched document has a keys array before it
// is trusted and cached.
func validateKeysetJSON(body []byte) error {
	var set jwkset.JWKSMarshal
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("cannot decode: %w", err)
	}
	if len(set.Keys) == 0 {
		return errors.New("empty keys array")
	}
	return nil
}

// populateKeyset rehydrates a raw JWKS JSON document into a storage.
func populateKeyset(ctx context.Context, storage jwkset.Storage, raw string) error {
	var set jwkset.JWKSMarshal
	if err := json.Unmarshal([]byte(raw), &set); err != nil {
		return fmt.Errorf("lti: decode cached jwks: %w", err)
	}
	keys := make([]jwkset.JWK, 0, len(set.Keys))
	for _, k := range set.Keys {
		jwk, err := jwkset.NewJWKFromMarshal(k, jwkset.JWKMarshalOptions{}, jwkset.JWKValidateOptions{SkipAll: true})
		if err != nil {
			return fmt.Errorf("lti: decode cached jwks key: %w", err)
		}
		keys = append(keys, jwk)
	}
	if err := storage.KeyReplaceAll(ctx, keys); err != nil {
		return fmt.Errorf("lti: load cached jwks: %w", err)
	}
	return nil
}
