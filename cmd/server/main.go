package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
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
	cfgPath = flag.String("config", "config.yaml", "path to config file")
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

	// ── gRPC services ─────────────────────────────────────────────────────────
	paymentSvc := service.NewPaymentService(paymentRepo, opClient, log.Logger)
	subscriptionSvc := service.NewSubscriptionService(
		planRepo,
		subscriptionRepo,
		paymentRepo,
		memberRepo,
		opClient,
		log.Logger,
	)

	// ── gRPC server ───────────────────────────────────────────────────────────
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			middleware.LoggingInterceptor(log.Logger),
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

	// TODO: register grpc-gateway mux once REST translation is needed.

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:           httpMux,
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
