package extension

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"

	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// MaxUpload is the biggest file the page may send.
const MaxUpload = 16 << 20

// upload:<sha256 of the uploaded file>. The hash is the package's key in the store.
var uploadSource = regexp.MustCompile(`^upload:[0-9a-f]{64}$`)

// Upload stores a file picked on the page as a package and says what it is, without starting it. Like Inspect, the
// extension only becomes part of the home when it is added.
func (m *Manager) Upload(ctx context.Context, filename string, data []byte) (Status, error) {
	if len(data) > MaxUpload {
		return Status{}, fmt.Errorf("the file is over %d MiB", MaxUpload>>20)
	}
	files, err := unpackUpload(filename, data)
	if err != nil {
		return Status{}, err
	}
	archive, err := packFiles(files)
	if err != nil {
		return Status{}, err
	}
	key := sha256Hex(data)
	source := "upload:" + key
	stored, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := m.store.SavePackage(stored, key, archive); err != nil {
		return Status{}, fmt.Errorf("could not save the file: %w", err)
	}
	status, err := m.Inspect(ctx, source)
	if err != nil {
		if !m.uses(source) {
			m.dropUpload(key)
		}
		return Status{}, err
	}
	return status, nil
}

func (m *Manager) uses(source string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.ContainsFunc(m.instances, func(i *instance) bool { return i.entry.Source == source })
}

// dropUpload forgets a package and its unpacked copy.
func (m *Manager) dropUpload(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	err := m.store.DeletePackage(ctx, key)
	return errors.Join(err, os.RemoveAll(filepath.Join(m.cacheDir, "extensions", key)))
}

// unpackUpload returns the files of an upload with extension.ts's folder as the root.
func unpackUpload(filename string, data []byte) (map[string][]byte, error) {
	var files map[string][]byte
	var err error
	switch name := strings.ToLower(filename); {
	case strings.HasSuffix(name, ".zip"):
		files, err = readZip(data)
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		files, err = readTarGz(data)
	case strings.HasSuffix(name, ".ts"):
		return map[string][]byte{entryFile: data}, nil
	default:
		return nil, errors.New("upload a .zip, .tar.gz, .tgz or a single .ts file")
	}
	if err != nil {
		return nil, err
	}
	return rootAtEntry(files)
}

// safeName cleans an archive path, and says no to ones that could escape the folder. ok is false for junk to skip.
func safeName(raw string) (name string, ok bool, err error) {
	if strings.ContainsAny(raw, "\\\x00") {
		return "", false, fmt.Errorf("the file has an unsafe path: %q", raw)
	}
	clean := path.Clean(strings.TrimPrefix(raw, "./"))
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false, fmt.Errorf("the file has an unsafe path: %q", raw)
	}
	if clean == "." || strings.HasPrefix(clean, "__MACOSX/") || strings.HasPrefix(path.Base(clean), "._") {
		return "", false, nil
	}
	return clean, true, nil
}

// unpacker collects regular files and keeps the total under maxDownload, however big the archive claims to be.
type unpacker struct {
	files map[string][]byte
	total int64
}

func (u *unpacker) add(raw string, r io.Reader) error {
	name, ok, err := safeName(raw)
	if err != nil || !ok {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(r, maxDownload-u.total+1))
	if err != nil {
		return err
	}
	if u.total += int64(len(data)); u.total > maxDownload {
		return fmt.Errorf("the file unpacks to over %d MiB", maxDownload>>20)
	}
	u.files[name] = data
	return nil
}

func readZip(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a zip file: %w", err)
	}
	u := &unpacker{files: map[string][]byte{}}
	for _, f := range zr.File {
		if !f.Mode().IsRegular() { // folders, and links, which could point at the credentials
			if _, _, err := safeName(f.Name); err != nil {
				return nil, err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		err = u.add(f.Name, rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
	}
	return u.files, nil
}

func readTarGz(data []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a .tar.gz file: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	u := &unpacker{files: map[string][]byte{}}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return u.files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("not a .tar.gz file: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			if _, _, err := safeName(h.Name); err != nil {
				return nil, err
			}
			continue
		}
		if err := u.add(h.Name, tr); err != nil {
			return nil, err
		}
	}
}

// rootAtEntry keeps what is under the shallowest extension.ts, with its folder as the root.
func rootAtEntry(files map[string][]byte) (map[string][]byte, error) {
	best, depth := "", -1
	var tied bool
	for name := range files {
		if path.Base(name) != entryFile {
			continue
		}
		d := strings.Count(name, "/")
		switch {
		case depth == -1 || d < depth:
			best, depth, tied = name, d, false
		case d == depth:
			tied = true
		}
	}
	if depth == -1 {
		return nil, errors.New("there is no " + entryFile + " in this file")
	}
	if tied {
		return nil, errors.New("this file has more than one " + entryFile + " at the same depth, so it's unclear which to use")
	}
	root := path.Dir(best)
	out := map[string][]byte{}
	for name, data := range files {
		if root == "." {
			out[name] = data
		} else if rest, ok := strings.CutPrefix(name, root+"/"); ok {
			out[rest] = data
		}
	}
	return out, nil
}

// packFiles builds the gzipped tar extract expects: everything inside one top-level folder.
func packFiles(files map[string][]byte) ([]byte, error) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, name := range sorted(files) {
		h := &tar.Header{Name: "package/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(files[name])), ModTime: time.Unix(0, 0)}
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if _, err := tw.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
