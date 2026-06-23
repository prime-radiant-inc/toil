package engine

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ArtifactMissingError struct {
	Missing []string
}

func (err *ArtifactMissingError) Error() string {
	return fmt.Sprintf("missing artifacts: %s", strings.Join(err.Missing, ", "))
}

func collectArtifacts(runDir string, nodeID string, workspace string, artifacts []string) ([]string, error) {
	if len(artifacts) == 0 {
		return nil, nil
	}
	artifactDir := filepath.Join(runDir, "artifacts", nodeID)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, err
	}

	collected := make([]string, 0, len(artifacts))
	missing := make([]string, 0)
	for _, artifact := range artifacts {
		source := artifact
		if !filepath.IsAbs(source) {
			source = filepath.Join(workspace, source)
		}
		destination := filepath.Join(artifactDir, filepath.Base(source))
		if err := copyFile(source, destination); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missing = append(missing, source)
				continue
			}
			return nil, fmt.Errorf("copy artifact %s: %w", source, err)
		}
		collected = append(collected, destination)
	}
	if len(missing) > 0 {
		return nil, &ArtifactMissingError{Missing: missing}
	}
	return collected, nil
}

func copyFile(source string, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()

	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer func() { _ = output.Close() }()

	_, err = io.Copy(output, input)
	return err
}
