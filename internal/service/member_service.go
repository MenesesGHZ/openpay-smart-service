package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	openpayv1 "github.com/menesesghz/openpay-smart-service/gen/openpay/v1"
	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/middleware"
	"github.com/menesesghz/openpay-smart-service/internal/openpay"
	"github.com/menesesghz/openpay-smart-service/internal/repository"
)

// MemberService implements openpayv1.MemberServiceServer.
type MemberService struct {
	openpayv1.UnimplementedMemberServiceServer

	memberRepo       repository.MemberRepository
	planRepo         repository.PlanRepository
	subscriptionRepo repository.SubscriptionRepository
	paymentRepo      repository.PaymentRepository
	tenantRepo       repository.TenantRepository
	opClient         *openpay.Client
	checkoutBaseURL  string
	log              zerolog.Logger
}

func NewMemberService(
	memberRepo repository.MemberRepository,
	planRepo repository.PlanRepository,
	subscriptionRepo repository.SubscriptionRepository,
	paymentRepo repository.PaymentRepository,
	tenantRepo repository.TenantRepository,
	opClient *openpay.Client,
	checkoutBaseURL string,
	log zerolog.Logger,
) *MemberService {
	return &MemberService{
		memberRepo:       memberRepo,
		planRepo:         planRepo,
		subscriptionRepo: subscriptionRepo,
		paymentRepo:      paymentRepo,
		tenantRepo:       tenantRepo,
		opClient:         opClient,
		checkoutBaseURL:  strings.TrimRight(checkoutBaseURL, "/"),
		log:              log.With().Str("service", "member").Logger(),
	}
}

// ── converters ────────────────────────────────────────────────────────────────

func memberToProto(m *domain.Member) *openpayv1.Member {
	p := &openpayv1.Member{
		Id:                m.ID.String(),
		TenantId:          m.TenantID.String(),
		OpenpayCustomerId: m.OpenpayCustomerID,
		ExternalId:        m.ExternalID,
		Name:              m.Name,
		Email:             m.Email,
		Phone:             m.Phone,
		CreatedAt:         timestamppb.New(m.CreatedAt),
		UpdatedAt:         timestamppb.New(m.UpdatedAt),
	}
	switch m.KYCStatus {
	case domain.KYCStatusVerified:
		p.KycStatus = openpayv1.KYCStatus_KYC_STATUS_VERIFIED
	case domain.KYCStatusRejected:
		p.KycStatus = openpayv1.KYCStatus_KYC_STATUS_REJECTED
	default:
		p.KycStatus = openpayv1.KYCStatus_KYC_STATUS_PENDING
	}
	if m.AddressLine1 != "" || m.AddressCity != "" {
		p.Address = &openpayv1.Address{
			Line1:      m.AddressLine1,
			Line2:      m.AddressLine2,
			City:       m.AddressCity,
			State:      m.AddressState,
			PostalCode: m.AddressPostalCode,
			Country:    m.AddressCountry,
		}
	}
	return p
}

func paymentLinkToProto(l *domain.PaymentLink) *openpayv1.PaymentLink {
	p := &openpayv1.PaymentLink{
		Id:          l.ID.String(),
		TenantId:    l.TenantID.String(),
		MemberId:    l.MemberID.String(),
		Token:       l.Token,
		Description: l.Description,
		Amount: &openpayv1.Money{
			Amount:   l.Amount,
			Currency: openpayv1.Currency_CURRENCY_MXN,
		},
		CreatedAt: timestamppb.New(l.CreatedAt),
	}
	switch l.Status {
	case domain.PaymentLinkStatusRedeemed:
		p.Status = openpayv1.PaymentLinkStatus_PAYMENT_LINK_STATUS_REDEEMED
	case domain.PaymentLinkStatusExpired:
		p.Status = openpayv1.PaymentLinkStatus_PAYMENT_LINK_STATUS_EXPIRED
	case domain.PaymentLinkStatusCancelled:
		p.Status = openpayv1.PaymentLinkStatus_PAYMENT_LINK_STATUS_CANCELLED
	default:
		p.Status = openpayv1.PaymentLinkStatus_PAYMENT_LINK_STATUS_ACTIVE
	}
	if l.ExpiresAt != nil {
		p.ExpiresAt = timestamppb.New(*l.ExpiresAt)
	}
	if l.RedeemedAt != nil {
		p.RedeemedAt = timestamppb.New(*l.RedeemedAt)
	}
	return p
}

