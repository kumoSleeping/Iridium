package agent

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Launching an agent here or at an executor (docs/exec.md) alike: a driver asks SpawnOptions for
// its program and puts its launch files through it, and has no branch of its own for an executor.

// Command is the program part of the agent's argv: at an executor the argv it gave; here
// o.Program, or name, looked up on env's PATH (a `#!/usr/bin/env node` script only with node
// there too).
func (o SpawnOptions) Command(name string, env []string) ([]string, error) {
	if r := o.Remote; r != nil {
		if len(r.Argv) == 0 {
			return nil, fmt.Errorf("%w: %s at the machine", ErrNoProgram, name)
		}
		return slices.Clone(r.Argv), nil
	}
	program, err := LookPath(cmp.Or(o.Program, name), env)
	if err != nil {
		return nil, err
	}
	if nodeScript(program) {
		if _, err := LookPath("node", env); err != nil {
			return nil, fmt.Errorf("%s: node not found: %w", name, err)
		}
	}
	return []string{program}, nil
}

// LaunchFile writes data as the session's launch file name (a patch, an extension): here beside
// SystemPromptFile, whole or not at all and only when it changed (an agent starting meanwhile
// may be reading it); at an executor in its scratch directory, under the session. Its path where
// the agent runs.
func (o SpawnOptions) LaunchFile(name string, data []byte) (string, error) {
	if r := o.Remote; r != nil {
		path := r.Join(r.Scratch, o.Session, name)
		return path, r.WriteFile(path, data)
	}
	if o.SystemPromptFile == "" {
		return "", errors.New("no directory for the session's launch files")
	}
	path := filepath.Join(filepath.Dir(o.SystemPromptFile), name)
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, data) {
		return path, nil
	}
	tmp := fmt.Sprintf("%s.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

// PromptFile is SystemPromptFile where the agent runs: itself here, a copy at an executor (made
// now, so a resume takes the current one). "" stays "".
func (o SpawnOptions) PromptFile() (string, error) {
	if o.Remote == nil || o.SystemPromptFile == "" {
		return o.SystemPromptFile, nil
	}
	data, err := os.ReadFile(o.SystemPromptFile)
	if err != nil {
		return "", err
	}
	path, err := o.LaunchFile(filepath.Base(o.SystemPromptFile), data)
	if err != nil {
		return "", fmt.Errorf("the system prompt there: %w", err)
	}
	return path, nil
}

// nodeScript: path is a `#!/usr/bin/env node` script.
func nodeScript(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 200)
	n, _ := f.Read(b)
	first, _, _ := bytes.Cut(b[:n], []byte("\n"))
	return bytes.HasPrefix(first, []byte("#!")) && bytes.Contains(first, []byte("env node"))
}
