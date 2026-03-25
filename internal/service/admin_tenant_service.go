package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	openpayv1 "github.com/menesesghz/openpay-smart-service/gen/openpay/v1"
	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/repository"
	"github.com/menesesghz/openpay-smart-service/internal/storage"
)

// AdminTenantService implements openpayv1.AdminTenantServiceServer.
// All methods require the static admin API key validated by AdminAuthInterceptor.
type AdminTenantService struct {
	openpayv1.UnimplementedAdminTenantServiceServer

	tenants repository.TenantRepository
	storage storage.Storage // may be nil if S3 is not configured
	log     zerolog.Logger
}

// NewAdminTenantService constructs an AdminTenantService.
// store may be nil — when nil, logo upload endpoints return Unimplemented.
func NewAdminTenantService(
	tenants repository.TenantRepository,
	store storage.Storage,
	log zerolog.Logger,
) *AdminTenantService {
	return &AdminTenantService{
		tenants: tenants,
		storage: store,
		log:     log.With().Str("service", "admin_tenant").Logger(),
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// feeTypeFromProto converts a proto FeeType to the domain type.
// FEE_TYPE_UNSPECIFIED and FEE_TYPE_ADDED both resolve to FeeTypeAdded.
func feeTypeFromProto(ft openpayv1.FeeType) domain.FeeType {
	if ft == openpayv1.FeeType_FEE_TYPE_INCLUSIVE {
		return domain.FeeTypeInclusive
	}
	return domain.FeeTypeAdded
}

// feeTypeToProto converts the domain FeeType to proto.
func feeTypeToProto(ft domain.FeeType) openpayv1.FeeType {
	if ft == domain.FeeTypeInclusive {
		return openpayv1.FeeType_FEE_TYPE_INCLUSIVE
	}
	return openpayv1.FeeType_FEE_TYPE_ADDED
}

// validateFee returns an error when neither fee component is greater than 0.
func validateFee(f domain.PlatformFeeConfig) error {
	if f.PercentageBPS == 0 && f.FixedCentavos == 0 {
		return status.Error(codes.InvalidArgument, "platform_fee: at least one of percentage_bps or fixed_centavos must be > 0")
	}
	if f.PercentageBPS < 0 {
		return status.Error(codes.InvalidArgument, "platform_fee.percentage_bps must be >= 0")
	}
	if f.FixedCentavos < 0 {
		return status.Error(codes.InvalidArgument, "platform_fee.fixed_centavos must be >= 0")
	}
	return nil
}

// feeFromProto converts a proto Fee message to the domain struct.
// Returns the default fee (150 BPS, 0 fixed) when msg is nil.
func feeFromProto(msg *openpayv1.Fee) domain.PlatformFeeConfig {
	if msg == nil {
		return domain.PlatformFeeConfig{PercentageBPS: 150} // default 1.5%
	}
	return domain.PlatformFeeConfig{
		PercentageBPS: int(msg.PercentageBps),
		FixedCentavos: msg.FixedCentavos,
	}
}

// feeToProto converts the domain PlatformFeeConfig to a proto Fee message.
func feeToProto(f domain.PlatformFeeConfig) *openpayv1.Fee {
	return &openpayv1.Fee{
		PercentageBps: int32(f.PercentageBPS),
		FixedCentavos: f.FixedCentavos,
	}
}

// ── CreateTenant ──────────────────────────────────────────────────────────────

// CreateTenant provisions a new tenant and returns the raw API key once.
func (s *AdminTenantService) CreateTenant(ctx context.Context, req *openpayv1.CreateTenantRequest) (*openpayv1.CreateTenantResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	tier := req.Tier
	if tier == "" {
		tier = "standard"
	}
	if tier != "free" && tier != "standard" && tier != "enterprise" {
		return nil, status.Error(codes.InvalidArgument, "tier must be 'free', 'standard', or 'enterprise'")
	}

	platformFee := feeFromProto(req.PlatformFee)
	if err := validateFee(platformFee); err != nil {
		return nil, err
	}
	feeType := feeTypeFromProto(req.FeeType)

	rawKey, keyHash, keyPrefix, err := generateAPIKey()
	if err != nil {
		s.log.Error().Err(err).Msg("failed to generate API key")
		return nil, status.Error(codes.Internal, "failed to generate API key")
	}

	now := time.Now().UTC()
	t := &domain.Tenant{
		ID:                  uuid.New(),
		Name:                req.Name,
		APIKeyHash:          keyHash,
		APIKeyPrefix:        keyPrefix,
		Tier:                tier,
		PlatformFee:         platformFee,
		FeeType:             feeType,
		CardNetworksEnabled: req.CardNetworksEnabled,
		CardNetworkList:     req.CardNetworkList,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if err := s.tenants.Create(ctx, t); err != nil {
		s.log.Error().Err(err).Str("name", req.Name).Msg("create tenant failed")
		return nil, status.Error(codes.Internal, "create tenant failed")
	}

	s.log.Info().
		Str("tenant_id", t.ID.String()).
		Str("name", t.Name).
		Str("tier", t.Tier).
		Msg("tenant created")

	return &openpayv1.CreateTenantResponse{
		Tenant: domainTenantToProto(t),
		ApiKey: rawKey,
	}, nil
}

// ── GetTenant ─────────────────────────────────────────────────────────────────

func (s *AdminTenantService) GetTenant(ctx context.Context, req *openpayv1.GetTenantRequest) (*openpayv1.GetTenantResponse, error) {
	id, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id")
	}

	t, err := s.tenants.GetByID(ctx, id)
	if err != nil {
		return nil, domainErrToStatus(err)
	}

	return &openpayv1.GetTenantResponse{Tenant: domainTenantToProto(t)}, nil
}

// ── ListTenants ───────────────────────────────────────────────────────────────

func (s *AdminTenantService) ListTenants(ctx context.Context, req *openpayv1.ListTenantsRequest) (*openpayv1.ListTenantsResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	tenants, nextToken, err := s.tenants.List(ctx, repository.ListTenantsOptions{
		Tier:      req.Tier,
		PageSize:  pageSize,
		PageToken: req.PageToken,
	})
	if err != nil {
		s.log.Error().Err(err).Msg("list tenants failed")
		return nil, status.Error(codes.Internal, "list tenants failed")
	}

	out := make([]*openpayv1.AdminTenant, len(tenants))
	for i, t := range tenants {
		out[i] = domainTenantToProto(t)
	}

	return &openpayv1.ListTenantsResponse{
		Tenants:  out,
		PageInfo: &openpayv1.PageInfo{NextPageToken: nextToken},
	}, nil
}

// ── UpdateTenant ──────────────────────────────────────────────────────────────

// UpdateTenant applies partial updates: only non-zero / non-empty fields are changed.
// platform_fee_bps is ignored when set to -1.
func (s *AdminTenantService) UpdateTenant(ctx context.Context, req *openpayv1.UpdateTenantRequest) (*openpayv1.UpdateTenantResponse, error) {
	id, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id")
	}

	t, err := s.tenants.GetByID(ctx, id)
	if err != nil {
		return nil, domainErrToStatus(err)
	}

	if req.Name != "" {
		t.Name = req.Name
	}
	if req.Tier != "" {
		if req.Tier != "free" && req.Tier != "standard" && req.Tier != "enterprise" {
			return nil, status.Error(codes.InvalidArgument, "tier must be 'free', 'standard', or 'enterprise'")
		}
		t.Tier = req.Tier
	}
	if req.PlatformFee != nil {
		newFee := feeFromProto(req.PlatformFee)
		if err := validateFee(newFee); err != nil {
			return nil, err
		}
		t.PlatformFee = newFee
	}
	if req.FeeType != openpayv1.FeeType_FEE_TYPE_UNSPECIFIED {
		t.FeeType = feeTypeFromProto(req.FeeType)
	}
	if req.CardNetworksEnabled != nil {
		t.CardNetworksEnabled = req.GetCardNetworksEnabled()
	}
	if req.CardNetworkList != nil {
		t.CardNetworkList = req.CardNetworkList
	}

	if err := s.tenants.Update(ctx, t); err != nil {
		return nil, domainErrToStatus(err)
	}

	// Re-fetch to get updated_at from the DB.
	updated, err := s.tenants.GetByID(ctx, id)
	if err != nil {
		return nil, domainErrToStatus(err)
	}

	return &openpayv1.UpdateTenantResponse{Tenant: domainTenantToProto(updated)}, nil
}

// ── DeleteTenant ──────────────────────────────────────────────────────────────

// DeleteTenant soft-deletes the tenant. The API key stops working immediately.
func (s *AdminTenantService) DeleteTenant(ctx context.Context, req *openpayv1.DeleteTenantRequest) (*openpayv1.DeleteTenantResponse, error) {
	id, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id")
	}

	if err := s.tenants.Delete(ctx, id); err != nil {
		return nil, domainErrToStatus(err)
	}

	s.log.Info().Str("tenant_id", id.String()).Msg("tenant soft-deleted")

	return &openpayv1.DeleteTenantResponse{Success: true}, nil
}

