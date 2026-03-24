package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config represents the top-level configuration for local-docs-mcp.
type Config struct {
	Schema string       `json:"$schema,omitempty"`
	Repos  []RepoConfig `json:"repos"`
}

// RepoConfig defines a single repository to index.
type RepoConfig struct {
	URL   string   `json:"url"`
	Paths []string `json:"paths"`
	Alias string   `json:"alias"`
}

// LoadConfig reads a JSON config file from the given path, validates it,
// and returns the parsed Config. The path is resolved to an absolute path
// before reading, so relative paths work correctly.
func LoadConfig(path string) (*Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving config path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config JSON: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func validate(cfg *Config) error {
	if len(cfg.Repos) == 0 {
		return fmt.Errorf("config: repos must not be empty")
	}

	seen := make(map[string]bool)
	for i, repo := range cfg.Repos {
		if repo.URL == "" {
			return fmt.Errorf("repo at index %d: missing url", i)
		}
		if repo.Alias == "" {
			return fmt.Errorf("repo at index %d: missing alias", i)
		}
		if len(repo.Paths) == 0 {
			return fmt.Errorf("repo at index %d: missing paths", i)
		}
		for j, p := range repo.Paths {
			if p == "" {
				return fmt.Errorf("repo at index %d: path at index %d is empty", i, j)
			}
		}
		if seen[repo.Alias] {
			return fmt.Errorf("duplicate alias %q", repo.Alias)
		}
		seen[repo.Alias] = true
	}

	return nil
}
