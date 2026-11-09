package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/percona/obs-dashboard/internal/api"
	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/config"
	"github.com/percona/obs-dashboard/internal/cve"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/metricsampler"
	"github.com/percona/obs-dashboard/internal/mq"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/presence"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/telemetry"
	"github.com/percona/obs-dashboard/internal/unblocker"
	"github.com/percona/obs-dashboard/internal/upgrade"
	"github.com/percona/obs-dashboard/internal/version"
	"github.com/percona/obs-dashboard/internal/worker"
	"github.com/percona/obs-dashboard/internal/workingset"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	slog.Info("obs-dashboard starting", "version", version.Version)
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	db, err := store.Open(cfg.Store.DBPath)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer db.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Built before any background goroutine starts: a failed or hanging
	// Google discovery aborts startup before any OBS traffic is generated.
	var provider *auth.Provider
	if cfg.Auth.Enabled() {
		discoverCtx, discoverCancel := context.WithTimeout(ctx, 15*time.Second)
		provider, err = auth.NewGoogleProvider(discoverCtx, cfg.Auth.GoogleClientID, cfg.Auth.GoogleClientSecret, cfg.Auth.PublicURL+"/auth/callback")
		discoverCancel()
		if err != nil {
			return fmt.Errorf("auth: %w", err)
		}
		slog.Info("auth: Google sign-in enabled", "public_url", cfg.Auth.PublicURL, "admins", len(cfg.Auth.Admins))
	} else {
		slog.Info("auth: GOOGLE_CLIENT_ID not set; sign-in disabled and actions unavailable")
	}
	authSvc := auth.NewService(db, auth.Options{
		Provider:   provider,
		PublicURL:  cfg.Auth.PublicURL,
		Admins:     cfg.Auth.Admins,
		SessionTTL: cfg.Auth.SessionTTL,
	})
	if err := authSvc.PruneExpired(); err != nil {
		slog.Warn("auth: prune expired sessions", "err", err)
	}

	if cfg.Upgrades.ApplyDir != "" {
		if err := upgrade.ValidateApplyDir(cfg.Upgrades.ApplyDir); err != nil {
			return fmt.Errorf("upgrades: %w", err)
		}
	}
	upg := upgrade.NewManager(upgrade.Options{
		DB:               db,
		DBPath:           cfg.Store.DBPath,
		Source:           upgrade.GitHubSource{Repo: cfg.Upgrades.GitHubRepo},
		Repo:             cfg.Upgrades.GitHubRepo,
		Current:          version.Version,
		HostFilesVersion: cfg.Upgrades.HostFilesVersion,
		ApplyDir:         cfg.Upgrades.ApplyDir,
		Interval:         cfg.Upgrades.CheckInterval,
	})
	upg.Reconcile(true)
	go upg.Run(ctx)
	slog.Info("upgrades: release checks enabled", "repo", cfg.Upgrades.GitHubRepo, "apply_enabled", upg.ApplyEnabled(), "host_files", cfg.Upgrades.HostFilesVersion)

	legacySlug := cfg.Instances[0].Slug
	for _, ic := range cfg.Instances {
		if ic.Root == cfg.LegacyRoot {
			legacySlug = ic.Slug
			break
		}
	}
	instances := make([]*obs.Instance, 0, len(cfg.Instances))
	for _, ic := range cfg.Instances {
		c := obs.NewClient(ic.APIURL, ic.Username, ic.Password)
		c.SetMinuteBudget(ic.MinuteRequestBudget)
		mqURL, err := ic.MQ.DialURL()
		if err != nil {
			return fmt.Errorf("instance %s: %w", ic.Name, err)
		}
		instances = append(instances, obs.NewInstance(obs.InstanceInfo{
			Name: ic.Name, Slug: ic.Slug, Root: ic.Root,
			WebURL: ic.WebURL, DownloadURL: ic.DownloadURL, Registry: ic.Registry,
			MQURL: mqURL, MQExchange: ic.MQ.Exchange, MQRoutingPrefix: ic.MQ.RoutingPrefix,
		}, c))
	}
	fleet := obs.NewFleet(instances...)
	if err := store.MigrateLogicalNames(db, cfg.LegacyRoot, legacySlug); err != nil {
		return fmt.Errorf("migrate logical names: %w", err)
	}
	h := hub.New()
	gate := presence.New(cfg.Idle.Enabled, cfg.Idle.Linger)

	scanner := cve.NewScanner(db, h, 2, cve.WithURLs(fleet))
	scanner.Start(ctx)

	nightlySched := cve.NewNightlyScheduler(db, scanner)
	go nightlySched.Run(ctx)

	activePkgs, err := store.GetActivePackages(db)
	if err != nil {
		return fmt.Errorf("seed working set: %w", err)
	}
	// One-shot backfill: settled built packages with no version (aggregates)
	// get a single pass so VersionTask can resolve them, then settle out.
	versionless, err := store.GetVersionlessBuiltPackages(db)
	if err != nil {
		return fmt.Errorf("seed version backfill: %w", err)
	}
	ws := workingset.New(cfg.WorkerPool.QueueSize, cfg.WorkerPool.PollInterval,
		cfg.WorkerPool.BackoffMax, cfg.WorkerPool.BatchThreshold)
	ws.Seed(activePkgs)
	ws.Seed(versionless)
	slog.Info("worker: seeded working set", "active", len(activePkgs), "version_backfill", len(versionless))
	// Seed repo owners and membership from every stored package (settled
	// ones too) before the poller's first tick, so packages living on an
	// instance that is down at startup are carried forward, not dropped.
	storedPkgs, err := store.QueryPackages(db, "")
	if err != nil {
		return fmt.Errorf("seed instance membership: %w", err)
	}
	fleet.SeedOwners(storedPkgs)

	devTasks := []worker.Task{
		obs.PackageTypeTask{},
		obs.BuildStateTask{},
		obs.PublishStateTask{},
		obs.VersionTask{},
		obs.ContainerTagsTask{},
		obs.BlockedReasonTask{},
		obs.BuildReasonTask{},
	}
	releaseTasks := []worker.Task{
		obs.PackageTypeTask{},
		obs.ContainerTagsTask{},
		obs.BinariesCheckTask{},
	}
	pool := worker.NewPool(cfg.WorkerPool.Size, devTasks, releaseTasks, fleet, db, h, ws, scanner)
	ws.SetGate(gate)
	pool.Start(ctx)
	ws.StartScheduler(ctx)

	poller := obs.NewPoller(fleet, db, cfg.Poller.Interval, h, ws, gate)

	go poller.Run(ctx)
	for _, inst := range fleet.Instances() {
		go mq.NewConsumer(inst, fleet, db, h, ws).Run(ctx)
	}
	go runPruner(ctx, db, cfg.Poller.Interval, cfg.Store.EventRetention, cfg.Store.MetricsRetention)

	sampler := &metricsampler.Sampler{DB: db, Snap: fleet}
	go sampler.Run(ctx)

	if cfg.Unblocker.Enabled {
		sweeper := &unblocker.Sweeper{DB: db, Rebuilder: fleet, Threshold: cfg.Unblocker.Threshold}
		go sweeper.Run(ctx)
	}

	telemetryEnabled := &atomic.Bool{}
	telemetryEnabled.Store(cfg.Telemetry.Enabled)
	reporter := &telemetry.Reporter{
		Interval: cfg.Telemetry.Interval,
		Enabled:  telemetryEnabled,
		Stats:    ws,
		Snap:     fleet,
		Limiter:  fleet,
	}
	go reporter.Run(ctx)

	router := api.NewRouter(db, h, fleet, ws, telemetryEnabled, cfg.Telemetry.Interval, gate, authSvc, upg, scanner)

	go fleet.RunHealthWatch(ctx, 15*time.Second, func(slug string, health obs.Health) {
		h.Notify(hub.InstanceHealth(map[string]any{"slug": slug, "health": health}))
	})

	handler := api.WithFrontend(router, cfg.Server.FrontendDir)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Error("http shutdown", "err", err)
		}
	}()

	slog.Info("listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http: %w", err)
	}
	return nil
}

func runPruner(ctx context.Context, db *sql.DB, interval, eventRetention, metricsRetention time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-eventRetention)
			if err := store.PruneEvents(db, cutoff); err != nil {
				slog.Error("prune events", "err", err)
			}
			if _, err := store.PruneMetricsSamples(db, time.Now().UTC().Add(-metricsRetention)); err != nil {
				slog.Warn("pruner: prune metrics samples", "err", err)
			}
		}
	}
}
