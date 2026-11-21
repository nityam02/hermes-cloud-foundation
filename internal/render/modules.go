package render

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestFile records which module set an extracted modules directory contains.
const ManifestFile = ".hermes-modules.json"

// ErrModulesChanged is returned when an extracted modules directory does not match the
// modules embedded in this binary and replacing it was not requested.
var ErrModulesChanged = errors.New("modules directory does not match the embedded modules")

type manifest struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// DigestFS returns the sha256 over every regular file of fsys (sorted path, NUL, size,
// NUL, content). Dot-files and dot-directories are skipped, matching //go:embed.
func DigestFS(fsys fs.FS) (string, error) {
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ExtractModules makes dest contain exactly the module tree in src and returns its digest.
// An existing dest is reused when its manifest and content match src. It is never
// overwritten silently: a different or locally modified module set returns
// ErrModulesChanged unless replace is true.
func ExtractModules(src fs.FS, dest, version string, replace bool) (string, error) {
	digest, err := DigestFS(src)
	if err != nil {
		return "", fmt.Errorf("render: digest embedded modules: %w", err)
	}
	if _, err := os.Stat(dest); err == nil {
		state, err := checkExtracted(dest, digest)
		if err != nil {
			return "", err
		}
		if state == "" {
			return digest, nil
		}
		if !replace {
			return "", fmt.Errorf("%w: %s %s (re-run with --refresh-modules to replace it)", ErrModulesChanged, dest, state)
		}
		if err := os.RemoveAll(dest); err != nil {
			return "", fmt.Errorf("render: remove %s: %w", dest, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("render: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return "", fmt.Errorf("render: %w", err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".modules-tmp-")
	if err != nil {
		return "", fmt.Errorf("render: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	err = fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(tmp, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		in, err := src.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
	if err != nil {
		return "", fmt.Errorf("render: extract modules: %w", err)
	}
	m, _ := json.MarshalIndent(manifest{Version: version, Digest: digest}, "", "  ")
	if err := os.WriteFile(filepath.Join(tmp, ManifestFile), append(m, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("render: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", fmt.Errorf("render: install modules: %w", err)
	}
	return digest, nil
}

// checkExtracted returns "" when dest holds exactly the module set with digest, otherwise
// a short description of the mismatch.
func checkExtracted(dest, digest string) (string, error) {
	// F-03: the digest covers regular files only, but the engine follows symbolic links, so any
	// symlink or other special file (device, fifo, socket) in the extracted tree, or a dest that
	// is itself a symlink, means the tree was modified after extraction.
	if st, err := os.Lstat(dest); err != nil {
		return "", fmt.Errorf("render: %w", err)
	} else if st.Mode()&fs.ModeSymlink != 0 {
		return "is a symbolic link", nil
	}
	var special string
	err := filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			special = p
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("render: inspect %s: %w", dest, err)
	}
	if special != "" {
		rel, _ := filepath.Rel(dest, special)
		return fmt.Sprintf("was modified after extraction: %s is a symbolic link or special file", rel), nil
	}
	data, err := os.ReadFile(filepath.Join(dest, ManifestFile))
	if err != nil {
		return "has no manifest (unknown content)", nil
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "has an unreadable manifest", nil
	}
	if m.Digest != digest {
		return fmt.Sprintf("contains a different module set (digest %.12s, this binary embeds %.12s)", m.Digest, digest), nil
	}
	onDisk, err := DigestFS(os.DirFS(dest))
	if err != nil {
		return "", fmt.Errorf("render: digest %s: %w", dest, err)
	}
	if onDisk != digest {
		return "was modified after extraction", nil
	}
	return "", nil
}
