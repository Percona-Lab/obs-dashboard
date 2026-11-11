package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	os.Setenv("OBS_USERNAME", "testuser")
	os.Setenv("OBS_PASSWORD", "testpass")
	defer os.Unsetenv("OBS_USERNAME")
	defer os.Unsetenv("OBS_PASSWORD")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Poller.Interval != 2*time.Minute {
		t.Errorf("expected 2m, got %v", cfg.Poller.Interval)
	}
	if cfg.Store.EventRetention != 7*24*time.Hour {
		t.Errorf("expected 168h, got %v", cfg.Store.EventRetention)
	}
}

func TestLoadMissingUsername(t *testing.T) {
	os.Unsetenv("OBS_USERNAME")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing OBS_USERNAME")
	}
}

func TestLoadEnvOverride(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	os.Setenv("POLL_INTERVAL", "2m")
	defer os.Unsetenv("OBS_USERNAME")
	defer os.Unsetenv("POLL_INTERVAL")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Poller.Interval != 2*time.Minute {
		t.Errorf("expected 2m override, got %v", cfg.Poller.Interval)
	}
}

func TestTelemetryDefaults(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.Interval != 60*time.Second {
		t.Fatalf("interval = %v, want 60s", cfg.Telemetry.Interval)
	}
	if cfg.Telemetry.Enabled {
		t.Fatalf("enabled = true, want false by default")
	}
}

func TestTrafficReductionDefaults(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerPool.BackoffMax != 5*time.Minute {
		t.Errorf("BackoffMax = %v, want 5m", cfg.WorkerPool.BackoffMax)
	}
	if cfg.WorkerPool.BatchThreshold != 4 {
		t.Errorf("BatchThreshold = %d, want 4", cfg.WorkerPool.BatchThreshold)
	}
	if cfg.Instances[0].MinuteRequestBudget != 60 {
		t.Errorf("MinuteRequestBudget = %d, want 60", cfg.Instances[0].MinuteRequestBudget)
	}
}

func TestLoadUnblockerDefaultsAndOverride(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	defer os.Unsetenv("OBS_USERNAME")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Unblocker.Enabled {
		t.Error("unblocker should be disabled by default")
	}
	if cfg.Unblocker.Threshold != 30*time.Minute {
		t.Errorf("default threshold = %v, want 30m", cfg.Unblocker.Threshold)
	}

	os.Setenv("UNBLOCKER_ENABLED", "true")
	os.Setenv("UNBLOCKER_THRESHOLD", "45m")
	defer os.Unsetenv("UNBLOCKER_ENABLED")
	defer os.Unsetenv("UNBLOCKER_THRESHOLD")

	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Unblocker.Enabled || cfg.Unblocker.Threshold != 45*time.Minute {
		t.Errorf("override: enabled=%v threshold=%v, want true/45m", cfg.Unblocker.Enabled, cfg.Unblocker.Threshold)
	}
}

func TestLoadIdleDefaultsAndOverride(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	defer os.Unsetenv("OBS_USERNAME")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Idle.Enabled {
		t.Error("idle mode should be enabled by default")
	}
	if cfg.Idle.Linger != 5*time.Minute {
		t.Errorf("default linger = %v, want 5m", cfg.Idle.Linger)
	}

	os.Setenv("IDLE_ENABLED", "false")
	os.Setenv("IDLE_LINGER", "10m")
	defer os.Unsetenv("IDLE_ENABLED")
	defer os.Unsetenv("IDLE_LINGER")

	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Idle.Enabled || cfg.Idle.Linger != 10*time.Minute {
		t.Errorf("override: enabled=%v linger=%v, want false/10m", cfg.Idle.Enabled, cfg.Idle.Linger)
	}
}

func TestLoadMetricsRetention(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	defer os.Unsetenv("OBS_USERNAME")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.MetricsRetention != 30*24*time.Hour {
		t.Errorf("default metrics retention = %v, want 720h", cfg.Store.MetricsRetention)
	}

	os.Setenv("METRICS_RETENTION", "60d")
	defer os.Unsetenv("METRICS_RETENTION")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.MetricsRetention != 60*24*time.Hour {
		t.Errorf("override metrics retention = %v, want 1440h", cfg.Store.MetricsRetention)
	}
}