func cardToProto(c *domain.MemberCard) *openpayv1.Card {
	return &openpayv1.Card{
		Id:              c.ID.String(),
		MemberId:        c.MemberID.String(),
		Brand:           c.Brand,
		CardType:        c.CardType,
		LastFour:        c.LastFour,
		HolderName:      c.HolderName,
		ExpirationYear:  c.ExpirationYear,
		ExpirationMonth: c.ExpirationMonth,
		BankName:        c.BankName,
		AllowsCharges:   c.AllowsCharges,
		CreatedAt:       timestamppb.New(c.CreatedAt),
	}
}

func subscriptionLinkToProto(l *domain.SubscriptionLink) *openpayv1.SubscriptionLink {
	p := &openpayv1.SubscriptionLink{
		Id:        l.ID.String(),
		MemberId:  l.MemberID.String(),
		PlanId:    l.PlanID.String(),
		Token:     l.Token,
		Status:    l.Status,
		CreatedAt: timestamppb.New(l.CreatedAt),
	}
	if l.SubscriptionID != nil {
		p.SubscriptionId = l.SubscriptionID.String()
	}
	if l.ExpiresAt != nil {
		p.ExpiresAt = timestamppb.New(*l.ExpiresAt)
	}
	return p
}

func generateLinkToken() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ── CreateMember ─────────────────────────────────────────────────────────────

func (s *MemberService) CreateMember(ctx context.Context, req *openpayv1.CreateMemberRequest) (*openpayv1.CreateMemberResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if req.Email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}

	// Idempotency: return existing member if key already used.
	if req.IdempotencyKey != "" {
		// We use external_id as the idempotency anchor when provided.
		if req.ExternalId != "" {
			existing, err := s.memberRepo.GetByExternalID(ctx, tc.Tenant.ID, req.ExternalId)
			if err == nil {
				return &openpayv1.CreateMemberResponse{Member: memberToProto(existing)}, nil
			}
		}
	}

	// Build OpenPay customer request.
	opReq := openpay.CreateCustomerRequest{
		Name:        req.Name,
		Email:       req.Email,
		PhoneNumber: req.Phone,
		ExternalID:  req.ExternalId,
	}
	if req.Address != nil {
		opReq.Address = &openpay.CustomerAddress{
			Line1:       req.Address.Line1,
			Line2:       req.Address.Line2,
			City:        req.Address.City,
			State:       req.Address.State,
			PostalCode:  req.Address.PostalCode,
			CountryCode: req.Address.Country,
		}
	}

	// Create customer in OpenPay first to get the openpay_customer_id.
	opCustomer, err := s.opClient.CreateCustomer(ctx, opReq)
	if err != nil {
		s.log.Error().Err(err).Str("tenant_id", tc.Tenant.ID.String()).Msg("openpay create customer failed")
		return nil, status.Error(codes.Internal, "failed to create customer in OpenPay")
	}

	m := &domain.Member{
		TenantID:          tc.Tenant.ID,
		OpenpayCustomerID: opCustomer.ID,
		ExternalID:        req.ExternalId,
		Name:              req.Name,
		Email:             req.Email,
		Phone:             req.Phone,
		KYCStatus:         domain.KYCStatusPending,
	}
	if req.Address != nil {
		m.AddressLine1 = req.Address.Line1
		m.AddressLine2 = req.Address.Line2
		m.AddressCity = req.Address.City
		m.AddressState = req.Address.State
		m.AddressPostalCode = req.Address.PostalCode
		m.AddressCountry = req.Address.Country
	}

	if err := s.memberRepo.Create(ctx, m); err != nil {
		s.log.Error().Err(err).Str("tenant_id", tc.Tenant.ID.String()).Msg("create member failed")
		return nil, status.Error(codes.Internal, "failed to create member")
	}

	s.log.Info().Str("member_id", m.ID.String()).Str("tenant_id", tc.Tenant.ID.String()).Msg("member created")
	return &openpayv1.CreateMemberResponse{Member: memberToProto(m)}, nil
}

// ── GetMember ─────────────────────────────────────────────────────────────────

func (s *MemberService) GetMember(ctx context.Context, req *openpayv1.GetMemberRequest) (*openpayv1.GetMemberResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	// Support "ext:<external_id>" prefix to look up by caller's own ID.
	if strings.HasPrefix(req.MemberId, "ext:") {
		extID := strings.TrimPrefix(req.MemberId, "ext:")
		m, err := s.memberRepo.GetByExternalID(ctx, tc.Tenant.ID, extID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, status.Error(codes.NotFound, "member not found")
			}
			return nil, status.Error(codes.Internal, "failed to fetch member")
		}
		return &openpayv1.GetMemberResponse{Member: memberToProto(m)}, nil
	}

	id, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}

	m, err := s.memberRepo.GetByID(ctx, tc.Tenant.ID, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "member not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch member")
	}
	return &openpayv1.GetMemberResponse{Member: memberToProto(m)}, nil
}

