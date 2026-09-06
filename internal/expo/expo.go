// Package expo wraps `npx expo export` to produce a JS bundle + assets for
// React Native CodePush, and parses its `metadata.json` output so the
// caller knows which files to upload and how they map onto an Expo Updates
// v1 manifest (launch asset vs. regular assets).
//
// Like the shorebird package's relationship to Flutter, this shells out to
// tooling the customer's own project already has installed (the Expo CLI,
// via npx) rather than reimplementing a JS bundler.
package expo

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsInstalled reports whether `npx` is available on PATH (expo itself is
// resolved on demand by npx from the project's node_modules/devDependencies).
func IsInstalled() bool {
	_, err := exec.LookPath("npx")
	return err == nil
}

// Run executes `npx expo <args...>` in dir, streaming output to the current
// process's stdout/stderr so the user sees Expo CLI's own progress output.
func Run(dir string, args ...string) error {
	cmd := exec.Command("npx", append([]string{"expo"}, args...)...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npx expo %v failed: %w", args, err)
	}
	return nil
}

// ExportMetadataAsset is a single non-bundle asset entry from metadata.json.
type ExportMetadataAsset struct {
	Path string `json:"path"` // e.g. "assets/<hash>", relative to the export output dir
	Ext  string `json:"ext"`  // e.g. "png" (no leading dot)
}

// ExportPlatformMetadata is the per-platform section of metadata.json.
type ExportPlatformMetadata struct {
	Bundle string                `json:"bundle"` // e.g. "bundles/android-<hash>.js", relative to the export output dir
	Assets []ExportMetadataAsset `json:"assets"`
}

// ExportMetadata mirrors the metadata.json produced by `npx expo export`.
type ExportMetadata struct {
	Version      int                               `json:"version"`
	Bundler      string                            `json:"bundler"`
	FileMetadata map[string]ExportPlatformMetadata `json:"fileMetadata"`
}

// ParseMetadata reads and parses <outputDir>/metadata.json.
func ParseMetadata(outputDir string) (*ExportMetadata, error) {
	data, err := os.ReadFile(filepath.Join(outputDir, "metadata.json"))
	if err != nil {
		return nil, fmt.Errorf(
			"could not read metadata.json in %s (did `expo export` run? try --output-dir): %w",
			outputDir, err,
		)
	}
	var meta ExportMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("could not parse metadata.json: %w", err)
	}
	return &meta, nil
}

// ManifestEntry describes one file to upload, and how it maps onto the
// Expo Updates v1 manifest (`launchAsset` + `assets[]`).
type ManifestEntry struct {
	FieldName     string // unique multipart field name for this file
	FilePath      string // absolute path on disk
	Key           string // manifest asset key
	IsLaunchAsset bool
	ContentType   string
	FileExtension string // no leading dot; empty for the launch asset
}

// BuildManifestEntries resolves every file `expo export` produced for the
// given platform (from metadata.json) into ManifestEntry values ready to
// upload.
func BuildManifestEntries(outputDir string, meta *ExportMetadata, platform string) ([]ManifestEntry, error) {
	platformMeta, ok := meta.FileMetadata[strings.ToLower(platform)]
	if !ok {
		return nil, fmt.Errorf("metadata.json has no entry for platform %q — did you pass --platform %s to `expo export`?", platform, strings.ToLower(platform))
	}

	entries := make([]ManifestEntry, 0, len(platformMeta.Assets)+1)

	bundlePath := filepath.Join(outputDir, platformMeta.Bundle)
	entries = append(entries, ManifestEntry{
		FieldName:     "file_0",
		FilePath:      bundlePath,
		Key:           strings.TrimSuffix(filepath.Base(platformMeta.Bundle), filepath.Ext(platformMeta.Bundle)),
		IsLaunchAsset: true,
		ContentType:   "application/javascript",
	})

	for i, asset := range platformMeta.Assets {
		assetPath := filepath.Join(outputDir, asset.Path)
		entries = append(entries, ManifestEntry{
			FieldName:     fmt.Sprintf("file_%d", i+1),
			FilePath:      assetPath,
			Key:           filepath.Base(asset.Path),
			IsLaunchAsset: false,
			ContentType:   contentTypeForExtension(asset.Ext),
			FileExtension: asset.Ext,
		})
	}

	return entries, nil
}

var extensionContentTypes = map[string]string{
	"png":   "image/png",
	"jpg":   "image/jpeg",
	"jpeg":  "image/jpeg",
	"gif":   "image/gif",
	"webp":  "image/webp",
	"svg":   "image/svg+xml",
	"ttf":   "font/ttf",
	"otf":   "font/otf",
	"woff":  "font/woff",
	"woff2": "font/woff2",
	"json":  "application/json",
	"mp4":   "video/mp4",
	"mp3":   "audio/mpeg",
	"wav":   "audio/wav",
	"html":  "text/html",
}

func contentTypeForExtension(ext string) string {
	if ct, ok := extensionContentTypes[strings.ToLower(strings.TrimPrefix(ext, "."))]; ok {
		return ct
	}
	return "application/octet-stream"
}
