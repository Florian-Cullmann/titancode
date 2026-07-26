package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxDiffBytes = 2 << 20

func (s *Scanner) Diff(ctx context.Context, path, mode string) (Diff, error) {
	path, err := s.changedPath(ctx, path)
	if err != nil {
		return Diff{}, err
	}
	if mode != "working" && mode != "staged" {
		return Diff{}, errors.New("mode must be working or staged")
	}

	args := []string{"diff", "--no-ext-diff", "--unified=3"}
	if mode == "staged" {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	output, err := s.gitWithExitCodeOne(ctx, args...)
	if err != nil {
		return Diff{}, fmt.Errorf("read diff: %w", err)
	}

	if mode == "working" && len(output) == 0 && s.isUntracked(ctx, path) {
		output, err = s.gitWithExitCodeOne(ctx, "diff", "--no-ext-diff", "--no-index", "--unified=3", "--", "/dev/null", path)
		if err != nil {
			return Diff{}, fmt.Errorf("read untracked diff: %w", err)
		}
	}

	diff := Diff{Path: path, Mode: mode, Binary: bytes.Contains(output, []byte("Binary files "))}
	if len(output) > maxDiffBytes {
		output = output[:maxDiffBytes]
		diff.Truncated = true
	}
	diff.Content = string(output)
	return diff, nil
}

func (s *Scanner) Stage(ctx context.Context, path string) error {
	path, err := s.changedPath(ctx, path)
	if err != nil {
		return err
	}
	if _, err := s.git(ctx, "add", "--", path); err != nil {
		return fmt.Errorf("stage file: %w", err)
	}
	return nil
}

func (s *Scanner) Unstage(ctx context.Context, path string) error {
	path, err := s.changedPath(ctx, path)
	if err != nil {
		return err
	}
	if _, err := s.git(ctx, "restore", "--staged", "--", path); err == nil {
		return nil
	}
	if _, err := s.git(ctx, "reset", "--", path); err != nil {
		return fmt.Errorf("unstage file: %w", err)
	}
	return nil
}

func (s *Scanner) changedPath(ctx context.Context, path string) (string, error) {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
		return "", errors.New("invalid repository path")
	}
	for _, change := range s.gitChanges(ctx) {
		if change.Path == path {
			return path, nil
		}
	}
	return "", errors.New("path is not part of the current change set")
}

func (s *Scanner) isUntracked(ctx context.Context, path string) bool {
	output, err := s.git(ctx, "ls-files", "--others", "--exclude-standard", "--", path)
	return err == nil && strings.TrimSpace(string(output)) == path
}

func (s *Scanner) gitWithExitCodeOne(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", s.root}, args...)...)
	output, err := command.Output()
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return output, nil
	}
	return output, err
}