// ── UpdateMember ─────────────────────────────────────────────────────────────

func (s *MemberService) UpdateMember(ctx context.Context, req *openpayv1.UpdateMemberRequest) (*openpayv1.UpdateMemberResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	id, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}

	m, err := s.memberRepo.GetByID(ctx, tc.Tenant.ID, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "member not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch member")
	}

	if req.Name != "" {
		m.Name = req.Name
	}
	if req.Email != "" {
		m.Email = req.Email
	}
	if req.Phone != "" {
		m.Phone = req.Phone
	}
	if req.Address != nil {
		m.AddressLine1 = req.Address.Line1
		m.AddressLine2 = req.Address.Line2
		m.AddressCity = req.Address.City
		m.AddressState = req.Address.State
		m.AddressPostalCode = req.Address.PostalCode
		m.AddressCountry = req.Address.Country
	}

	if err := s.memberRepo.Update(ctx, m); err != nil {
		return nil, status.Error(codes.Internal, "failed to update member")
	}
	return &openpayv1.UpdateMemberResponse{Member: memberToProto(m)}, nil
}

// ── ListMembers ───────────────────────────────────────────────────────────────

func (s *MemberService) ListMembers(ctx context.Context, req *openpayv1.ListMembersRequest) (*openpayv1.ListMembersResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	opts := repository.ListMembersOptions{
		TenantID:  tc.Tenant.ID,
		Email:     req.Email,
		PageSize:  int(req.PageSize),
		PageToken: req.PageToken,
	}
	if req.CreatedAfter != nil {
		t := req.CreatedAfter.AsTime()
		opts.CreatedAfter = &t
	}
	if req.CreatedBefore != nil {
		t := req.CreatedBefore.AsTime()
		opts.CreatedBefore = &t
	}
	switch req.KycStatus {
	case openpayv1.KYCStatus_KYC_STATUS_VERIFIED:
		opts.KYCStatus = string(domain.KYCStatusVerified)
	case openpayv1.KYCStatus_KYC_STATUS_REJECTED:
		opts.KYCStatus = string(domain.KYCStatusRejected)
	case openpayv1.KYCStatus_KYC_STATUS_PENDING:
		opts.KYCStatus = string(domain.KYCStatusPending)
	}

	members, nextToken, err := s.memberRepo.List(ctx, opts)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list members")
	}

	out := make([]*openpayv1.Member, len(members))
	for i, m := range members {
		out[i] = memberToProto(m)
	}
	return &openpayv1.ListMembersResponse{
		Members:  out,
		PageInfo: &openpayv1.PageInfo{NextPageToken: nextToken},
	}, nil
}

// ── CreatePaymentLink ─────────────────────────────────────────────────────────

func (s *MemberService) CreatePaymentLink(ctx context.Context, req *openpayv1.CreatePaymentLinkRequest) (*openpayv1.CreatePaymentLinkResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	memberID, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}
	if req.Amount == nil || req.Amount.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be greater than 0")
	}

	token, err := generateLinkToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate link token")
	}

	currency := "MXN"
	if req.Amount.Currency != openpayv1.Currency_CURRENCY_UNSPECIFIED {
		currency = req.Amount.Currency.String()[len("CURRENCY_"):]
	}

	link := &domain.PaymentLink{
		TenantID:    tc.Tenant.ID,
		MemberID:    memberID,
		Token:       token,
		Amount:      req.Amount.Amount,
		Currency:    currency,
		Description: req.Description,
		OrderID:     req.OrderId,
		Status:      domain.PaymentLinkStatusActive,
	}
	if req.ExpiresInSecs > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresInSecs) * time.Second)
		link.ExpiresAt = &t
	}

	if err := s.memberRepo.CreatePaymentLink(ctx, link); err != nil {
		s.log.Error().Err(err).Str("member_id", memberID.String()).Msg("create payment link failed")
		return nil, status.Error(codes.Internal, "failed to create payment link")
	}

	checkoutURL := s.checkoutBaseURL + "/l/" + token
	return &openpayv1.CreatePaymentLinkResponse{
		Link:        paymentLinkToProto(link),
		CheckoutUrl: checkoutURL,
	}, nil
}