// ── RotateAPIKey ──────────────────────────────────────────────────────────────

// RotateAPIKey issues a new API key for the tenant. The previous key is
// invalidated immediately. Returns the new raw key once.
func (s *AdminTenantService) RotateAPIKey(ctx context.Context, req *openpayv1.RotateAPIKeyRequest) (*openpayv1.RotateAPIKeyResponse, error) {
	id, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id")
	}

	// Verify the tenant exists and is active before generating a new key.
	if _, err := s.tenants.GetByID(ctx, id); err != nil {
		return nil, domainErrToStatus(err)
	}

	rawKey, keyHash, keyPrefix, err := generateAPIKey()
	if err != nil {
		s.log.Error().Err(err).Str("tenant_id", id.String()).Msg("failed to generate new API key")
		return nil, status.Error(codes.Internal, "failed to generate API key")
	}

	if err := s.tenants.RotateAPIKey(ctx, id, keyHash, keyPrefix); err != nil {
		return nil, domainErrToStatus(err)
	}

	s.log.Info().Str("tenant_id", id.String()).Msg("API key rotated")

	return &openpayv1.RotateAPIKeyResponse{
		ApiKey:       rawKey,
		ApiKeyPrefix: keyPrefix,
	}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// generateAPIKey creates a cryptographically random tenant API key.
// Returns (rawKey, sha256Hash, first12Chars, error).
// Format: "opk_" + 64 hex chars (32 random bytes).
func generateAPIKey() (raw, hash, prefix string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	raw = "opk_" + hex.EncodeToString(b)

	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])

	if len(raw) >= 12 {
		prefix = raw[:12]
	} else {
		prefix = raw
	}
	return
}

// domainTenantToProto converts a domain.Tenant to the AdminTenant proto message.
func domainTenantToProto(t *domain.Tenant) *openpayv1.AdminTenant {
	return &openpayv1.AdminTenant{
		TenantId:            t.ID.String(),
		Name:                t.Name,
		Tier:                t.Tier,
		ApiKeyPrefix:        t.APIKeyPrefix,
		LogoUrl:             t.LogoURL,
		PlatformFee:         feeToProto(t.PlatformFee),
		FeeType:             feeTypeToProto(t.FeeType),
		CardNetworksEnabled: t.CardNetworksEnabled,
		CardNetworkList:     t.CardNetworkList,
		CreatedAt:           timestamppb.New(t.CreatedAt),
		UpdatedAt:           timestamppb.New(t.UpdatedAt),
	}
}
