package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"

	openpayv1 "github.com/menesesghz/openpay-smart-service/gen/openpay/v1"
	"github.com/menesesghz/openpay-smart-service/internal/config"
	"github.com/menesesghz/openpay-smart-service/internal/kafka"
	"github.com/menesesghz/openpay-smart-service/internal/middleware"
	"github.com/menesesghz/openpay-smart-service/internal/openpay"
	"github.com/menesesghz/openpay-smart-service/internal/repository/postgres"
	"github.com/menesesghz/openpay-smart-service/internal/service"
	"github.com/menesesghz/openpay-smart-service/internal/webhook"
)

var (
	cfgPath = flag.String("config", "", "path to config file (optional; defaults to env vars)")
	version = "dev"
)

func main() {
	flag.Parse()

	// ── Config ────────────────────────────────────────────────────────────────
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	// ── Logger ────────────────────────────────────────────────────────────────
	level, _ := zerolog.ParseLevel(cfg.Telemetry.LogLevel)
	zerolog.SetGlobalLevel(level)
	log.Logger = zerolog.New(os.Stdout).With().
		Timestamp().
		Str("service", cfg.Telemetry.ServiceName).
		Str("version", version).
		Logger()

	log.Info().Str("env", cfg.OpenPay.Environment).Msg("starting openpay-smart-service")

	ctx := context.Background()

	// ── PostgreSQL ────────────────────────────────────────────────────────────
	db, err := postgres.Connect(ctx, cfg.Database)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot connect to PostgreSQL")
	}
	defer db.Close()
	log.Info().Msg("PostgreSQL connected")

	// ── Repositories ─────────────────────────────────────────────────────────
	tenantRepo := postgres.NewTenantRepo(db, cfg.Encryption.AESKeyHex)
	paymentRepo := postgres.NewPaymentRepo(db)
	balanceRepo := postgres.NewBalanceRepo(db)
	planRepo := postgres.NewPlanRepo(db)
	subscriptionRepo := postgres.NewSubscriptionRepo(db)
	memberRepo := postgres.NewMemberRepo(db)
	webhookRepo := postgres.NewWebhookRepo(db)

	// ── Redis ─────────────────────────────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal().Err(err).Msg("cannot connect to Redis")
	}
	log.Info().Msg("Redis connected")

	// ── OpenPay client (singleton, service-owner credentials) ─────────────────
	opClient := openpay.NewClientFromConfig(cfg.OpenPay, log.Logger)

	// ── Kafka publisher ───────────────────────────────────────────────────────
	kafkaPublisher := kafka.NewPublisher(
		cfg.Kafka.Brokers,
		cfg.Kafka.TopicPaymentEvents,
		cfg.Kafka.TopicSubscriptionEvents,
		log.Logger,
	)
	defer func() {
		if err := kafkaPublisher.Close(); err != nil {
			log.Error().Err(err).Msg("kafka publisher close error")
		}
	}()

	// ── Services ──────────────────────────────────────────────────────────────
	paymentSvc := service.NewPaymentService(paymentRepo, opClient, log.Logger)
	subscriptionSvc := service.NewSubscriptionService(
		planRepo,
		subscriptionRepo,
		paymentRepo,
		memberRepo,
		opClient,
		log.Logger,
	)
	adminTenantSvc := service.NewAdminTenantService(tenantRepo, log.Logger)
	tenantSvc := service.NewTenantService(tenantRepo, log.Logger)
	memberSvc := service.NewMemberService(memberRepo, planRepo, subscriptionRepo, opClient, log.Logger)
	balanceSvc := service.NewBalanceService(balanceRepo, log.Logger)
	webhookSvc := service.NewWebhookService(webhookRepo, cfg.Encryption.AESKeyHex, log.Logger)

	// ── gRPC server ───────────────────────────────────────────────────────────
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			middleware.LoggingInterceptor(log.Logger),
			// Propagate the real browser IP (injected by grpc-gateway) into the
			// context so the OpenPay client can forward it via X-Forwarded-For.
			clientIPInterceptor(),
			// AdminAuthInterceptor must come before AuthInterceptor so that admin
			// requests are validated and flagged before the tenant lookup runs.
			middleware.AdminAuthInterceptor(cfg.Admin.APIKey),
			middleware.AuthInterceptor(tenantRepo),
			middleware.RateLimitInterceptor(rdb, func(tenantID string) middleware.TenantTier {
				// TODO: look up the tenant's tier from tenantRepo for accurate rate limits.
				// For now all tenants fall back to Standard.
				return middleware.TierStandard
			}),
		),
		grpc.ChainStreamInterceptor(
			middleware.StreamAuthInterceptor(tenantRepo),
		),
	)

	// Register service handlers.
	openpayv1.RegisterPaymentServiceServer(grpcServer, paymentSvc)
	openpayv1.RegisterSubscriptionServiceServer(grpcServer, subscriptionSvc)
	openpayv1.RegisterAdminTenantServiceServer(grpcServer, adminTenantSvc)
	openpayv1.RegisterTenantServiceServer(grpcServer, tenantSvc)
	openpayv1.RegisterMemberServiceServer(grpcServer, memberSvc)
	openpayv1.RegisterBalanceServiceServer(grpcServer, balanceSvc)
	openpayv1.RegisterWebhookServiceServer(grpcServer, webhookSvc)

	// Health check
	healthSvc := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSvc)
	healthSvc.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// gRPC reflection — lets grpcurl list and call methods without proto files.
	reflection.Register(grpcServer)

	// ── gRPC listener ─────────────────────────────────────────────────────────
	grpcAddr := fmt.Sprintf(":%d", cfg.Server.GRPCPort)
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", grpcAddr).Msg("cannot bind gRPC port")
	}

	// ── grpc-gateway (REST → gRPC translation) ────────────────────────────────
	// RegisterXxxHandlerFromEndpoint dials the gRPC server, so HTTP requests go
	// through the full interceptor chain (auth, rate limit, logging).
	// Inject the real browser/client IP into gRPC metadata so the OpenPay client
	// can forward it in the X-Forwarded-For header for anti-fraud compliance.
	gwMux := runtime.NewServeMux(
		runtime.WithMetadata(func(_ context.Context, req *http.Request) metadata.MD {
			return metadata.Pairs("x-client-ip", realClientIP(req))
		}),
	)
	grpcEndpoint := fmt.Sprintf("localhost:%d", cfg.Server.GRPCPort)
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	for _, reg := range []struct {
		name string
		fn   func() error
	}{
		{"AdminTenantService", func() error {
			return openpayv1.RegisterAdminTenantServiceHandlerFromEndpoint(ctx, gwMux, grpcEndpoint, dialOpts)
		}},
		{"TenantService", func() error {
			return openpayv1.RegisterTenantServiceHandlerFromEndpoint(ctx, gwMux, grpcEndpoint, dialOpts)
		}},
		{"PaymentService", func() error {
			return openpayv1.RegisterPaymentServiceHandlerFromEndpoint(ctx, gwMux, grpcEndpoint, dialOpts)
		}},
		{"SubscriptionService", func() error {
			return openpayv1.RegisterSubscriptionServiceHandlerFromEndpoint(ctx, gwMux, grpcEndpoint, dialOpts)
		}},
		{"MemberService", func() error {
			return openpayv1.RegisterMemberServiceHandlerFromEndpoint(ctx, gwMux, grpcEndpoint, dialOpts)
		}},
		{"BalanceService", func() error {
			return openpayv1.RegisterBalanceServiceHandlerFromEndpoint(ctx, gwMux, grpcEndpoint, dialOpts)
		}},
		{"WebhookService", func() error {
			return openpayv1.RegisterWebhookServiceHandlerFromEndpoint(ctx, gwMux, grpcEndpoint, dialOpts)
		}},
	} {
		if err := reg.fn(); err != nil {
			log.Fatal().Err(err).Str("service", reg.name).Msg("failed to register grpc-gateway handler")
		}
	}

	// ── HTTP mux ──────────────────────────────────────────────────────────────
	httpMux := http.NewServeMux()

	// Health probe
	httpMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// OpenPay webhook ingress — POST /webhooks/openpay
	// OpenPay calls this endpoint for every transaction event (charge.succeeded,
	// charge.failed, etc.). Configure the same URL in the OpenPay dashboard under
	// Configuración → Notificaciones. Set the "Contraseña" field there to the
	// value of OPENPAY_OPENPAY_WEBHOOK_INGRESS_SECRET in this service's env.
	ingressHandler := webhook.NewIngressHandler(
		cfg.OpenPay.WebhookIngressSecret,
		paymentRepo,
		tenantRepo,
		balanceRepo,
		subscriptionRepo,
		kafkaPublisher,
		log.Logger,
	)
	httpMux.Handle("/webhooks/openpay", ingressHandler)

	// REST gateway — all /v1/ routes are handled by grpc-gateway.
	httpMux.Handle("/v1/", gwMux)

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:           corsMiddleware(httpMux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// ── Start ─────────────────────────────────────────────────────────────────
	go func() {
		log.Info().Str("addr", grpcAddr).Msg("gRPC server listening")
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatal().Err(err).Msg("gRPC server failed")
		}
	}()

	go func() {
		log.Info().Str("addr", httpSrv.Addr).Msg("HTTP gateway listening")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("HTTP gateway failed")
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down...")

	grpcServer.GracefulStop()

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		log.Error().Err(err).Msg("HTTP shutdown error")
	}

	log.Info().Msg("server stopped")
}