// ── GetPaymentLink ────────────────────────────────────────────────────────────

func (s *MemberService) GetPaymentLink(ctx context.Context, req *openpayv1.GetPaymentLinkRequest) (*openpayv1.GetPaymentLinkResponse, error) {
	link, err := s.memberRepo.GetPaymentLinkByToken(ctx, req.Token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "payment link not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch payment link")
	}
	return &openpayv1.GetPaymentLinkResponse{Link: paymentLinkToProto(link)}, nil
}

// ── ListPaymentLinks ──────────────────────────────────────────────────────────

func (s *MemberService) ListPaymentLinks(ctx context.Context, req *openpayv1.ListPaymentLinksRequest) (*openpayv1.ListPaymentLinksResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	memberID, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}

	opts := repository.ListPaymentLinksOptions{
		TenantID:  tc.Tenant.ID,
		MemberID:  &memberID,
		PageSize:  int(req.PageSize),
		PageToken: req.PageToken,
	}
	switch req.Status {
	case openpayv1.PaymentLinkStatus_PAYMENT_LINK_STATUS_ACTIVE:
		opts.Status = string(domain.PaymentLinkStatusActive)
	case openpayv1.PaymentLinkStatus_PAYMENT_LINK_STATUS_REDEEMED:
		opts.Status = string(domain.PaymentLinkStatusRedeemed)
	case openpayv1.PaymentLinkStatus_PAYMENT_LINK_STATUS_EXPIRED:
		opts.Status = string(domain.PaymentLinkStatusExpired)
	}

	links, nextToken, err := s.memberRepo.ListPaymentLinks(ctx, opts)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list payment links")
	}

	out := make([]*openpayv1.PaymentLink, len(links))
	for i, l := range links {
		out[i] = paymentLinkToProto(l)
	}
	return &openpayv1.ListPaymentLinksResponse{
		Links:    out,
		PageInfo: &openpayv1.PageInfo{NextPageToken: nextToken},
	}, nil
}

// ── ExpirePaymentLink ─────────────────────────────────────────────────────────

func (s *MemberService) ExpirePaymentLink(ctx context.Context, req *openpayv1.ExpirePaymentLinkRequest) (*openpayv1.ExpirePaymentLinkResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	linkID, err := uuid.Parse(req.LinkId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid link_id")
	}

	link, err := s.memberRepo.GetPaymentLinkByID(ctx, tc.Tenant.ID, linkID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "payment link not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch payment link")
	}

	if link.Status != domain.PaymentLinkStatusActive {
		return nil, status.Errorf(codes.FailedPrecondition, "link is already %s", link.Status)
	}

	link.Status = domain.PaymentLinkStatusCancelled
	if err := s.memberRepo.UpdatePaymentLink(ctx, link); err != nil {
		return nil, status.Error(codes.Internal, "failed to expire payment link")
	}
	return &openpayv1.ExpirePaymentLinkResponse{Link: paymentLinkToProto(link)}, nil
}

// ── RegisterCard ──────────────────────────────────────────────────────────────

func (s *MemberService) RegisterCard(ctx context.Context, req *openpayv1.RegisterCardRequest) (*openpayv1.RegisterCardResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	memberID, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}
	if req.TokenId == "" {
		return nil, status.Error(codes.InvalidArgument, "token_id is required")
	}
	if req.DeviceSessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "device_session_id is required")
	}

	member, err := s.memberRepo.GetByID(ctx, tc.Tenant.ID, memberID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "member not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch member")
	}
	if member.OpenpayCustomerID == "" {
		return nil, status.Error(codes.FailedPrecondition, "member has no OpenPay customer ID")
	}

	opCard, err := s.opClient.CreateCard(ctx, member.OpenpayCustomerID, openpay.CreateCardRequest{
		TokenID:         req.TokenId,
		DeviceSessionID: req.DeviceSessionId,
	})
	if err != nil {
		s.log.Error().Err(err).Str("member_id", memberID.String()).Msg("openpay create card failed")
		return nil, status.Errorf(codes.Internal, "create card on OpenPay: %v", err)
	}

	card := &domain.MemberCard{
		TenantID:        tc.Tenant.ID,
		MemberID:        memberID,
		OpenpayCardID:   opCard.ID,
		CardType:        opCard.Type,
		Brand:           opCard.Brand,
		LastFour:        opCard.CardNumber,
		HolderName:      opCard.HolderName,
		ExpirationYear:  opCard.ExpirationYear,
		ExpirationMonth: opCard.ExpirationMonth,
		BankName:        opCard.BankName,
		AllowsCharges:   opCard.AllowsCharges,
	}
	if err := s.memberRepo.CreateCard(ctx, card); err != nil {
		s.log.Error().Err(err).Str("member_id", memberID.String()).Msg("persist card failed")
		return nil, status.Error(codes.Internal, "failed to persist card")
	}

	s.log.Info().Str("member_id", memberID.String()).Str("card_id", card.ID.String()).Msg("card registered")
	return &openpayv1.RegisterCardResponse{Card: cardToProto(card)}, nil
}

