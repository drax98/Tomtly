// Package tsbuild turns TypeScript with its imports into one piece of JavaScript for the goja VM.
package tsbuild

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// Bundle compiles source, which is called name in errors and resolves imports from dir, without leaving dir.
func Bundle(dir, name, source string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return bundle(dir, name, source, api.BuildOptions{Plugins: []api.Plugin{importsOnlyFrom(dir)}})
}

// BundleScript is Bundle for an automation, which may only import its own code: relative paths to .ts and .js files
// inside dir. It ignores tsconfig.json, which is only there for editors, and never looks outside dir, so its errors
// can't tell anyone which files exist elsewhere.
func BundleScript(dir, name, source string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return bundle(dir, name, source, api.BuildOptions{
		Plugins:     []api.Plugin{ownCodeOnly(dir), importsOnlyFrom(dir)},
		TsconfigRaw: `{"compilerOptions":{"target":"ES2017"}}`, // what examples/automations/tsconfig.json says
	})
}

func bundle(dir, name, source string, options api.BuildOptions) (string, error) {
	options.Stdin = &api.StdinOptions{Contents: source, ResolveDir: dir, Sourcefile: name + ".ts", Loader: api.LoaderTS}
	options.Bundle, options.Write, options.Format = true, false, api.FormatIIFE
	options.Target, options.Platform, options.LogLevel = api.ES2017, api.PlatformNeutral, api.LogLevelSilent
	r := api.Build(options)
	if len(r.Errors) > 0 {
		m := r.Errors[0]
		if m.Location != nil {
			return "", fmt.Errorf("%s:%d: %s", filepath.Base(m.Location.File), m.Location.Line, m.Text)
		}
		return "", fmt.Errorf("%s", m.Text)
	}
	if len(r.OutputFiles) != 1 {
		return "", fmt.Errorf("compile produced %d files", len(r.OutputFiles))
	}
	return string(r.OutputFiles[0].Contents), nil
}

// importsOnlyFrom limits imports to the given folder. Output from this code is visible through the API, so without
// this, anyone who can save or install code could read the credentials files next to it.
func importsOnlyFrom(root string) api.Plugin {
	return api.Plugin{Name: "imports-only-from", Setup: func(build api.PluginBuild) {
		build.OnLoad(api.OnLoadOptions{Filter: ".*"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
			refused := fmt.Errorf("imports can only reach files in %s's own folder", filepath.Base(root))
			path, err := filepath.EvalSymlinks(args.Path) // a link inside the folder must not lead out of it
			if err != nil {
				return api.OnLoadResult{}, refused // the raw error would show the path
			}
			resolved, err := filepath.EvalSymlinks(root)
			if err != nil {
				return api.OnLoadResult{}, refused
			}
			if inside(resolved, path) {
				return api.OnLoadResult{}, nil
			}
			return api.OnLoadResult{}, refused
		})
	}}
}

// ownCodeOnly refuses an import before esbuild looks for it, unless it is a relative path that stays in root, so nothing
// outside root is even tested for. Reading a data file is refused too: one in the folder could be a copied secret.
func ownCodeOnly(root string) api.Plugin {
	return api.Plugin{Name: "own-code-only", Setup: func(build api.PluginBuild) {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			resolved = root
		}
		refused := fmt.Errorf("a script can only import .ts and .js files in its own folder, by a relative path")
		build.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
			target := filepath.Join(args.ResolveDir, args.Path)
			relative := strings.HasPrefix(args.Path, "./") || strings.HasPrefix(args.Path, "../")
			// esbuild gives a nested import's folder with links resolved, so either spelling of root can appear here.
			if relative && (inside(root, target) || inside(resolved, target)) {
				return api.OnResolveResult{}, nil
			}
			return api.OnResolveResult{}, refused
		})
		build.OnLoad(api.OnLoadOptions{Filter: ".*"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
			if ext := filepath.Ext(args.Path); ext != ".ts" && ext != ".js" {
				return api.OnLoadResult{}, refused
			}
			return api.OnLoadResult{}, nil
		})
	}}
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
