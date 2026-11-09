package extension

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"home-control/internal/store"
)

const (
	maxDownload    = 64 << 20
	downloadTime   = 2 * time.Minute
	githubArchives = "https://codeload.github.com"
)

// github:owner/repo[/folder]@ref. The ref is required, so what runs in your home can't change by itself.
var githubSource = regexp.MustCompile(`^github:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)((?:/[A-Za-z0-9_.-]+)*)@([A-Za-z0-9_./-]+)$`)

// resolve returns the local folder holding a source, downloading it the first time.
func (m *Manager) resolve(ctx context.Context, source string) (string, error) {
	if strings.HasPrefix(source, "upload:") {
		if !uploadSource.MatchString(source) {
			return "", fmt.Errorf("source %q is not an uploaded file", source)
		}
		return m.unpacked(ctx, strings.TrimPrefix(source, "upload:"), nil)
	}
	if strings.HasPrefix(source, "market:") {
		key, ok := packageKey(source)
		if !ok {
			return "", fmt.Errorf("source %q is not from the marketplace", source)
		}
		return m.unpacked(ctx, key, nil) // never fetched here: it was checked and stored when it was inspected
	}
	if !strings.HasPrefix(source, "github:") {
		if !strings.HasPrefix(source, "./") && !filepath.IsAbs(source) {
			return "", fmt.Errorf(`source %q must be "./folder" inside the config folder, or github:owner/repo@ref`, source)
		}
		if filepath.IsAbs(source) {
			return source, nil
		}
		return filepath.Join(m.configDir, source), nil
	}
	match := githubSource.FindStringSubmatch(source)
	if match == nil {
		return "", fmt.Errorf("source %q must look like github:owner/repo@ref or github:owner/repo/folder@ref", source)
	}
	owner, repo, folder, ref := match[1], match[2], match[3], match[4]
	if strings.Contains(ref, "..") || owner == ".." || repo == ".." || strings.Contains(folder, "/../") || strings.HasSuffix(folder, "/..") {
		return "", fmt.Errorf("source %q has a bad path", source)
	}
	// Keyed by repo and ref: once downloaded a version stays as it was, even if the tag later moves. Change the ref to update.
	key := sha256Hex([]byte(owner + "/" + repo + "@" + ref))
	root, err := m.unpacked(ctx, key, func() ([]byte, error) {
		archive, err := m.download(ctx, owner, repo, ref)
		if err != nil {
			return nil, err
		}
		return archive, m.pinContent(ctx, key, source, archive)
	})
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(folder)), nil
}

// unpacked returns the folder holding a package's code. The store keeps the archive, so the code survives losing the
// cache; only the first time does it fetch it.
func (m *Manager) unpacked(ctx context.Context, key string, fetch func() ([]byte, error)) (string, error) {
	root := filepath.Join(m.cacheDir, "extensions", key)
	if _, err := os.Stat(root); err == nil {
		return root, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	stored, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	archive, err := m.store.Package(stored, key)
	if errors.Is(err, store.ErrNotFound) {
		if fetch == nil {
			return "", errors.New("this package is no longer stored; add it again")
		}
		if archive, err = fetch(); err != nil {
			return "", err
		}
		if err := m.store.SavePackage(stored, key, archive); err != nil {
			return "", fmt.Errorf("could not save the package: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("could not read the package: %w", err)
	}
	if want, ok := strings.CutPrefix(key, "market-"); ok {
		if sha256Hex(archive) != want {
			return "", errors.New("the stored copy of this package isn't the one the marketplace verified, so it won't run. Remove it and add it again")
		}
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o750); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(root), ".unpack-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp) // does nothing once renamed
	if err := extract(bytes.NewReader(archive), tmp); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o700); err != nil { // #nosec G302 -- a folder, owner only; it needs x to be opened
		return "", err
	}
	return root, os.Rename(tmp, root)
}

// pinContent records what a github: source held the first time it was downloaded, and refuses a later download that differs,
// so losing the stored copy can't quietly pick up a tag or branch that moved.
// shortcut: the record sits beside the package, so wiping the whole store forgets it; that also forgets every approval.
func (m *Manager) pinContent(ctx context.Context, key, source string, archive []byte) error {
	got, err := contentHash(archive)
	if err != nil {
		return err
	}
	pinKey := key + ".content"
	stored, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	pinned, err := m.store.Package(stored, pinKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return m.store.SavePackage(stored, pinKey, []byte(got))
	case err != nil:
		return err
	case string(pinned) != got:
		return fmt.Errorf("%s has changed since it was first downloaded: the tag or branch was moved. Remove it and add the version you mean", source)
	}
	return nil
}

// contentHash covers the files of an archive and not its bytes, since GitHub doesn't promise to compress the same code the
// same way twice. The top folder is left out because its name is GitHub's choice.
func contentHash(archive []byte) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return "", fmt.Errorf("not an archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		_, name, found := strings.Cut(strings.TrimPrefix(h.Name, "./"), "/")
		if h.Typeflag != tar.TypeReg || !found {
			continue
		}
		sum := sha256.New()
		if _, err := io.Copy(sum, io.LimitReader(tr, maxDownload)); err != nil {
			return "", err
		}
		files[name] = hex.EncodeToString(sum.Sum(nil))
	}
	all := sha256.New()
	for _, name := range sorted(files) {
		fmt.Fprintf(all, "%q %s\n", name, files[name])
	}
	return hex.EncodeToString(all.Sum(nil)), nil
}

func (m *Manager) download(ctx context.Context, owner, repo, ref string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTime)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.archives+"/"+owner+"/"+repo+"/tar.gz/"+ref, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github answered %s for %s/%s@%s", resp.Status, owner, repo, ref)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(archive) > maxDownload {
		return nil, fmt.Errorf("the download is over %d bytes", maxDownload)
	}
	return archive, nil
}

// Limits on what an archive may hold beyond its bytes.
const (
	maxArchiveEntries = 5000
	maxArchiveDepth   = 32
)

// extract unpacks a GitHub archive, dropping its top-level folder. Only plain files and folders come out: a link
// could point at the credentials, and a path with .. could write anywhere.
func extract(r io.Reader, into string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("not an archive: %w", err)
	}
	defer gz.Close()
	archive := tar.NewReader(gz)
	var total int64
	entries := 0
	for {
		h, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		// Empty files and folders cost nothing to compress but each one is a real inode: count them, not just bytes.
		if entries++; entries > maxArchiveEntries {
			return fmt.Errorf("archive has over %d entries", maxArchiveEntries)
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			continue
		}
		_, rest, found := strings.Cut(strings.TrimPrefix(h.Name, "./"), "/")
		clean := path.Clean(rest)
		if !found || rest == "" || clean == "." {
			continue
		}
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("archive has an unsafe path: %s", h.Name)
		}
		if strings.Count(clean, "/") >= maxArchiveDepth {
			return fmt.Errorf("archive nests folders over %d deep", maxArchiveDepth)
		}
		target := filepath.Join(into, filepath.FromSlash(clean))
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
		}
		if total += h.Size; total > maxDownload {
			return fmt.Errorf("archive unpacks to over %d bytes", maxDownload)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) // #nosec G304 -- target is inside into, checked above
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(archive, h.Size))
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
}

// sha256Hex is how every downloaded or stored package and image is named and checked.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