// ── ListCards ─────────────────────────────────────────────────────────────────

func (s *MemberService) ListCards(ctx context.Context, req *openpayv1.ListCardsRequest) (*openpayv1.ListCardsResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	memberID, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}

	cards, err := s.memberRepo.ListCards(ctx, tc.Tenant.ID, memberID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list cards")
	}

	out := make([]*openpayv1.Card, len(cards))
	for i, c := range cards {
		out[i] = cardToProto(c)
	}
	return &openpayv1.ListCardsResponse{Cards: out}, nil
}

// ── DeleteCard ────────────────────────────────────────────────────────────────

func (s *MemberService) DeleteCard(ctx context.Context, req *openpayv1.DeleteCardRequest) (*openpayv1.DeleteCardResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	memberID, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}
	cardID, err := uuid.Parse(req.CardId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid card_id")
	}

	member, err := s.memberRepo.GetByID(ctx, tc.Tenant.ID, memberID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "member not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch member")
	}

	card, err := s.memberRepo.GetCardByID(ctx, tc.Tenant.ID, memberID, cardID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "card not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch card")
	}

	if err := s.opClient.DeleteCard(ctx, member.OpenpayCustomerID, card.OpenpayCardID); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			s.log.Error().Err(err).Str("card_id", cardID.String()).Msg("openpay delete card failed")
			return nil, status.Errorf(codes.Internal, "delete card on OpenPay: %v", err)
		}
	}

	if err := s.memberRepo.DeleteCard(ctx, tc.Tenant.ID, memberID, cardID); err != nil {
		return nil, status.Error(codes.Internal, "failed to delete card")
	}

	s.log.Info().Str("member_id", memberID.String()).Str("card_id", cardID.String()).Msg("card deleted")
	return &openpayv1.DeleteCardResponse{Success: true}, nil
}

// ── CreateSubscriptionLink ────────────────────────────────────────────────────

func (s *MemberService) CreateSubscriptionLink(ctx context.Context, req *openpayv1.CreateSubscriptionLinkRequest) (*openpayv1.CreateSubscriptionLinkResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	memberID, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}
	planID, err := uuid.Parse(req.PlanId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid plan_id")
	}

	// Validate member belongs to this tenant.
	if _, err := s.memberRepo.GetByID(ctx, tc.Tenant.ID, memberID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "member not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch member")
	}

	// Validate plan belongs to this tenant and is active.
	plan, err := s.planRepo.GetByID(ctx, tc.Tenant.ID, planID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "plan not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch plan")
	}
	if !plan.Active {
		return nil, status.Error(codes.FailedPrecondition, "plan is inactive")
	}

	token, err := generateLinkToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate link token")
	}

	link := &domain.SubscriptionLink{
		TenantID:    tc.Tenant.ID,
		MemberID:    memberID,
		PlanID:      planID,
		Token:       token,
		Status:      "pending",
		Description: req.Description,
	}
	if req.ExpiresInSecs > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresInSecs) * time.Second)
		link.ExpiresAt = &t
	}

	if err := s.memberRepo.CreateSubscriptionLink(ctx, link); err != nil {
		s.log.Error().Err(err).Str("member_id", memberID.String()).Msg("create subscription link failed")
		return nil, status.Error(codes.Internal, "failed to create subscription link")
	}

	checkoutURL := s.checkoutBaseURL + "/s/" + token
	s.log.Info().Str("member_id", memberID.String()).Str("link_id", link.ID.String()).Msg("subscription link created")
	return &openpayv1.CreateSubscriptionLinkResponse{
		Link:        subscriptionLinkToProto(link),
		CheckoutUrl: checkoutURL,
	}, nil
}

// ── RedeemSubscriptionLink ────────────────────────────────────────────────────

