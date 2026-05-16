package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hjson/hjson-go/v4"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
)

func TestNormaliseMissingConfigFileCreatesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "yggdrasil.conf")

	cfg := config.GenerateConfig()
	if err := saveNormalisedConfigFile(cfg, configPath, false); err != nil {
		t.Fatalf("saveNormalisedConfigFile() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected written config file to be non-empty")
	}
	if strings.Contains(string(data), "AdminListen") {
		t.Fatal("expected normalized config file to omit AdminListen")
	}

	parsed := config.GenerateConfig()
	if err := hjson.Unmarshal(data, parsed); err != nil {
		t.Fatalf("hjson.Unmarshal() error = %v", err)
	}
}
