package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexDSNRedaction(t *testing.T) {
	cfg := SetDefaults(false)
	cfg.Server.IndexSqlConfig.DSN = "postgres://test:private-test-password@localhost/index"
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-test-password") {
		t.Fatal("DSN leaked in JSON settings")
	}
	yaml, err := GenerateConfigYaml(&cfg, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(yaml, "private-test-password") {
		t.Fatal("DSN leaked in YAML settings")
	}
}

func TestIndexEnvironmentOverrides(t *testing.T) {
	previous, previousEnv := Config, Env
	t.Cleanup(func() { Config, Env = previous, previousEnv })
	for _, missingFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "yaml", true: "defaults"}[missingFile], func(t *testing.T) {
			Config, Env = Settings{}, Environment{}
			t.Setenv("FILEBROWSER_INDEX_DRIVER", "postgres")
			t.Setenv("FILEBROWSER_INDEX_DSN", "postgres://test:password@localhost/index")
			t.Setenv("FILEBROWSER_INDEX_MODE", "size")
			t.Setenv("FILEBROWSER_INDEX_INCLUDE_HIDDEN", "true")
			t.Setenv("FILEBROWSER_SHOW_HIDDEN", "true")
			path := filepath.Join(t.TempDir(), "config.yaml")
			if !missingFile {
				if err := os.WriteFile(path, []byte("server:\n  sources:\n    - path: .\n  indexSqlConfig:\n    driver: sqlite\n    mode: full\n    includeHidden: false\nuserDefaults:\n  listing:\n    showHidden: false\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := loadConfigWithDefaults(path, false); err != nil {
				t.Fatal(err)
			}
			cfg := Config.Server.IndexSqlConfig
			if cfg.Driver != "postgres" || cfg.Mode != "size" || cfg.DSN == "" || !cfg.IncludeHidden {
				t.Fatalf("overrides not applied: driver=%s mode=%s hidden=%v", cfg.Driver, cfg.Mode, cfg.IncludeHidden)
			}
			if !Config.UserDefaults.Listing.ShowHidden {
				t.Fatal("default visibility not applied")
			}
			stored, err := ApplyConfigSpecifiedPathsToUserDefaults(UserDefaults{}, Config.UserDefaults)
			if err != nil || !stored.Listing.ShowHidden {
				t.Fatalf("stored defaults overlay: %v", err)
			}
			if err := ValidateConfig(Config); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FILEBROWSER_INDEX_INCLUDE_HIDDEN", "false")
			t.Setenv("FILEBROWSER_SHOW_HIDDEN", "false")
			if err := loadIndexEnvConfig(); err != nil {
				t.Fatal(err)
			}
			if Config.Server.IndexSqlConfig.IncludeHidden || Config.UserDefaults.Listing.ShowHidden {
				t.Fatal("explicit false ignored")
			}
		})
	}
}

func TestInvalidIndexConfiguration(t *testing.T) {
	previous := Config
	t.Cleanup(func() { Config = previous })
	Config = SetDefaults(false)
	t.Setenv("FILEBROWSER_INDEX_INCLUDE_HIDDEN", "not-a-bool")
	if err := loadIndexEnvConfig(); err == nil {
		t.Fatal("expected invalid boolean error")
	}
	for _, cfg := range []IndexSqlConfig{
		{Driver: "postgres"},
		{Driver: "postgres", DSN: "postgres://localhost/test", DisableReuse: true},
		{Driver: "unknown"},
		{Mode: "unknown"},
	} {
		candidate := SetDefaults(false)
		candidate.Server.Sources = []*Source{{Path: "."}}
		candidate.Server.IndexSqlConfig = cfg
		if err := ValidateConfig(candidate); err == nil {
			t.Fatalf("accepted invalid config driver=%s mode=%s", cfg.Driver, cfg.Mode)
		}
	}
}
