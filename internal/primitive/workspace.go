package primitive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options configures built-in primitive behavior.
type Options struct {
	AllowedCommands []string
	DefaultTimeout  int
}

// DefaultOptions returns the built-in primitive defaults.
func DefaultOptions() Options {
	return Options{
		DefaultTimeout: 30,
	}
}

type workspacePathResolver struct {
	root string
}

func newWorkspacePathResolver(root string) workspacePathResolver {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = filepath.Clean(root)
	}

	if evalRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = evalRoot
	}

	return workspacePathResolver{root: filepath.Clean(absRoot)}
}

func (w workspacePathResolver) Root() string {
	return w.root
}

func (w workspacePathResolver) Resolve(path string) (string, error) {
	if path == "" {
		return "", &PrimitiveError{Code: ErrValidation, Message: "path is required"}
	}

	if containsParentTraversal(path) {
		return "", &PrimitiveError{
			Code:    ErrPermission,
			Message: fmt.Sprintf("path %s is outside workspace %s", path, w.root),
		}
	}

	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(w.root, candidate)
	}

	resolved, err := resolveWithSymlinkCheck(candidate)
	if err != nil {
		return "", &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}

	if !w.IsWithinRoot(resolved) {
		return "", &PrimitiveError{
			Code:    ErrPermission,
			Message: fmt.Sprintf("path %s is outside workspace %s", path, w.root),
		}
	}

	return resolved, nil
}

func (w workspacePathResolver) IsWithinRoot(path string) bool {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func containsParentTraversal(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return true
		}
	}
	return false
}

func resolveWithSymlinkCheck(path string) (string, error) {
	cleanPath := filepath.Clean(path)
	existingPath := cleanPath
	var remainder []string

	for {
		_, err := os.Lstat(existingPath)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("cannot inspect path %s: %w", existingPath, err)
		}

		parent := filepath.Dir(existingPath)
		if parent == existingPath {
			return "", fmt.Errorf("workspace root does not exist: %s", cleanPath)
		}

		remainder = append([]string{filepath.Base(existingPath)}, remainder...)
		existingPath = parent
	}

	resolvedExisting, err := filepath.EvalSymlinks(existingPath)
	if err != nil {
		return "", fmt.Errorf("cannot resolve symlinks for %s: %w", existingPath, err)
	}

	if len(remainder) == 0 {
		return filepath.Clean(resolvedExisting), nil
	}

	parts := append([]string{resolvedExisting}, remainder...)
	return filepath.Clean(filepath.Join(parts...)), nil
}
