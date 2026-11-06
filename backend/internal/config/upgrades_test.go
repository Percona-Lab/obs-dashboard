package config

import (
	"testing"
	"time"
)

func TestUpgradesDefaults(t *testing.T) {
	t.Setenv("CONFIG_FILE", "/nonexistent.yaml")
	t.Setenv("OBS_USERNAME", "u")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Upgrades.GitHubRepo != "Percona-Lab/obs-dashboard" || cfg.Upgrades.CheckInterval != time.Hour || cfg.Upgrades.ApplyDir != "" {
		t.Fatalf("upgrades = %+v", cfg.Upgrades)
	}
}

func TestUpgradesEnv(t *testing.T) {
	t.Setenv("CONFIG_FILE", "/nonexistent.yaml")
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("UPGRADES_GITHUB_REPO", "acme/dash")
	t.Setenv("UPGRADES_CHECK_INTERVAL", "30m")
	t.Setenv("UPGRADE_APPLY_DIR", "/apply")
	t.Setenv("OBS_DASHBOARD_HOST_FILES_VERSION", "v0.3.0")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	u := cfg.Upgrades
	if u.GitHubRepo != "acme/dash" || u.CheckInterval != 30*time.Minute || u.ApplyDir != "/apply" || u.HostFilesVersion != "v0.3.0" {
		t.Fatalf("upgrades = %+v", u)
	}
}

func TestUpgradesValidation(t *testing.T) {
	for name, env := range map[string][2]string{
		"short interval": {"UPGRADES_CHECK_INTERVAL", "1m"},
		"bad interval":   {"UPGRADES_CHECK_INTERVAL", "soon"},
		"bad repo":       {"UPGRADES_GITHUB_REPO", "not a repo"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CONFIG_FILE", "/nonexistent.yaml")
			t.Setenv("OBS_USERNAME", "u")
			t.Setenv(env[0], env[1])
			if _, err := Load(); err == nil {
				t.Fatal("want error")
			}
		})
	}
}