func (s *MemberService) RedeemSubscriptionLink(ctx context.Context, req *openpayv1.RedeemSubscriptionLinkRequest) (*openpayv1.RedeemSubscriptionLinkResponse, error) {
	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}
	if req.TokenId == "" {
		return nil, status.Error(codes.InvalidArgument, "token_id is required")
	}
	if req.DeviceSessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "device_session_id is required")
	}

	// Look up the subscription link by token (no tenant auth required — public URL).
	link, err := s.memberRepo.GetSubscriptionLinkByToken(ctx, req.Token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "subscription link not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch subscription link")
	}

	// Validate state.
	if link.Status != "pending" {
		return nil, status.Errorf(codes.FailedPrecondition, "subscription link is already %s", link.Status)
	}
	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		expiredAt := *link.ExpiresAt
		link.Status = "expired"
		_ = s.memberRepo.UpdateSubscriptionLink(ctx, link)
		_ = expiredAt
		return nil, status.Error(codes.FailedPrecondition, "subscription link has expired")
	}

	// Get the member.
	member, err := s.memberRepo.GetByID(ctx, link.TenantID, link.MemberID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to fetch member")
	}
	if member.OpenpayCustomerID == "" {
		return nil, status.Error(codes.FailedPrecondition, "member has no OpenPay customer ID")
	}

	// Get the plan.
	plan, err := s.planRepo.GetByID(ctx, link.TenantID, link.PlanID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to fetch plan")
	}
	if !plan.Active {
		return nil, status.Error(codes.FailedPrecondition, "plan is inactive")
	}

	// Register card with OpenPay using the JS SDK token.
	opCard, err := s.opClient.CreateCard(ctx, member.OpenpayCustomerID, openpay.CreateCardRequest{
		TokenID:         req.TokenId,
		DeviceSessionID: req.DeviceSessionId,
	})
	if err != nil {
		s.log.Error().Err(err).Str("link_token", req.Token).Msg("openpay create card failed during redemption")
		return nil, status.Errorf(codes.Internal, "create card on OpenPay: %v", err)
	}

	// Persist card metadata.
	card := &domain.MemberCard{
		TenantID:        link.TenantID,
		MemberID:        link.MemberID,
		OpenpayCardID:   opCard.ID,
		CardType:        opCard.Type,
		Brand:           opCard.Brand,
		LastFour:        opCard.CardNumber,
		HolderName:      opCard.HolderName,
		ExpirationYear:  opCard.ExpirationYear,
		ExpirationMonth: opCard.ExpirationMonth,
		BankName:        opCard.BankName,
		AllowsCharges:   opCard.AllowsCharges,
	}
	if err := s.memberRepo.CreateCard(ctx, card); err != nil {
		s.log.Error().Err(err).Str("link_token", req.Token).Msg("persist card failed during redemption")
		return nil, status.Error(codes.Internal, "failed to persist card")
	}

	// Create subscription on OpenPay.
	opSub, err := s.opClient.CreateSubscription(ctx, member.OpenpayCustomerID, openpay.CreateSubscriptionRequest{
		PlanID: plan.OpenpayPlanID,
		CardID: opCard.ID,
	})
	if err != nil {
		s.log.Error().Err(err).Str("link_token", req.Token).Msg("openpay create subscription failed during redemption")
		return nil, status.Errorf(codes.Internal, "create subscription on OpenPay: %v", err)
	}

	// Persist subscription.
	sub := &domain.Subscription{
		TenantID:     link.TenantID,
		MemberID:     link.MemberID,
		PlanID:       link.PlanID,
		OpenpaySubID: opSub.ID,
		SourceCardID: opCard.ID,
		Status:       domain.SubscriptionStatus(opSub.Status),
	}
	if opSub.TrialEndDate != "" {
		sub.TrialEndDate = parseOpenPayDate(opSub.TrialEndDate)
	}
	if opSub.PeriodEndDate != "" {
		sub.PeriodEndDate = parseOpenPayDate(opSub.PeriodEndDate)
	}

	if err := s.subscriptionRepo.Create(ctx, sub); err != nil {
		s.log.Error().Err(err).Str("openpay_sub_id", opSub.ID).Msg("subscription created on OpenPay but failed to persist locally")
		return nil, status.Error(codes.Internal, "failed to persist subscription")
	}

	// Mark the link as completed.
	now := time.Now()
	link.Status = "completed"
	link.SubscriptionID = &sub.ID
	link.CompletedAt = &now
	if err := s.memberRepo.UpdateSubscriptionLink(ctx, link); err != nil {
		// Non-fatal: subscription is already created. Log and continue.
		s.log.Error().Err(err).Str("link_token", req.Token).Msg("failed to mark subscription link completed")
	}

	s.log.Info().
		Str("member_id", link.MemberID.String()).
		Str("subscription_id", sub.ID.String()).
		Str("link_token", req.Token).
		Msg("subscription link redeemed")

	return &openpayv1.RedeemSubscriptionLinkResponse{
		Link:           subscriptionLinkToProto(link),
		SubscriptionId: sub.ID.String(),
	}, nil
}

