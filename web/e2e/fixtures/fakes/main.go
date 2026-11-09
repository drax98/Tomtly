// Command fakes stands in for what the end-to-end stack must never contact for real.
//
//	fakes device   a fake smart home on the second network: an HTTP device that holds state and logs what it receives
//	fakes github   only the two GitHub hosts the marketplace and Home Control download from, over TLS with its own CA
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: fakes device|github")
	}
	switch os.Args[1] {
	case "device":
		log.Fatal(http.ListenAndServe(":9000", newDevice()))
	case "github":
		log.Fatal(serveGitHub())
	}
	log.Fatal("usage: fakes device|github")
}

//go:embed devices.json
var initialDevices []byte

type value struct {
	Code     string   `json:"code"`
	Type     string   `json:"type"`
	Writable bool     `json:"writable,omitempty"`
	Value    any      `json:"value"`
	Range    []string `json:"range,omitempty"`
	Min      *int     `json:"min,omitempty"`
	Max      *int     `json:"max,omitempty"`
	Step     *int     `json:"step,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Scale    int      `json:"scale,omitempty"`
}

type fakeDevice struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	Product string  `json:"product,omitempty"`
	Online  bool    `json:"online"`
	Values  []value `json:"values"`
}

type received struct {
	Method string      `json:"method"`
	URL    string      `json:"url"`
	Header http.Header `json:"header"`
	Body   string      `json:"body"`
}

type device struct {
	mu       sync.Mutex
	devices  []fakeDevice
	commands []map[string]any
	requests []received
	refuse   bool
}

func newDevice() *device {
	d := &device{}
	d.reset()
	return d
}

func (d *device) reset() {
	d.devices, d.commands, d.requests, d.refuse = nil, nil, nil, false
	if err := json.Unmarshal(initialDevices, &d.devices); err != nil {
		log.Fatal(err)
	}
}

func (d *device) set(id, code string, v any) bool {
	for n := range d.devices {
		if d.devices[n].ID != id {
			continue
		}
		for m := range d.devices[n].Values {
			if d.devices[n].Values[m].Code == code {
				d.devices[n].Values[m].Value = v
				return true
			}
		}
	}
	return false
}

// command applies one write, with what the real thing would report after it.
func (d *device) command(id, code string, v any) {
	if !d.set(id, code, v) {
		return
	}
	switch {
	case id == "vacuum" && v == "start":
		d.set(id, "state", "cleaning")
	case id == "vacuum" && v == "dock":
		d.set(id, "state", "docked")
	case id == "blinds" && code == "control" && v == "open":
		d.set(id, "percent_state", 100)
		d.set(id, "percent_control", 100)
	case id == "blinds" && code == "control" && v == "close":
		d.set(id, "percent_state", 0)
		d.set(id, "percent_control", 0)
	case id == "blinds" && code == "percent_control":
		d.set(id, "percent_state", v)
	}
}

func (d *device) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	d.mu.Lock()
	defer d.mu.Unlock()
	if !strings.HasPrefix(r.URL.Path, "/test/") && r.URL.Path != "/devices" {
		d.requests = append(d.requests, received{r.Method, r.URL.String(), r.Header, string(body)})
	}
	switch r.URL.Path {
	case "/devices":
		reply(w, d.devices)
	case "/found":
		found := []map[string]string{}
		for _, device := range d.devices {
			found = append(found, map[string]string{"id": device.ID, "name": device.Name, "product": device.Product, "address": "10.77.0.20"})
		}
		found = append(found, map[string]string{"id": "new-bulb", "name": "New bulb", "product": "Fake bulb", "address": "10.77.0.21", "note": "Press its pairing button to use it."})
		reply(w, found)
	case "/command":
		if d.refuse {
			http.Error(w, "the device refused that", http.StatusInternalServerError)
			return
		}
		var command struct {
			ID, Code string
			Value    any
		}
		if err := json.Unmarshal(body, &command); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		d.commands = append(d.commands, map[string]any{"id": command.ID, "code": command.Code, "value": command.Value})
		d.command(command.ID, command.Code, command.Value)
		reply(w, map[string]bool{"ok": true})
	case "/test/state":
		reply(w, map[string]any{"devices": d.devices, "commands": d.commands, "requests": d.requests})
	case "/test/reset":
		d.reset()
		reply(w, map[string]bool{"ok": true})
	case "/test/set":
		var change struct {
			ID     string
			Online *bool
			Code   string
			Value  any
			Refuse *bool
		}
		if err := json.Unmarshal(body, &change); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if change.Refuse != nil {
			d.refuse = *change.Refuse
		}
		for n := range d.devices {
			if d.devices[n].ID == change.ID && change.Online != nil {
				d.devices[n].Online = *change.Online
			}
		}
		if change.Code != "" {
			d.set(change.ID, change.Code, change.Value)
		}
		reply(w, map[string]bool{"ok": true})
	default:
		reply(w, map[string]bool{"ok": true}) // anything else is a sink the logs above remember
	}
}

func reply(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Print(err)
	}
}

// commitOf gives each ref a stable 40-hex commit, as GitHub's commits API would.
func commitOf(owner, repo, ref string) string {
	sum := sha1.Sum([]byte(owner + "/" + repo + "@" + ref))
	return hex.EncodeToString(sum[:])
}

// serveGitHub answers https://api.github.com/repos/<o>/<r>/commits/<ref> and https://codeload.github.com/<o>/<r>/tar.gz/<ref>
// from folders at /github/<o>/<r>/<ref>. Nothing else is implemented, and an unknown repo or ref is a 404 like the real one.
func serveGitHub() error {
	cert, err := selfSigned("/ca/ca.pem")
	if err != nil {
		return err
	}
	// By hand, since http.ServeMux refuses those two patterns together: /repos/a/commits/b can match either.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 5)
		switch {
		case len(parts) == 5 && parts[0] == "repos" && parts[3] == "commits":
			if !isDir(filepath.Join("/github", parts[1], parts[2], parts[4])) {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, commitOf(parts[1], parts[2], parts[4]))
		case len(parts) >= 4 && parts[2] == "tar.gz":
			ref := strings.Join(parts[3:], "/")
			folder := findRef(parts[0], parts[1], ref)
			if folder == "" {
				http.NotFound(w, r)
				return
			}
			archive, err := tarGz(folder, parts[1]+"-"+filepath.Base(folder))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/gzip")
			if _, err := w.Write(archive); err != nil {
				log.Print(err)
			}
		default:
			http.NotFound(w, r)
		}
	})
	server := http.Server{Addr: ":443", Handler: handler, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
	return server.ListenAndServeTLS("", "")
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// findRef looks a ref or a commit up: the marketplace downloads by the commit it was given.
func findRef(owner, repo, ref string) string {
	root := filepath.Join("/github", owner, repo)
	if isDir(filepath.Join(root, ref)) {
		return filepath.Join(root, ref)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if commitOf(owner, repo, entry.Name()) == ref {
			return filepath.Join(root, entry.Name())
		}
	}
	return ""
}

func tarGz(folder, top string) ([]byte, error) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)
	err := filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(folder, path)
		if err != nil {
			return err
		}
		header := &tar.Header{Name: top + "/" + filepath.ToSlash(rel), Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(data))}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		_, err = archive.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// selfSigned makes the certificate for GitHub's two names and leaves it where SSL_CERT_FILE points in the other containers.
func selfSigned(certFile string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "e2e fake github"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"api.github.com", "codeload.github.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}