func TestTrafficReductionEnvOverride(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("WORKER_POOL_BACKOFF_MAX", "2m")
	t.Setenv("WORKER_POOL_BATCH_THRESHOLD", "8")
	t.Setenv("OBS_MINUTE_REQUEST_BUDGET", "30")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerPool.BackoffMax != 2*time.Minute {
		t.Errorf("BackoffMax = %v, want 2m", cfg.WorkerPool.BackoffMax)
	}
	if cfg.WorkerPool.BatchThreshold != 8 {
		t.Errorf("BatchThreshold = %d, want 8", cfg.WorkerPool.BatchThreshold)
	}
	if cfg.Instances[0].MinuteRequestBudget != 30 {
		t.Errorf("MinuteRequestBudget = %d, want 30", cfg.Instances[0].MinuteRequestBudget)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLegacyInstanceFallback(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	defer os.Unsetenv("OBS_USERNAME")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("want 1 legacy instance, got %d", len(cfg.Instances))
	}
	in := cfg.Instances[0]
	if in.Name != "openSUSE" || in.Slug != "opensuse" || in.Root != "isv:percona" {
		t.Errorf("unexpected legacy identity: %+v", in)
	}
	if in.APIURL != "https://api.opensuse.org" || in.WebURL != "https://build.opensuse.org" ||
		in.DownloadURL != "https://download.opensuse.org/repositories" || in.Registry != "registry.opensuse.org" {
		t.Errorf("unexpected legacy hosts: %+v", in)
	}
	if in.MQ.Exchange != "pubsub" || in.MQ.RoutingPrefix != "opensuse.obs" || in.MinuteRequestBudget != 60 {
		t.Errorf("unexpected legacy MQ/budget: %+v", in)
	}
	if in.Username != "u" {
		t.Errorf("legacy username = %q", in.Username)
	}
	if cfg.LegacyRoot != "isv:percona" {
		t.Errorf("LegacyRoot = %q", cfg.LegacyRoot)
	}
}

const twoInstances = `
obs_instances:
  - name: openSUSE
    root: "isv:percona"
    api_url: "https://api.opensuse.org/"
    web_url: "https://build.opensuse.org/"
    download_url: "https://download.opensuse.org/repositories"
    registry: "registry.opensuse.org"
    username: "fileuser"
    mq:
      url: "amqps://a"
  - name: Percona OBS
    root: "percona"
    api_url: "https://api.obs.example.com"
    web_url: "https://obs.example.com"
    download_url: "https://download.obs.example.com/repositories"
    registry: "registry.obs.example.com"
    minute_request_budget: 10
    mq:
      url: "amqps://b"
      exchange: "events"
      routing_prefix: "percona.obs"
`

func TestLoadTwoInstances(t *testing.T) {
	os.Setenv("CONFIG_FILE", writeConfig(t, twoInstances))
	os.Setenv("OBS_PERCONA_OBS_USERNAME", "envuser")
	os.Setenv("OBS_PERCONA_OBS_PASSWORD", "envpass")
	defer os.Unsetenv("CONFIG_FILE")
	defer os.Unsetenv("OBS_PERCONA_OBS_USERNAME")
	defer os.Unsetenv("OBS_PERCONA_OBS_PASSWORD")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 2 {
		t.Fatalf("want 2 instances, got %d", len(cfg.Instances))
	}
	a, b := cfg.Instances[0], cfg.Instances[1]
	if a.Slug != "opensuse" || a.APIURL != "https://api.opensuse.org" || a.WebURL != "https://build.opensuse.org" {
		t.Errorf("instance a: %+v", a)
	}
	if a.MQ.Exchange != "pubsub" || a.MQ.RoutingPrefix != "opensuse.obs" || a.MinuteRequestBudget != 60 {
		t.Errorf("instance a defaults: %+v", a)
	}
	if a.Username != "fileuser" {
		t.Errorf("instance a username = %q", a.Username)
	}
	if b.Slug != "percona_obs" || b.Root != "percona" || b.MinuteRequestBudget != 10 {
		t.Errorf("instance b: %+v", b)
	}
	if b.MQ.Exchange != "events" || b.MQ.RoutingPrefix != "percona.obs" {
		t.Errorf("instance b MQ: %+v", b.MQ)
	}
	if b.Username != "envuser" || b.Password != "envpass" {
		t.Errorf("instance b env creds: %q/%q", b.Username, b.Password)
	}
}

func TestLoadInstanceValidation(t *testing.T) {
	cases := map[string]string{
		"duplicate slug": `
obs_instances:
  - {name: A, root: r, api_url: x, web_url: x, download_url: x, registry: x, username: u, mq: {url: x}}
  - {name: a, root: s, api_url: x, web_url: x, download_url: x, registry: x, username: u, mq: {url: x}}
`,
		"empty root": `
obs_instances:
  - {name: A, api_url: x, web_url: x, download_url: x, registry: x, username: u, mq: {url: x}}
`,
		"missing registry": `
obs_instances:
  - {name: A, root: r, api_url: x, web_url: x, download_url: x, username: u, mq: {url: x}}
`,
		"missing mq url": `
obs_instances:
  - {name: A, root: r, api_url: x, web_url: x, download_url: x, registry: x, username: u}
`,
		"missing username": `
obs_instances:
  - {name: A, root: r, api_url: x, web_url: x, download_url: x, registry: x, mq: {url: x}}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			os.Setenv("CONFIG_FILE", writeConfig(t, body))
			defer os.Unsetenv("CONFIG_FILE")
			if _, err := Load(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"openSUSE": "opensuse", "Percona OBS": "percona_obs", "x-1": "x_1"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMQDialURL(t *testing.T) {
	cases := []struct {
		name string
		mq   MQInstanceConfig
		want string
	}{
		{"no fields keeps URL", MQInstanceConfig{URL: "amqps://opensuse:opensuse@rabbit.opensuse.org:5671/"},
			"amqps://opensuse:opensuse@rabbit.opensuse.org:5671/"},
		{"fields injected, vhost kept", MQInstanceConfig{URL: "amqps://obs.example.com:5671/obs", Username: "obs-dashboard", Password: "s3cret"},
			"amqps://obs-dashboard:s3cret@obs.example.com:5671/obs"},
		{"fields replace URL credentials", MQInstanceConfig{URL: "amqps://old:oldpw@obs.example.com:5671/obs", Username: "new", Password: "newpw"},
			"amqps://new:newpw@obs.example.com:5671/obs"},
		{"password only keeps URL username", MQInstanceConfig{URL: "amqps://user:oldpw@obs.example.com:5671/obs", Password: "newpw"},
			"amqps://user:newpw@obs.example.com:5671/obs"},
		{"special characters are escaped", MQInstanceConfig{URL: "amqps://obs.example.com:5671/obs", Username: "u", Password: "p@ss/w:rd"},
			"amqps://u:p%40ss%2Fw%3Ard@obs.example.com:5671/obs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.mq.DialURL()
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("DialURL() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMQDialURLInvalidDoesNotLeakPassword(t *testing.T) {
	_, err := MQInstanceConfig{URL: "amqps://u:topsecret@host:bad-port/obs"}.DialURL()
	if err == nil {
		t.Fatal("expected an error for an invalid mq.url")
	}
	if strings.Contains(err.Error(), "topsecret") {
		t.Errorf("error leaks the password: %v", err)
	}
}

const labsMQInstance = `
obs_instances:
  - name: labs
    root: "isv:percona"
    api_url: "https://obs.example.com"
    web_url: "https://obs.example.com"
    download_url: "https://download.obs.example.com"
    registry: "registry.obs.example.com"
    username: "u"
    mq:
      url: "amqps://obs.example.com:5671/obs"
      exchange: "obs.events"
      routing_prefix: "percona.obs"
      username: "fileuser"
      password: "filepass"
`

func TestLoadInstanceMQCredentials(t *testing.T) {
	os.Setenv("CONFIG_FILE", writeConfig(t, labsMQInstance))
	defer os.Unsetenv("CONFIG_FILE")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	mq := cfg.Instances[0].MQ
	if mq.Username != "fileuser" || mq.Password != "filepass" {
		t.Errorf("file credentials = %q/%q", mq.Username, mq.Password)
	}

	os.Setenv("OBS_LABS_MQ_USERNAME", "envuser")
	os.Setenv("OBS_LABS_MQ_PASSWORD", "envpass")
	defer os.Unsetenv("OBS_LABS_MQ_USERNAME")
	defer os.Unsetenv("OBS_LABS_MQ_PASSWORD")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Instances[0].MQ.DialURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "amqps://envuser:envpass@obs.example.com:5671/obs" {
		t.Errorf("env override dial URL = %q", got)
	}
}

func TestLoadInstanceInvalidMQURL(t *testing.T) {
	os.Setenv("CONFIG_FILE", writeConfig(t, strings.Replace(labsMQInstance,
		`"amqps://obs.example.com:5671/obs"`, `"amqps://obs.example.com:bad/obs"`, 1)))
	defer os.Unsetenv("CONFIG_FILE")
	if _, err := Load(); err == nil {
		t.Fatal("expected a validation error for an invalid mq.url")
	}
}

func TestAuthDefaults(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.SessionTTL != 168*time.Hour {
		t.Errorf("session ttl: got %v", cfg.Auth.SessionTTL)
	}
	if cfg.Auth.Enabled() {
		t.Error("auth enabled without GOOGLE_CLIENT_ID")
	}
	if len(cfg.Auth.Admins) != 0 {
		t.Errorf("admins: %v", cfg.Auth.Admins)
	}
}

func TestAuthAdminsFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("auth:\n  admins: [\"root@percona.com\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("OBS_DASHBOARD_ADMINS", " a@percona.com, ,B@percona.com ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"root@percona.com", "a@percona.com", "B@percona.com"}
	if strings.Join(cfg.Auth.Admins, ",") != strings.Join(want, ",") {
		t.Errorf("admins: got %v want %v", cfg.Auth.Admins, want)
	}
}

func TestAuthEnabledValidation(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("GOOGLE_CLIENT_ID", "id")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "GOOGLE_CLIENT_SECRET") {
		t.Fatalf("missing secret: got %v", err)
	}
	t.Setenv("GOOGLE_CLIENT_SECRET", "secret")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "auth.public_url") {
		t.Fatalf("missing public_url: got %v", err)
	}
	t.Setenv("AUTH_PUBLIC_URL", "dashboard.example")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "auth.public_url") {
		t.Fatalf("relative public_url: got %v", err)
	}
	t.Setenv("AUTH_PUBLIC_URL", "https://dashboard.obs.pg.labs.percona.com/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Auth.Enabled() || cfg.Auth.PublicURL != "https://dashboard.obs.pg.labs.percona.com" {
		t.Fatalf("auth: %+v", cfg.Auth)
	}
	if cfg.Auth.GoogleClientID != "id" || cfg.Auth.GoogleClientSecret != "secret" {
		t.Fatalf("credentials not loaded: %+v", cfg.Auth)
	}
}

func TestReleaseConfigDefaultsAndEnv(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("RELEASE_GITHUB_TOKEN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Release.GitHubRepo != "percona/obs-packaging" {
		t.Errorf("GitHubRepo = %q, want percona/obs-packaging", cfg.Release.GitHubRepo)
	}
	if cfg.Release.Enabled() {
		t.Error("release must be disabled without a token")
	}

	t.Setenv("RELEASE_GITHUB_TOKEN", "ghp_x")
	t.Setenv("RELEASE_GITHUB_REPO", "example/fork")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Release.Enabled() {
		t.Error("token from env must enable release")
	}
	if cfg.Release.GitHubToken != "ghp_x" || cfg.Release.GitHubRepo != "example/fork" {
		t.Errorf("Release = %+v", cfg.Release)
	}
}

func TestReleaseRepoValidation(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("RELEASE_GITHUB_REPO", "not-a-repo")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "release.github_repo") {
		t.Fatalf("expected release.github_repo error, got %v", err)
	}
}
