package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	openpayv1 "github.com/menesesghz/openpay-smart-service/gen/openpay/v1"
	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/middleware"
	"github.com/menesesghz/openpay-smart-service/internal/repository"
)

// TenantService implements openpayv1.TenantServiceServer.
// It handles bank account registration and disbursement schedule management
// for authenticated tenants.
type TenantService struct {
	openpayv1.UnimplementedTenantServiceServer

	tenantRepo repository.TenantRepository
	log        zerolog.Logger
}

func NewTenantService(tenantRepo repository.TenantRepository, log zerolog.Logger) *TenantService {
	return &TenantService{
		tenantRepo: tenantRepo,
		log:        log.With().Str("service", "tenant").Logger(),
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func tenantFromCtx(ctx context.Context) (*domain.Tenant, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}
	return tc.Tenant, nil
}

// maskCLABE returns "**************XXXX" where XXXX = last 4 chars of the CLABE.
func maskCLABE(clabe string) string {
	if len(clabe) < 4 {
		return strings.Repeat("*", len(clabe))
	}
	return strings.Repeat("*", len(clabe)-4) + clabe[len(clabe)-4:]
}

// freqToCron maps a DisbursementFrequency enum to a sensible default cron expression.
// All times are UTC; 16:00 UTC is before the 17:00 SPEI cutoff.
func freqToCron(f openpayv1.DisbursementFrequency) string {
	switch f {
	case openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_DAILY:
		return "0 16 * * *"
	case openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_WEEKLY:
		return "0 16 * * 1" // Mondays
	case openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_MONTHLY:
		return "0 16 1 * *" // 1st of each month
	default:
		return ""
	}
}

// freqToString converts the enum to the DB string representation.
func freqToString(f openpayv1.DisbursementFrequency) string {
	switch f {
	case openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_DAILY:
		return "daily"
	case openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_WEEKLY:
		return "weekly"
	case openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_MONTHLY:
		return "monthly"
	case openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_CUSTOM:
		return "custom"
	default:
		return "daily"
	}
}

// nextRunFromCron computes a simple next-run time (now + period) given a frequency.
// A real implementation would use a cron parser; this is good enough for most cases.
func nextRunFromFreq(f openpayv1.DisbursementFrequency) time.Time {
	now := time.Now().UTC()
	switch f {
	case openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_WEEKLY:
		return now.Add(7 * 24 * time.Hour)
	case openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_MONTHLY:
		return now.AddDate(0, 1, 0)
	default: // daily
		return now.Add(24 * time.Hour)
	}
}

func scheduleToProto(s *domain.DisbursementSchedule) *openpayv1.DisbursementSchedule {
	p := &openpayv1.DisbursementSchedule{
		CronExpr: s.CronExpr,
		Enabled:  s.Enabled,
	}
	switch s.Frequency {
	case "weekly":
		p.Frequency = openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_WEEKLY
	case "monthly":
		p.Frequency = openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_MONTHLY
	case "custom":
		p.Frequency = openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_CUSTOM
	default:
		p.Frequency = openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_DAILY
	}
	if !s.NextRunAt.IsZero() {
		p.NextRunAt = timestamppb.New(s.NextRunAt)
	}
	if !s.LastRunAt.IsZero() {
		p.LastRunAt = timestamppb.New(s.LastRunAt)
	}
	return p
}

// ── SetupBankAccount ──────────────────────────────────────────────────────────

func (s *TenantService) SetupBankAccount(ctx context.Context, req *openpayv1.SetupBankAccountRequest) (*openpayv1.SetupBankAccountResponse, error) {
	tenant, err := tenantFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	if len(req.Clabe) != 18 {
		return nil, status.Error(codes.InvalidArgument, "clabe must be exactly 18 digits")
	}
	if req.HolderName == "" {
		return nil, status.Error(codes.InvalidArgument, "holder_name is required")
	}

	if err := s.tenantRepo.SetBankAccount(ctx, tenant.ID, req.Clabe, req.HolderName, req.BankName); err != nil {
		s.log.Error().Err(err).Str("tenant_id", tenant.ID.String()).Msg("set bank account failed")
		return nil, status.Error(codes.Internal, "failed to save bank account")
	}

	s.log.Info().Str("tenant_id", tenant.ID.String()).Msg("bank account registered")

	return &openpayv1.SetupBankAccountResponse{
		BankAccount: &openpayv1.TenantBankAccount{
			Clabe:       req.Clabe,
			ClabeMasked: maskCLABE(req.Clabe),
			HolderName:  req.HolderName,
			BankName:    req.BankName,
			RegisteredAt: timestamppb.Now(),
		},
	}, nil
}

// ── GetBankAccount ────────────────────────────────────────────────────────────

func (s *TenantService) GetBankAccount(ctx context.Context, _ *openpayv1.GetBankAccountRequest) (*openpayv1.GetBankAccountResponse, error) {
	tenant, err := tenantFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	// Re-fetch to get the latest persisted state (including the stored CLABE).
	t, err := s.tenantRepo.GetByID(ctx, tenant.ID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "tenant not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch tenant")
	}

	if !t.HasBankAccount() {
		return nil, status.Error(codes.NotFound, "no bank account registered")
	}

	return &openpayv1.GetBankAccountResponse{
		BankAccount: &openpayv1.TenantBankAccount{
			ClabeMasked: maskCLABE(t.BankCLABE),
			HolderName:  t.BankHolderName,
			BankName:    t.BankName,
			RegisteredAt: timestamppb.New(t.UpdatedAt),
		},
	}, nil
}

