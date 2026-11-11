package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Poller     PollerConfig
	Store      StoreConfig
	Server     ServerConfig
	WorkerPool WorkerPoolConfig
	Telemetry  TelemetryConfig
	Unblocker  UnblockerConfig
	Idle       IdleConfig
	Auth       AuthConfig
	Upgrades   UpgradesConfig
	Release    ReleaseConfig
	// Instances lists every OBS instance the dashboard reads from, in config
	// order. Always at least one (the legacy instance when obs_instances is
	// absent).
	Instances []InstanceConfig
	// LegacyRoot is obs_root: the root stored project names carried before
	// the root-free migration. Used only by that migration.
	LegacyRoot string
}

// legacyConfig holds the pre-obs_instances single-instance keys.
type legacyConfig struct {
	root, baseURL, username, password, mqURL string
	budget                                   int
}

// MQInstanceConfig is one instance's AMQP event bus. Username and Password,
// when set, override credentials embedded in URL (which carries host, port
// and vhost).
type MQInstanceConfig struct {
	URL           string `mapstructure:"url"`
	Exchange      string `mapstructure:"exchange"`
	RoutingPrefix string `mapstructure:"routing_prefix"`
	Username      string `mapstructure:"username"`
	Password      string `mapstructure:"password"`
}

// DialURL returns URL with Username/Password applied as its user info; each
// set field replaces the matching part embedded in URL. Errors never include
// the URL, which may carry a password.
func (m MQInstanceConfig) DialURL() (string, error) {
	u, err := url.Parse(m.URL)
	if err != nil {
		return "", fmt.Errorf("mq.url is not a valid URL")
	}
	if m.Username == "" && m.Password == "" {
		return m.URL, nil
	}
	user, pass, hasPass := "", "", false
	if u.User != nil {
		user = u.User.Username()
		pass, hasPass = u.User.Password()
	}
	if m.Username != "" {
		user = m.Username
	}
	if m.Password != "" {
		pass, hasPass = m.Password, true
	}
	if hasPass {
		u.User = url.UserPassword(user, pass)
	} else {
		u.User = url.User(user)
	}
	return u.String(), nil
}

// InstanceConfig describes one OBS instance.
type InstanceConfig struct {
	Name                string           `mapstructure:"name"`
	Slug                string           `mapstructure:"-"`
	Root                string           `mapstructure:"root"`
	APIURL              string           `mapstructure:"api_url"`
	WebURL              string           `mapstructure:"web_url"`
	DownloadURL         string           `mapstructure:"download_url"`
	Registry            string           `mapstructure:"registry"`
	Username            string           `mapstructure:"username"`
	Password            string           `mapstructure:"password"`
	MinuteRequestBudget int              `mapstructure:"-"`
	MQ                  MQInstanceConfig `mapstructure:"mq"`
}

// rawInstance distinguishes an omitted budget (default 60) from an explicit 0.
type rawInstance struct {
	InstanceConfig      `mapstructure:",squash"`
	MinuteRequestBudget *int `mapstructure:"minute_request_budget"`
}

// Slug derives an instance's stable identifier from its display name:
// lowercase, every character outside [a-z0-9] replaced by '_'.
func Slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

type PollerConfig struct {
	Interval time.Duration
}

type StoreConfig struct {
	DBPath           string
	EventRetention   time.Duration
	MetricsRetention time.Duration
}

type ServerConfig struct {
	HTTPPort    int
	FrontendDir string
}

type WorkerPoolConfig struct {
	Size           int
	PollInterval   time.Duration
	QueueSize      int
	BackoffMax     time.Duration
	BatchThreshold int
}

type TelemetryConfig struct {
	Interval time.Duration
	Enabled  bool
}

type UnblockerConfig struct {
	Enabled   bool
	Threshold time.Duration
}

type IdleConfig struct {
	Enabled bool
	Linger  time.Duration
}

// AuthConfig configures Google sign-in. Sign-in is enabled only when
// GoogleClientID is set; otherwise protected actions are unavailable.
type AuthConfig struct {
	PublicURL          string
	Admins             []string
	SessionTTL         time.Duration
	GoogleClientID     string
	GoogleClientSecret string
}