// clientIPInterceptor reads the "x-client-ip" metadata key injected by the
// grpc-gateway WithMetadata hook and stores it in the context using
// openpay.WithClientIP so that every downstream OpenPay API call sets
// X-Forwarded-For to the real browser/caller IP (required by anti-fraud).
func clientIPInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get("x-client-ip"); len(vals) > 0 && vals[0] != "" {
				ctx = openpay.WithClientIP(ctx, vals[0])
			}
		}
		return handler(ctx, req)
	}
}

// realClientIP extracts the originating client IP from the HTTP request,
// preferring X-Real-IP, then the first address in X-Forwarded-For, then
// falling back to RemoteAddr.
//
// Loopback (127.x, ::1) and private RFC-1918 addresses are skipped because
// forwarding them to OpenPay as X-Forwarded-For would mismatch the public IP
// that the OpenPay JS SDK used when generating the device_session_id fingerprint.
// When no routable IP is found we return "" so the OpenPay client omits the
// header and lets Docker's outbound NAT IP flow through naturally.
func realClientIP(r *http.Request) string {
	candidates := []string{
		r.Header.Get("X-Real-IP"),
		firstToken(r.Header.Get("X-Forwarded-For")),
		remoteHost(r.RemoteAddr),
	}
	for _, ip := range candidates {
		if ip != "" && !isPrivateOrLoopback(ip) {
			return ip
		}
	}
	return "" // let the OpenPay client omit X-Forwarded-For
}

func firstToken(s string) string {
	for i, c := range s {
		if c == ',' {
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

func remoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func isPrivateOrLoopback(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	private := []net.IPNet{
		{IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(8, 32)},
		{IP: net.ParseIP("172.16.0.0"), Mask: net.CIDRMask(12, 32)},
		{IP: net.ParseIP("192.168.0.0"), Mask: net.CIDRMask(16, 32)},
		// Docker default bridge
		{IP: net.ParseIP("172.17.0.0"), Mask: net.CIDRMask(16, 32)},
	}
	for _, block := range private {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// corsMiddleware adds CORS headers so browser-based clients (e.g. the checkout
// test page) can call the REST gateway from a file:// or localhost origin.
// OPTIONS preflight requests are answered immediately with 204 No Content.
//
// NOTE: In production, replace the wildcard Access-Control-Allow-Origin with
// your actual frontend domain(s).
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// Preflight: answer immediately, no need to pass to the handler.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