// ── DeleteBankAccount ─────────────────────────────────────────────────────────

func (s *TenantService) DeleteBankAccount(ctx context.Context, _ *openpayv1.DeleteBankAccountRequest) (*openpayv1.DeleteBankAccountResponse, error) {
	tenant, err := tenantFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.tenantRepo.ClearBankAccount(ctx, tenant.ID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "tenant not found")
		}
		s.log.Error().Err(err).Str("tenant_id", tenant.ID.String()).Msg("clear bank account failed")
		return nil, status.Error(codes.Internal, "failed to remove bank account")
	}

	s.log.Info().Str("tenant_id", tenant.ID.String()).Msg("bank account removed")
	return &openpayv1.DeleteBankAccountResponse{Success: true}, nil
}

// ── SetDisbursementFrequency ──────────────────────────────────────────────────

func (s *TenantService) SetDisbursementFrequency(ctx context.Context, req *openpayv1.SetDisbursementFrequencyRequest) (*openpayv1.SetDisbursementFrequencyResponse, error) {
	tenant, err := tenantFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	freq := req.Frequency
	cronExpr := freqToCron(freq)

	if freq == openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_CUSTOM {
		if req.CronExpr == "" {
			return nil, status.Error(codes.InvalidArgument, "cron_expr is required when frequency is CUSTOM")
		}
		cronExpr = req.CronExpr
	}

	nextRun := nextRunFromFreq(freq)

	schedule := &domain.DisbursementSchedule{
		TenantID:  tenant.ID,
		Frequency: freqToString(freq),
		CronExpr:  cronExpr,
		Enabled:   req.Enabled,
		NextRunAt: nextRun,
	}

	if err := s.tenantRepo.UpsertSchedule(ctx, schedule); err != nil {
		s.log.Error().Err(err).Str("tenant_id", tenant.ID.String()).Msg("upsert schedule failed")
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update disbursement schedule: %v", err))
	}

	s.log.Info().Str("tenant_id", tenant.ID.String()).Str("frequency", schedule.Frequency).Msg("disbursement schedule updated")

	return &openpayv1.SetDisbursementFrequencyResponse{
		Schedule: scheduleToProto(schedule),
	}, nil
}

// ── GetDisbursementSchedule ───────────────────────────────────────────────────

func (s *TenantService) GetDisbursementSchedule(ctx context.Context, _ *openpayv1.GetDisbursementScheduleRequest) (*openpayv1.GetDisbursementScheduleResponse, error) {
	tenant, err := tenantFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	schedule, err := s.tenantRepo.GetSchedule(ctx, tenant.ID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Return a sensible default — daily, enabled.
			return &openpayv1.GetDisbursementScheduleResponse{
				Schedule: &openpayv1.DisbursementSchedule{
					Frequency: openpayv1.DisbursementFrequency_DISBURSEMENT_FREQUENCY_DAILY,
					CronExpr:  "0 16 * * *",
					Enabled:   false,
				},
			}, nil
		}
		return nil, status.Error(codes.Internal, "failed to fetch disbursement schedule")
	}

	return &openpayv1.GetDisbursementScheduleResponse{
		Schedule: scheduleToProto(schedule),
	}, nil
}

// ── TriggerManualDisbursement ─────────────────────────────────────────────────

func (s *TenantService) TriggerManualDisbursement(_ context.Context, _ *openpayv1.TriggerManualDisbursementRequest) (*openpayv1.TriggerManualDisbursementResponse, error) {
	// Full implementation requires calling OpenPay's POST /payouts endpoint.
	// This will be wired up in the DisbursementScheduler worker.
	return nil, status.Error(codes.Unimplemented, "manual disbursement not yet available — contact support to trigger a payout")
}
