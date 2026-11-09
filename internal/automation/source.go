package automation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/dop251/goja"
)

// A name becomes a file name, so this is what keeps saves inside the scripts folder.
var scriptName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Errors the API tells apart.
var (
	ErrScriptNotFound = errors.New("no such script")
	ErrConflict       = errors.New("this script changed since you opened it; reload it to see the latest version")
)

// Source is a script's text. Version identifies it, so a save can't overwrite a newer change made elsewhere.
type Source struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Version string `json:"version"`
	// Access is what the script asks for, after a save.
	Access []Access `json:"access,omitempty"`
}

func versionOf(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:])
}

func (e *Engine) scriptPath(name string) (string, error) {
	if !scriptName.MatchString(name) {
		return "", fmt.Errorf("%q is not a valid script name: use letters, digits, - and _", name)
	}
	return filepath.Join(e.dir, name+".ts"), nil
}

// readScript returns a script's text, refusing anything but a regular file so a link can't expose another file.
func readScript(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrScriptNotFound
	}
	if err != nil {
		return nil, withoutPath(err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	return data, withoutPath(err)
}

// withoutPath keeps only the file's name in an error people will read: where the service keeps its files is none of
// their business, and these errors reach the page.
func withoutPath(err error) error {
	var failed *fs.PathError
	if errors.As(err, &failed) {
		return fmt.Errorf("%s: %w", filepath.Base(failed.Path), failed.Err)
	}
	return err
}

// ReadSource returns a script's text for editing.
func (e *Engine) ReadSource(name string) (Source, error) {
	path, err := e.scriptPath(name)
	if err != nil {
		return Source{}, err
	}
	data, err := readScript(path)
	if err != nil {
		return Source{}, err
	}
	return Source{Name: name, Source: string(data), Version: versionOf(data)}, nil
}

// SaveSource replaces or creates a script and starts running the new version.
//
// An empty version creates the script and fails if it exists. The new text must compile and load, or nothing changes.
// A live script returns to shadow mode unless keepLive is set, so an edit can't reach real devices by accident. Whoever may
// not approve access can't leave new code holding old approvals: unless mayApprove is set, changing a script takes its
// approvals back and it goes to shadow mode, for someone who may approve to look at what the new code asks for.
func (e *Engine) SaveSource(name, source, version string, keepLive, mayApprove bool) (Source, error) {
	path, err := e.scriptPath(name)
	if err != nil {
		return Source{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	current, err := readScript(path)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrScriptNotFound) {
		return Source{}, err
	}
	if exists != (version != "") || (exists && versionOf(current) != version) {
		return Source{}, ErrConflict
	}

	e.refreshDevices()
	if _, err := e.check(name, source); err != nil {
		return Source{}, err
	}
	review := exists && !mayApprove && string(current) != source
	if review {
		keepLive = false
	}
	if !exists || review { // the name may have been used before, and a new script has earned nothing
		if err := e.forget(name); err != nil {
			return Source{}, err
		}
	}
	// Leave live first: if the write then fails, the script is in shadow mode, which is the safe side.
	wasLive := e.live[name]
	if wasLive && !keepLive {
		e.live[name] = false
		if err := e.saveLive(name); err != nil {
			e.live[name] = true
			return Source{}, fmt.Errorf("could not save the setting: %w", err)
		}
	}
	if err := writeAtomically(path, []byte(source)); err != nil {
		return Source{}, fmt.Errorf("could not save the script: %w", withoutPath(err))
	}
	e.replaceScript(name)
	message := "saved"
	if wasLive && !keepLive {
		message = "saved, back in shadow mode"
	}
	e.record(name, "log", message)
	result := Source{Name: name, Source: source, Version: versionOf([]byte(source))}
	if i := slices.IndexFunc(e.scripts, func(s *script) bool { return s.name == name }); i >= 0 {
		result.Access = e.accessOf(e.scripts[i])
	}
	return result, nil
}

// check runs a script once in a throwaway VM, and returns what it asked for. Nothing it does reaches a device or the
// activity log. Its callbacks run once too, within the one time limit, to find access that isn't asked for until then,
// like a daily job's set. They can fail there without saying anything about the script: values read as unknown.
func (e *Engine) check(name, source string) (map[string]*request, error) {
	trial := &script{name: name, dry: true, requests: map[string]*request{}}
	if err := e.setup(trial, source); err != nil {
		return trial.requests, err
	}
	calls := trial.reconciles
	for _, job := range trial.dailies {
		calls = append(calls, job.fn)
	}
	for _, w := range trial.widgets {
		calls = append(calls, w.render)
		for _, name := range slices.Sorted(maps.Keys(w.actions)) {
			calls = append(calls, w.actions[name])
		}
	}
	_ = e.guard(trial, func() error {
		for _, fn := range calls {
			// A button's action gets what its widget's inputs held; here that's nothing, which its reads must survive.
			if _, err := fn(goja.Undefined(), trial.vm.NewObject()); err != nil {
				var stopped *goja.InterruptedError
				if errors.As(err, &stopped) {
					break
				}
			}
		}
		return nil
	})
	return trial.requests, nil
}

func writeAtomically(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".save-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // does nothing once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { // #nosec G302 -- the owner's own scripts, which they also edit with their own tools
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// SDK returns the types scripts can use, keyed by path, so an editor can offer completion.
func (e *Engine) SDK() (map[string]string, error) {
	files := map[string]string{}
	data, err := os.ReadFile(filepath.Join(e.sdkDir, "home.d.ts"))
	if err != nil {
		return nil, withoutPath(err)
	}
	files["home.d.ts"] = string(data)
	return files, nil
}
