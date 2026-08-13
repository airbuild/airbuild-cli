package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigFile is the name of the project-level config file.
const ConfigFile = ".airbuild.json"

// PlatformBuilds holds build output paths for a single platform.
type PlatformBuilds struct {
	Debug   string `json:"debug,omitempty"`
	Release string `json:"release,omitempty"`
}

// ProjectConfig is the project-level config stored at .airbuild.json.
// Created by `airbuild init` and used by `airbuild push`.
type ProjectConfig struct {
	AppID  string                    `json:"appId"`
	Builds map[string]PlatformBuilds `json:"builds"`
}

// Load reads the project config from the current directory.
// Returns an error if the file does not exist.
func Load() (*ProjectConfig, error) {
	return LoadFrom(ConfigFile)
}

// LoadFrom reads the project config from a specific path.
func LoadFrom(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no %s found. Run `airbuild init` first", path)
		}
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}

	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("could not parse %s: %w", path, err)
	}
	if cfg.AppID == "" {
		return nil, fmt.Errorf("invalid config: appId is missing")
	}
	// Ensure Builds map is initialized (handles "builds": null in JSON)
	if cfg.Builds == nil {
		cfg.Builds = make(map[string]PlatformBuilds)
	}
	// Normalize platform keys to lowercase (handles "Android" vs "android")
	normalized := make(map[string]PlatformBuilds)
	for k, v := range cfg.Builds {
		normalized[strings.ToLower(k)] = v
	}
	cfg.Builds = normalized
	return &cfg, nil
}

// Save writes the project config to the current directory.
func (c *ProjectConfig) Save() error {
	return c.SaveTo(ConfigFile)
}

// SaveTo writes the project config to a specific path.
func (c *ProjectConfig) SaveTo(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("could not marshal config: %w", err)
	}
	// Add trailing newline
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	return nil
}

// HasPlatform returns true if the config has build paths for a platform.
func (c *ProjectConfig) HasPlatform(platform string) bool {
	_, ok := c.Builds[platform]
	return ok
}

// GetBuildPath returns the file path for a platform and build type.
// buildType is "debug" or "release".
func (c *ProjectConfig) GetBuildPath(platform, buildType string) string {
	pb, ok := c.Builds[platform]
	if !ok {
		return ""
	}
	switch buildType {
	case "debug":
		return pb.Debug
	case "release":
		return pb.Release
	default:
		return ""
	}
}

// ConfiguredPlatforms returns the list of platforms configured in the config.
func (c *ProjectConfig) ConfiguredPlatforms() []string {
	var platforms []string
	if _, ok := c.Builds["ios"]; ok {
		platforms = append(platforms, "ios")
	}
	if _, ok := c.Builds["android"]; ok {
		platforms = append(platforms, "android")
	}
	return platforms
}

// Exists checks if a project config exists in the current directory.
func Exists() bool {
	_, err := os.Stat(filepath.Join(".", ConfigFile))
	return err == nil
}