// GetPaymentLinkInfo is a public endpoint (no tenant auth) used by the hosted
// checkout page to display the charge details before the member enters a card.
func (s *MemberService) GetPaymentLinkInfo(ctx context.Context, req *openpayv1.GetPaymentLinkInfoRequest) (*openpayv1.GetPaymentLinkInfoResponse, error) {
	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	link, err := s.memberRepo.GetPaymentLinkByToken(ctx, req.Token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "payment link not found")
		}
		return nil, status.Errorf(codes.Internal, "get payment link: %v", err)
	}

	member, err := s.memberRepo.GetByID(ctx, link.TenantID, link.MemberID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get member: %v", err)
	}

	tenant, err := s.tenantRepo.GetByID(ctx, link.TenantID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get tenant: %v", err)
	}

	expiresAt := ""
	if link.ExpiresAt != nil {
		expiresAt = link.ExpiresAt.Format(time.RFC3339)
	}

	return &openpayv1.GetPaymentLinkInfoResponse{
		Description: link.Description,
		Amount:      link.Amount,
		Currency:    link.Currency,
		MemberName:  member.Name,
		MemberEmail: member.Email,
		Status:      string(link.Status),
		ExpiresAt:   expiresAt,
		OrderId:     link.OrderID,
		TenantName:  tenant.Name,
		LogoUrl:     tenant.LogoURL,
	}, nil
}

