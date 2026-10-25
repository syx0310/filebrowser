package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndexScheduleEnvironmentAndSourceOverrides(t *testing.T) {
	previous, previousEnv := Config, Env
	t.Cleanup(func() { Config, Env = previous, previousEnv })
	t.Setenv("FILEBROWSER_INDEX_SCAN_INTERVAL", "90m")
	t.Setenv("FILEBROWSER_INDEX_FULL_SCAN_EVERY", "8")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := `server:
  indexing:
    scanInterval: 6h
    fullScanEvery: 3
  sources:
    - path: .
    - path: /archive
      config:
        indexing:
          scanInterval: 24h
    - path: /live
      config:
        indexing:
          scanInterval: auto
          fullScanEvery: 1
`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadConfigWithDefaults(configPath, false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(Config); err != nil {
		t.Fatal(err)
	}
	wants := []IndexingConfig{{"90m", 8}, {"24h", 8}, {"auto", 1}}
	for i, source := range Config.Server.Sources {
		if got := source.Config.Indexing.Resolve(Config.Server.Indexing); got != wants[i] {
			t.Fatalf("source %d: %+v want %+v", i, got, wants[i])
		}
	}
	if err := loadConfigWithDefaults(filepath.Join(t.TempDir(), "missing.yaml"), false); err != nil {
		t.Fatal(err)
	}
	if got := (IndexingConfig{}).Resolve(Config.Server.Indexing); got != wants[0] {
		t.Fatalf("environment without config file: %+v", got)
	}
}

func TestInvalidIndexSchedules(t *testing.T) {
	for _, interval := range []string{"5", "1d", "0", "0s", "-1h", "59s", "999999999999999999h"} {
		for _, perSource := range []bool{false, true} {
			cfg := SetDefaults(false)
			cfg.Server.Sources = []*Source{{Path: "."}}
			if perSource {
				cfg.Server.Sources[0].Config.Indexing.ScanInterval = interval
			} else {
				cfg.Server.Indexing.ScanInterval = interval
			}
			if err := ValidateConfig(cfg); err == nil {
				t.Fatalf("accepted interval %q (source=%v)", interval, perSource)
			}
		}
	}
	for _, perSource := range []bool{false, true} {
		cfg := SetDefaults(false)
		cfg.Server.Sources = []*Source{{Path: "."}}
		if perSource {
			cfg.Server.Sources[0].Config.Indexing.FullScanEvery = -1
		} else {
			cfg.Server.Indexing.FullScanEvery = -1
		}
		if err := ValidateConfig(cfg); err == nil {
			t.Fatal("accepted negative full scan cycle")
		}
	}
	previous := Config
	t.Cleanup(func() { Config = previous })
	for _, value := range []string{"-1", "1.5", "invalid"} {
		t.Setenv("FILEBROWSER_INDEX_FULL_SCAN_EVERY", value)
		if err := loadIndexEnvConfig(); err == nil {
			t.Fatalf("accepted full scan cycle %q", value)
		}
	}
}