// Enabled reports whether Google sign-in is configured.
func (a AuthConfig) Enabled() bool { return a.GoogleClientID != "" }

// loadAuth reads the auth section. Admins come from auth.admins plus the
// comma-separated OBS_DASHBOARD_ADMINS env var.
func loadAuth(v *viper.Viper) (AuthConfig, error) {
	ttl, err := time.ParseDuration(v.GetString("auth.session_ttl"))
	if err != nil {
		return AuthConfig{}, fmt.Errorf("invalid AUTH_SESSION_TTL %q: %w", v.GetString("auth.session_ttl"), err)
	}
	a := AuthConfig{
		PublicURL:          strings.TrimRight(v.GetString("auth.public_url"), "/"),
		SessionTTL:         ttl,
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
	}
	for _, e := range append(v.GetStringSlice("auth.admins"), strings.Split(os.Getenv("OBS_DASHBOARD_ADMINS"), ",")...) {
		if e = strings.TrimSpace(e); e != "" {
			a.Admins = append(a.Admins, e)
		}
	}
	if !a.Enabled() {
		return a, nil
	}
	if a.GoogleClientSecret == "" {
		return AuthConfig{}, fmt.Errorf("GOOGLE_CLIENT_SECRET is required when GOOGLE_CLIENT_ID is set")
	}
	u, err := url.Parse(a.PublicURL)
	if a.PublicURL == "" || err != nil || (strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https") || u.Host == "" {
		return AuthConfig{}, fmt.Errorf("auth.public_url (or AUTH_PUBLIC_URL) must be an absolute http(s) URL when GOOGLE_CLIENT_ID is set, got %q", a.PublicURL)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil || strings.ContainsAny(a.PublicURL, "?#") {
		return AuthConfig{}, fmt.Errorf("auth.public_url (or AUTH_PUBLIC_URL) must be only scheme and host (no path, query, fragment or userinfo), got %q", a.PublicURL)
	}
	// Canonical form so it compares equal to the browser's Origin header.
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && !(scheme == "https" && port == "443") && !(scheme == "http" && port == "80") {
		host += ":" + port
	}
	a.PublicURL = scheme + "://" + host
	return a, nil
}

func loadInstances(v *viper.Viper, legacy legacyConfig) ([]InstanceConfig, error) {
	var raw []rawInstance
	if v.IsSet("obs_instances") {
		if err := v.UnmarshalKey("obs_instances", &raw); err != nil {
			return nil, fmt.Errorf("obs_instances: %w", err)
		}
	}
	if len(raw) == 0 {
		if legacy.username == "" {
			return nil, fmt.Errorf("OBS_USERNAME is required")
		}
		return []InstanceConfig{{
			Name:                "openSUSE",
			Slug:                "opensuse",
			Root:                legacy.root,
			APIURL:              legacy.baseURL,
			WebURL:              "https://build.opensuse.org",
			DownloadURL:         "https://download.opensuse.org/repositories",
			Registry:            "registry.opensuse.org",
			Username:            legacy.username,
			Password:            legacy.password,
			MinuteRequestBudget: legacy.budget,
			MQ:                  MQInstanceConfig{URL: legacy.mqURL, Exchange: "pubsub", RoutingPrefix: "opensuse.obs"},
		}}, nil
	}

	out := make([]InstanceConfig, 0, len(raw))
	seen := map[string]bool{}
	for i, r := range raw {
		in := r.InstanceConfig
		in.Slug = Slug(in.Name)
		label := fmt.Sprintf("obs_instances[%d] (%s)", i, in.Name)
		if in.Name == "" {
			return nil, fmt.Errorf("obs_instances[%d]: name is required", i)
		}
		if seen[in.Slug] {
			return nil, fmt.Errorf("%s: duplicate instance slug %q", label, in.Slug)
		}
		seen[in.Slug] = true
		env := "OBS_" + strings.ToUpper(in.Slug)
		if u := os.Getenv(env + "_USERNAME"); u != "" {
			in.Username = u
		}
		if p := os.Getenv(env + "_PASSWORD"); p != "" {
			in.Password = p
		}
		if u := os.Getenv(env + "_MQ_USERNAME"); u != "" {
			in.MQ.Username = u
		}
		if p := os.Getenv(env + "_MQ_PASSWORD"); p != "" {
			in.MQ.Password = p
		}
		in.MinuteRequestBudget = 60
		if r.MinuteRequestBudget != nil {
			in.MinuteRequestBudget = *r.MinuteRequestBudget
		}
		if in.MQ.Exchange == "" {
			in.MQ.Exchange = "pubsub"
		}
		if in.MQ.RoutingPrefix == "" {
			in.MQ.RoutingPrefix = "opensuse.obs"
		}
		in.APIURL = strings.TrimRight(in.APIURL, "/")
		in.WebURL = strings.TrimRight(in.WebURL, "/")
		in.DownloadURL = strings.TrimRight(in.DownloadURL, "/")
		in.Registry = strings.TrimRight(in.Registry, "/")
		for field, val := range map[string]string{
			"root": in.Root, "api_url": in.APIURL, "web_url": in.WebURL,
			"download_url": in.DownloadURL, "registry": in.Registry, "mq.url": in.MQ.URL,
		} {
			if val == "" {
				return nil, fmt.Errorf("%s: %s is required", label, field)
			}
		}
		if in.Username == "" {
			return nil, fmt.Errorf("%s: username is required (or set %s_USERNAME)", label, env)
		}
		if _, err := in.MQ.DialURL(); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		out = append(out, in)
	}
	return out, nil
}

func Load() (*Config, error) {
	v := viper.New()

	v.SetDefault("obs_root", "isv:percona")
	_ = v.BindEnv("obs_root", "OBS_ROOT")

	v.SetDefault("obs.base_url", "https://api.opensuse.org")
	v.SetDefault("obs.minute_request_budget", 60)
	v.SetDefault("mq.url", "amqps://opensuse:opensuse@rabbit.opensuse.org:5671/")
	v.SetDefault("poller.interval", "2m")
	v.SetDefault("store.db_path", "/data/obsboard.db")
	v.SetDefault("store.event_retention", "7d")
	v.SetDefault("store.metrics_retention", "30d")
	v.SetDefault("server.http_port", 4000)
	v.SetDefault("server.frontend_dir", "")
	v.SetDefault("worker_pool.size", 5)
	v.SetDefault("worker_pool.poll_interval", "30s")
	v.SetDefault("worker_pool.queue_size", 512)
	v.SetDefault("worker_pool.backoff_max", "5m")
	v.SetDefault("worker_pool.batch_threshold", 4)
	v.SetDefault("telemetry.interval", "60s")
	v.SetDefault("telemetry.enabled", false)
	v.SetDefault("unblocker.enabled", false)
	v.SetDefault("unblocker.threshold", "30m")
	v.SetDefault("idle.enabled", true)
	v.SetDefault("idle.linger", "5m")
	v.SetDefault("auth.session_ttl", "168h")
	v.SetDefault("upgrades.github_repo", "Percona-Lab/obs-dashboard")
	v.SetDefault("upgrades.check_interval", "1h")
	v.SetDefault("release.github_repo", "percona/obs-packaging")

	// Config file (optional)
	cfgFile := "config.yaml"
	if f := os.Getenv("CONFIG_FILE"); f != "" {
		cfgFile = f
	}
	v.SetConfigFile(cfgFile)
	_ = v.ReadInConfig()

	// Env vars take priority
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	for _, pair := range [][]string{
		{"obs.username", "OBS_USERNAME"},
		{"obs.password", "OBS_PASSWORD"},
		{"obs.base_url", "OBS_BASE_URL"},
		{"obs.minute_request_budget", "OBS_MINUTE_REQUEST_BUDGET"},
		{"mq.url", "MQ_URL"},
		{"poller.interval", "POLL_INTERVAL"},
		{"store.db_path", "DB_PATH"},
		{"store.event_retention", "EVENT_RETENTION"},
		{"store.metrics_retention", "METRICS_RETENTION"},
		{"server.http_port", "HTTP_PORT"},
		{"server.frontend_dir", "FRONTEND_DIR"},
		{"worker_pool.size", "WORKER_POOL_SIZE"},
		{"worker_pool.poll_interval", "WORKER_POOL_POLL_INTERVAL"},
		{"worker_pool.queue_size", "WORKER_POOL_QUEUE_SIZE"},
		{"worker_pool.backoff_max", "WORKER_POOL_BACKOFF_MAX"},
		{"worker_pool.batch_threshold", "WORKER_POOL_BATCH_THRESHOLD"},
		{"telemetry.interval", "TELEMETRY_INTERVAL"},
		{"telemetry.enabled", "TELEMETRY_ENABLED"},
		{"unblocker.enabled", "UNBLOCKER_ENABLED"},
		{"unblocker.threshold", "UNBLOCKER_THRESHOLD"},
		{"idle.enabled", "IDLE_ENABLED"},
		{"idle.linger", "IDLE_LINGER"},
		{"auth.public_url", "AUTH_PUBLIC_URL"},
		{"auth.session_ttl", "AUTH_SESSION_TTL"},
		{"upgrades.github_repo", "UPGRADES_GITHUB_REPO"},
		{"upgrades.check_interval", "UPGRADES_CHECK_INTERVAL"},
		{"release.github_token", "RELEASE_GITHUB_TOKEN"},
		{"release.github_repo", "RELEASE_GITHUB_REPO"},
	} {
		_ = v.BindEnv(pair[0], pair[1])
	}

	pollInterval, err := time.ParseDuration(v.GetString("poller.interval"))
	if err != nil {
		return nil, fmt.Errorf("invalid POLL_INTERVAL %q: %w", v.GetString("poller.interval"), err)
	}

	retention, err := parseRetention(v.GetString("store.event_retention"))
	if err != nil {
		return nil, fmt.Errorf("invalid EVENT_RETENTION %q: %w", v.GetString("store.event_retention"), err)
	}

	metricsRetention, err := parseRetention(v.GetString("store.metrics_retention"))
	if err != nil {
		return nil, fmt.Errorf("invalid METRICS_RETENTION %q: %w", v.GetString("store.metrics_retention"), err)
	}

	pollIntervalWP, err := time.ParseDuration(v.GetString("worker_pool.poll_interval"))
	if err != nil {
		return nil, fmt.Errorf("invalid WORKER_POOL_POLL_INTERVAL %q: %w", v.GetString("worker_pool.poll_interval"), err)
	}

	backoffMax, err := time.ParseDuration(v.GetString("worker_pool.backoff_max"))
	if err != nil {
		return nil, fmt.Errorf("invalid WORKER_POOL_BACKOFF_MAX %q: %w", v.GetString("worker_pool.backoff_max"), err)
	}

	telemetryInterval, err := time.ParseDuration(v.GetString("telemetry.interval"))
	if err != nil {
		return nil, fmt.Errorf("invalid TELEMETRY_INTERVAL %q: %w", v.GetString("telemetry.interval"), err)
	}

	unblockThreshold, err := time.ParseDuration(v.GetString("unblocker.threshold"))
	if err != nil {
		return nil, fmt.Errorf("invalid UNBLOCKER_THRESHOLD %q: %w", v.GetString("unblocker.threshold"), err)
	}

	idleLinger, err := time.ParseDuration(v.GetString("idle.linger"))
	if err != nil {
		return nil, fmt.Errorf("invalid IDLE_LINGER %q: %w", v.GetString("idle.linger"), err)
	}

	legacy := legacyConfig{
		root:     v.GetString("obs_root"),
		baseURL:  strings.TrimRight(v.GetString("obs.base_url"), "/"),
		username: v.GetString("obs.username"),
		password: v.GetString("obs.password"),
		budget:   v.GetInt("obs.minute_request_budget"),
		mqURL:    v.GetString("mq.url"),
	}

	cfg := &Config{
		Poller: PollerConfig{Interval: pollInterval},
		Store: StoreConfig{
			DBPath:           v.GetString("store.db_path"),
			EventRetention:   retention,
			MetricsRetention: metricsRetention,
		},
		Server: ServerConfig{
			HTTPPort:    v.GetInt("server.http_port"),
			FrontendDir: v.GetString("server.frontend_dir"),
		},
		WorkerPool: WorkerPoolConfig{
			Size:           v.GetInt("worker_pool.size"),
			PollInterval:   pollIntervalWP,
			QueueSize:      v.GetInt("worker_pool.queue_size"),
			BackoffMax:     backoffMax,
			BatchThreshold: v.GetInt("worker_pool.batch_threshold"),
		},
		Telemetry: TelemetryConfig{
			Interval: telemetryInterval,
			Enabled:  v.GetBool("telemetry.enabled"),
		},
		Unblocker: UnblockerConfig{
			Enabled:   v.GetBool("unblocker.enabled"),
			Threshold: unblockThreshold,
		},
		Idle: IdleConfig{
			Enabled: v.GetBool("idle.enabled"),
			Linger:  idleLinger,
		},
	}

	instances, err := loadInstances(v, legacy)
	if err != nil {
		return nil, err
	}
	cfg.Instances = instances
	cfg.LegacyRoot = legacy.root

	authCfg, err := loadAuth(v)
	if err != nil {
		return nil, err
	}
	cfg.Auth = authCfg

	upgradesCfg, err := loadUpgrades(v)
	if err != nil {
		return nil, err
	}
	cfg.Upgrades = upgradesCfg

	releaseCfg, err := loadRelease(v)
	if err != nil {
		return nil, err
	}
	cfg.Release = releaseCfg

	return cfg, nil
}

// parseRetention handles "7d" as well as standard Go duration strings.
func parseRetention(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// UpgradesConfig configures release checks and UI-applied upgrades.
type UpgradesConfig struct {
	GitHubRepo       string
	CheckInterval    time.Duration
	ApplyDir         string // UPGRADE_APPLY_DIR; empty disables applying
	HostFilesVersion string // OBS_DASHBOARD_HOST_FILES_VERSION
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func loadUpgrades(v *viper.Viper) (UpgradesConfig, error) {
	interval, err := time.ParseDuration(v.GetString("upgrades.check_interval"))
	if err != nil {
		return UpgradesConfig{}, fmt.Errorf("invalid UPGRADES_CHECK_INTERVAL %q: %w", v.GetString("upgrades.check_interval"), err)
	}
	if interval < 5*time.Minute {
		return UpgradesConfig{}, fmt.Errorf("upgrades.check_interval must be at least 5m, got %s", interval)
	}
	repo := strings.TrimSpace(v.GetString("upgrades.github_repo"))
	if !repoRe.MatchString(repo) {
		return UpgradesConfig{}, fmt.Errorf("upgrades.github_repo must be owner/name, got %q", repo)
	}
	return UpgradesConfig{
		GitHubRepo:       repo,
		CheckInterval:    interval,
		ApplyDir:         strings.TrimSpace(os.Getenv("UPGRADE_APPLY_DIR")),
		HostFilesVersion: strings.TrimSpace(os.Getenv("OBS_DASHBOARD_HOST_FILES_VERSION")),
	}, nil
}

// ReleaseConfig configures the Release tab, which drives the obs-packaging
// release workflows on GitHub. The feature is off without a token.
type ReleaseConfig struct {
	GitHubToken string // RELEASE_GITHUB_TOKEN: classic PAT with repo and workflow scopes
	GitHubRepo  string // owner/name of the obs-packaging repository
}

// Enabled reports whether the Release tab is configured.
func (r ReleaseConfig) Enabled() bool { return r.GitHubToken != "" }

func loadRelease(v *viper.Viper) (ReleaseConfig, error) {
	repo := strings.TrimSpace(v.GetString("release.github_repo"))
	if !repoRe.MatchString(repo) {
		return ReleaseConfig{}, fmt.Errorf("release.github_repo must be owner/name, got %q", repo)
	}
	return ReleaseConfig{
		GitHubToken: strings.TrimSpace(v.GetString("release.github_token")),
		GitHubRepo:  repo,
	}, nil
}
