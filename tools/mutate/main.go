//go:build unix

// mutate is a small mutation tester: it makes one change at a time to a Go file (a flipped comparison, && for ||, a
// dropped !, true for false), runs the package's tests against it through go test -overlay, and lists every change the
// tests didn't notice. The source files are never written.
//
// Usage, from the repo root: go run ./tools/mutate -pkg ./internal/auth [-run REGEX] [-j 4] internal/auth/password.go ...
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

type mutant struct {
	file, where, what string
	source            []byte
}

var swaps = map[token.Token]token.Token{
	token.EQL: token.NEQ, token.NEQ: token.EQL, token.LSS: token.GEQ, token.GEQ: token.LSS,
	token.GTR: token.LEQ, token.LEQ: token.GTR, token.LAND: token.LOR, token.LOR: token.LAND,
}

// mutants makes every mutant of one file, each a whole copy of the file with one change.
func mutants(file string) []mutant {
	var out []mutant
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	render := func(where token.Pos, what string) {
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, f); err != nil {
			return
		}
		out = append(out, mutant{file, fset.Position(where).String(), what, buf.Bytes()})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if to, ok := swaps[n.Op]; ok {
				from := n.Op
				n.Op = to
				render(n.OpPos, from.String()+" -> "+to.String())
				n.Op = from
			}
		case *ast.UnaryExpr:
			if n.Op == token.NOT {
				// Swap the node for its operand through its parent is hard in go/ast; a double negation is the same.
				inner := n.X
				n.X = &ast.UnaryExpr{Op: token.NOT, X: inner}
				render(n.OpPos, "drop !")
				n.X = inner
			}
		case *ast.Ident:
			if n.Name == "true" || n.Name == "false" {
				from := n.Name
				n.Name = map[string]string{"true": "false", "false": "true"}[from]
				render(n.NamePos, from+" -> "+n.Name)
				n.Name = from
			}
		}
		return true
	})
	return out
}

func main() {
	pkg := flag.String("pkg", "", "the package to test")
	run := flag.String("run", "", "go test -run")
	jobs := flag.Int("j", 4, "mutants at a time")
	limit := flag.Duration("timeout", 2*time.Minute, "per mutant")
	flag.Parse()
	work, _ := os.MkdirTemp("", "mutate-*")
	defer os.RemoveAll(work)
	var all []mutant
	for _, f := range flag.Args() {
		all = append(all, mutants(f)...)
	}
	fmt.Fprintf(os.Stderr, "%d mutants\n", len(all))
	type result struct {
		m       mutant
		outcome string
	}
	results := make([]result, len(all))
	var wg sync.WaitGroup
	sem := make(chan struct{}, *jobs)
	for i, m := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			path := filepath.Join(work, fmt.Sprintf("m%d.go", i))
			os.WriteFile(path, m.source, 0o644)
			abs, _ := filepath.Abs(m.file)
			overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{abs: path}})
			overlayPath := filepath.Join(work, fmt.Sprintf("o%d.json", i))
			os.WriteFile(overlayPath, overlay, 0o644)
			ctx, cancel := context.WithTimeout(context.Background(), *limit)
			defer cancel()
			args := []string{"test", "-count=1", "-overlay", overlayPath, *pkg}
			if *run != "" {
				args = append(args, "-run", *run)
			}
			cmd := exec.CommandContext(ctx, "go", args...)
			// go test starts the test binary as a child of its own: on a timeout the whole group goes, or an orphaned
			// test keeps its database sessions and locks, and every later mutant hangs behind it.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
			cmd.WaitDelay = 5 * time.Second
			out, err := cmd.CombinedOutput()
			switch {
			case ctx.Err() != nil:
				results[i] = result{m, "TIMEOUT"}
			case err == nil:
				results[i] = result{m, "SURVIVED"}
			case bytes.Contains(out, []byte("[build failed]")) || bytes.Contains(out, []byte("[setup failed]")):
				results[i] = result{m, "not valid"}
			default:
				results[i] = result{m, "killed"}
			}
		}()
	}
	wg.Wait()
	counts := map[string]int{}
	var survivors []string
	for _, r := range results {
		counts[r.outcome]++
		if r.outcome == "SURVIVED" || r.outcome == "TIMEOUT" {
			survivors = append(survivors, r.outcome+" "+r.m.where+"  "+r.m.what)
		}
	}
	sort.Strings(survivors)
	for _, s := range survivors {
		fmt.Println(s)
	}
	valid := counts["killed"] + counts["SURVIVED"]
	score := 0.0
	if valid > 0 {
		score = 100 * float64(counts["killed"]) / float64(valid)
	}
	// A timeout isn't counted as caught: it may be the mutant looping, or the tests waiting on something else.
	fmt.Printf("%d mutants: %d killed, %d survived, %d timed out, %d not valid; score %.0f%% of those that finished\n",
		len(all), counts["killed"], counts["SURVIVED"], counts["TIMEOUT"], counts["not valid"], score)
}