// RedeemPaymentLink is a public endpoint (no tenant auth) that tokenizes a card
// via OpenPay JS SDK, creates a one-time charge, and marks the link as redeemed.
func (s *MemberService) RedeemPaymentLink(ctx context.Context, req *openpayv1.RedeemPaymentLinkRequest) (*openpayv1.RedeemPaymentLinkResponse, error) {
	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}
	if req.TokenId == "" {
		return nil, status.Error(codes.InvalidArgument, "token_id is required")
	}
	if req.DeviceSessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "device_session_id is required")
	}

	link, err := s.memberRepo.GetPaymentLinkByToken(ctx, req.Token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "payment link not found")
		}
		return nil, status.Errorf(codes.Internal, "get payment link: %v", err)
	}

	if link.Status != domain.PaymentLinkStatusActive {
		return nil, status.Errorf(codes.FailedPrecondition, "payment link is already %s", link.Status)
	}
	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		link.Status = domain.PaymentLinkStatusExpired
		_ = s.memberRepo.UpdatePaymentLink(ctx, link)
		return nil, status.Error(codes.FailedPrecondition, "payment link has expired")
	}

	member, err := s.memberRepo.GetByID(ctx, link.TenantID, link.MemberID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get member: %v", err)
	}
	if member.OpenpayCustomerID == "" {
		return nil, status.Error(codes.FailedPrecondition, "member has no OpenPay customer ID")
	}

	// Register the card token with OpenPay to get a permanent card ID.
	opCard, err := s.opClient.CreateCard(ctx, member.OpenpayCustomerID, openpay.CreateCardRequest{
		TokenID:         req.TokenId,
		DeviceSessionID: req.DeviceSessionId,
	})
	if err != nil {
		s.log.Error().Err(err).Str("link_token", req.Token).Msg("openpay create card failed during payment link redemption")
		return nil, status.Errorf(codes.Internal, "create card on OpenPay: %v", err)
	}

	// Persist the card for future use.
	card := &domain.MemberCard{
		TenantID:        link.TenantID,
		MemberID:        link.MemberID,
		OpenpayCardID:   opCard.ID,
		CardType:        opCard.Type,
		Brand:           opCard.Brand,
		LastFour:        opCard.CardNumber,
		HolderName:      opCard.HolderName,
		ExpirationYear:  opCard.ExpirationYear,
		ExpirationMonth: opCard.ExpirationMonth,
		BankName:        opCard.BankName,
		AllowsCharges:   opCard.AllowsCharges,
	}
	if err := s.memberRepo.CreateCard(ctx, card); err != nil {
		s.log.Error().Err(err).Str("link_token", req.Token).Msg("persist card failed during payment link redemption")
		return nil, status.Error(codes.Internal, "failed to persist card")
	}

	// Issue the charge on OpenPay using the stored card.
	amountMajor := float64(link.Amount) / 100.0
	chargeReq := openpay.CreateCardChargeRequest{
		Method:          "card",
		SourceID:        opCard.ID,
		Amount:          amountMajor,
		Currency:        link.Currency,
		Description:     link.Description,
		OrderID:         link.OrderID,
		DeviceSessionID: req.DeviceSessionId,
		Capture:         true,
	}
	charge, err := s.opClient.CreateCharge(ctx, member.OpenpayCustomerID, chargeReq)
	if err != nil {
		s.log.Error().Err(err).Str("link_token", req.Token).Msg("openpay create charge failed during payment link redemption")
		return nil, status.Errorf(codes.Internal, "create charge on OpenPay: %v", err)
	}

	// Persist the payment record.
	// ID and IdempotencyKey must be set explicitly — the repo does not
	// auto-generate them. Using charge.ID as the idempotency key ensures
	// that a retry of this RPC (e.g. due to a network hiccup) is a no-op
	// rather than a duplicate insert.
	linkID := link.ID
	now := time.Now()
	payment := &domain.Payment{
		ID:                   uuid.New(),
		TenantID:             link.TenantID,
		MemberID:             link.MemberID,
		LinkID:               &linkID,
		OpenpayTransactionID: charge.ID,
		IdempotencyKey:       "pay-link:" + charge.ID,
		OrderID:              link.OrderID,
		GrossAmount:          link.Amount,
		Currency:             link.Currency,
		Method:               domain.PaymentMethodCard,
		Status:               domain.PaymentStatus(charge.Status),
		Description:          link.Description,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := s.paymentRepo.Create(ctx, payment); err != nil {
		// Non-fatal: charge already created on OpenPay. Log and continue so
		// the link is still marked redeemed. The webhook will settle it.
		s.log.Error().Err(err).Str("charge_id", charge.ID).Msg("failed to persist payment record after charge")
	}

	// Mark the link as redeemed.
	link.Status = domain.PaymentLinkStatusRedeemed
	link.RedeemedAt = &now
	if err := s.memberRepo.UpdatePaymentLink(ctx, link); err != nil {
		s.log.Error().Err(err).Str("link_token", req.Token).Msg("failed to mark payment link redeemed")
	}

	s.log.Info().
		Str("member_id", link.MemberID.String()).
		Str("charge_id", charge.ID).
		Str("link_token", req.Token).
		Msg("payment link redeemed")

	return &openpayv1.RedeemPaymentLinkResponse{
		Link:     paymentLinkToProto(link),
		ChargeId: charge.ID,
	}, nil
}

// GetSubscriptionLinkInfo is a public endpoint (no tenant auth) used by the
// hosted checkout page to display plan and member details before payment.
func (s *MemberService) GetSubscriptionLinkInfo(ctx context.Context, req *openpayv1.GetSubscriptionLinkInfoRequest) (*openpayv1.GetSubscriptionLinkInfoResponse, error) {
	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	link, err := s.memberRepo.GetSubscriptionLinkByToken(ctx, req.Token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "subscription link not found")
		}
		return nil, status.Errorf(codes.Internal, "get subscription link: %v", err)
	}

	member, err := s.memberRepo.GetByID(ctx, link.TenantID, link.MemberID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get member: %v", err)
	}

	plan, err := s.planRepo.GetByID(ctx, link.TenantID, link.PlanID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get plan: %v", err)
	}

	tenant, err := s.tenantRepo.GetByID(ctx, link.TenantID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get tenant: %v", err)
	}

	expiresAt := ""
	if link.ExpiresAt != nil {
		expiresAt = link.ExpiresAt.Format(time.RFC3339)
	}

	return &openpayv1.GetSubscriptionLinkInfoResponse{
		PlanName:    plan.Name,
		Amount:      plan.Amount,
		Currency:    plan.Currency,
		RepeatUnit:  plan.RepeatUnit,
		RepeatEvery: int32(plan.RepeatEvery),
		MemberName:  member.Name,
		MemberEmail: member.Email,
		Status:      link.Status,
		ExpiresAt:   expiresAt,
		Description: link.Description,
		TenantName:  tenant.Name,
		LogoUrl:     tenant.LogoURL,
	}, nil
}
