package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	openpayv1 "github.com/menesesghz/openpay-smart-service/gen/openpay/v1"
	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/middleware"
	"github.com/menesesghz/openpay-smart-service/internal/repository"
)

// BalanceService implements openpayv1.BalanceServiceServer.
type BalanceService struct {
	openpayv1.UnimplementedBalanceServiceServer

	balanceRepo repository.BalanceRepository
	log         zerolog.Logger
}

func NewBalanceService(balanceRepo repository.BalanceRepository, log zerolog.Logger) *BalanceService {
	return &BalanceService{
		balanceRepo: balanceRepo,
		log:         log.With().Str("service", "balance").Logger(),
	}
}

// ── converters ────────────────────────────────────────────────────────────────

func balanceToProto(b *domain.Balance) *openpayv1.Balance {
	return &openpayv1.Balance{
		Id:       b.ID.String(),
		TenantId: b.TenantID.String(),
		MemberId: func() string {
			if b.MemberID != nil {
				return b.MemberID.String()
			}
			return ""
		}(),
		Available: &openpayv1.Money{Amount: b.Available, Currency: openpayv1.Currency_CURRENCY_MXN},
		Pending:   &openpayv1.Money{Amount: b.Pending, Currency: openpayv1.Currency_CURRENCY_MXN},
		UpdatedAt: timestamppb.New(b.UpdatedAt),
	}
}

func currencyStr(c openpayv1.Currency) string {
	if c == openpayv1.Currency_CURRENCY_UNSPECIFIED {
		return "MXN"
	}
	s := c.String()
	if len(s) > 9 { // "CURRENCY_XXX"
		return s[9:]
	}
	return "MXN"
}

// ── GetTenantBalance ──────────────────────────────────────────────────────────

func (s *BalanceService) GetTenantBalance(ctx context.Context, req *openpayv1.GetTenantBalanceRequest) (*openpayv1.GetTenantBalanceResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	b, err := s.balanceRepo.GetTenantBalance(ctx, tc.Tenant.ID, currencyStr(req.Currency))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// No balance record yet — return zeroed balance.
			return &openpayv1.GetTenantBalanceResponse{
				Balance: &openpayv1.Balance{
					TenantId:  tc.Tenant.ID.String(),
					Available: &openpayv1.Money{Amount: 0, Currency: openpayv1.Currency_CURRENCY_MXN},
					Pending:   &openpayv1.Money{Amount: 0, Currency: openpayv1.Currency_CURRENCY_MXN},
				},
			}, nil
		}
		return nil, status.Error(codes.Internal, "failed to fetch balance")
	}
	return &openpayv1.GetTenantBalanceResponse{Balance: balanceToProto(b)}, nil
}

// ── GetMemberBalance ──────────────────────────────────────────────────────────

func (s *BalanceService) GetMemberBalance(ctx context.Context, req *openpayv1.GetMemberBalanceRequest) (*openpayv1.GetMemberBalanceResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	memberID, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}

	b, err := s.balanceRepo.GetMemberBalance(ctx, tc.Tenant.ID, memberID, currencyStr(req.Currency))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &openpayv1.GetMemberBalanceResponse{
				Balance: &openpayv1.Balance{
					TenantId:  tc.Tenant.ID.String(),
					MemberId:  memberID.String(),
					Available: &openpayv1.Money{Amount: 0, Currency: openpayv1.Currency_CURRENCY_MXN},
					Pending:   &openpayv1.Money{Amount: 0, Currency: openpayv1.Currency_CURRENCY_MXN},
				},
			}, nil
		}
		return nil, status.Error(codes.Internal, "failed to fetch member balance")
	}
	return &openpayv1.GetMemberBalanceResponse{Balance: balanceToProto(b)}, nil
}

// ── ListBalances ──────────────────────────────────────────────────────────────

func (s *BalanceService) ListBalances(ctx context.Context, req *openpayv1.ListBalancesRequest) (*openpayv1.ListBalancesResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	balances, nextToken, err := s.balanceRepo.List(ctx,
		tc.Tenant.ID,
		currencyStr(req.Currency),
		int(req.PageSize),
		req.PageToken,
	)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list balances")
	}

	out := make([]*openpayv1.Balance, len(balances))
	for i, b := range balances {
		out[i] = balanceToProto(b)
	}
	return &openpayv1.ListBalancesResponse{
		Balances: out,
		PageInfo: &openpayv1.PageInfo{NextPageToken: nextToken},
	}, nil
}

// ── GetBalanceHistory ─────────────────────────────────────────────────────────

func (s *BalanceService) GetBalanceHistory(ctx context.Context, req *openpayv1.GetBalanceHistoryRequest) (*openpayv1.GetBalanceHistoryResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	memberID, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}

	granularity := "day"
	switch req.Granularity {
	case openpayv1.BalanceHistoryGranularity_BALANCE_HISTORY_GRANULARITY_HOUR:
		granularity = "hour"
	case openpayv1.BalanceHistoryGranularity_BALANCE_HISTORY_GRANULARITY_WEEK:
		granularity = "week"
	}

	from := time.Now().AddDate(0, -1, 0) // default: last 30 days
	to := time.Now()
	if req.From != nil {
		from = req.From.AsTime()
	}
	if req.To != nil {
		to = req.To.AsTime()
	}

	snapshots, err := s.balanceRepo.GetHistory(ctx, tc.Tenant.ID, memberID, currencyStr(req.Currency), granularity, from, to)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to fetch balance history")
	}

	out := make([]*openpayv1.BalanceSnapshot, len(snapshots))
	for i, snap := range snapshots {
		out[i] = &openpayv1.BalanceSnapshot{
			At:        timestamppb.New(snap.At),
			Available: &openpayv1.Money{Amount: snap.Available, Currency: openpayv1.Currency_CURRENCY_MXN},
			Pending:   &openpayv1.Money{Amount: snap.Pending, Currency: openpayv1.Currency_CURRENCY_MXN},
		}
	}
	return &openpayv1.GetBalanceHistoryResponse{Snapshots: out}, nil
}
