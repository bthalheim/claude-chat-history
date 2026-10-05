package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Session transcripts are append-only logs, so a repo copy and the local
// copy can be compared by prefix: if one is a prefix of the other, the
// longer one is simply newer and nothing is lost by replacing the shorter.

type syncState int

const (
	stUnknown    syncState = iota
	stInSync               // identical
	stMissing              // not in the local store yet
	stSavedNewer           // local is a strict prefix of the saved copy
	stLocalNewer           // saved copy is a strict prefix of local
	stDiverged             // neither is a prefix of the other
)

func (s syncState) label() string {
	switch s {
	case stInSync:
		return "synced"
	case stMissing:
		return "restore"
	case stSavedNewer:
		return "update"
	case stLocalNewer:
		return "local+"
	case stDiverged:
		return "diverged"
	}
	return "?"
}

// safe reports whether restoring cannot lose local data.
func (s syncState) safe() bool { return s == stMissing || s == stSavedNewer }

func (s syncState) describe() string {
	switch s {
	case stLocalNewer:
		return "has newer content than the saved copy"
	case stDiverged:
		return "differs from the saved copy"
	}
	return s.label()
}

// store locates Claude Code's central session storage. Each session is
// restored to the project folder it was recorded in (the cwd in its
// transcript), unless an explicit project was given.
type store struct {
	root       string // e.g. ~/.claude/projects
	projectDir string // explicit target folder; empty means per-session
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// encodeProject mirrors how Claude Code names a project's folder in its
// store, e.g. C:\opt\repos\x -> C--opt-repos-x.
func encodeProject(path string) string { return nonAlnum.ReplaceAllString(path, "-") }

// newStore resolves the store. storeRoot defaults to
// $CLAUDE_CONFIG_DIR/projects or ~/.claude/projects. If project is empty,
// each session goes to the folder for the cwd recorded in its transcript.
func newStore(storeRoot, project string) (store, error) {
	if storeRoot == "" {
		cfg := os.Getenv("CLAUDE_CONFIG_DIR")
		if cfg == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return store{}, err
			}
			cfg = filepath.Join(home, ".claude")
		}
		storeRoot = filepath.Join(cfg, "projects")
	}
	st := store{root: storeRoot}
	if project != "" {
		abs, err := filepath.Abs(project)
		if err != nil {
			return store{}, err
		}
		st.projectDir = filepath.Join(storeRoot, encodeProject(abs))
	}
	return st, nil
}

// transcriptCwd returns the working directory the session was started in,
// taken from the first record that carries one.
func transcriptCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for i := 0; i < 200; i++ {
		line, err := r.ReadBytes('\n')
		var rec struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Cwd != "" {
			return rec.Cwd
		}
		if err != nil {
			break
		}
	}
	return ""
}

// dirFor is the store folder a saved session restores into: the explicit
// project, else the transcript's cwd, else the current directory.
func (s store) dirFor(saved string) string {
	if s.projectDir != "" {
		return s.projectDir
	}
	cwd := transcriptCwd(saved)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return filepath.Join(s.root, encodeProject(cwd))
}

func (s store) describe() string {
	if s.projectDir != "" {
		return s.projectDir
	}
	return s.root + " (each session's original project)"
}

func (s store) localPath(saved, id string) string {
	return filepath.Join(s.dirFor(saved), id+".jsonl")
}

func compareFiles(saved, local string) (syncState, error) {
	lf, err := os.Open(local)
	if errors.Is(err, os.ErrNotExist) {
		return stMissing, nil
	}
	if err != nil {
		return stUnknown, err
	}
	defer lf.Close()
	sf, err := os.Open(saved)
	if err != nil {
		return stUnknown, err
	}
	defer sf.Close()
	si, err := sf.Stat()
	if err != nil {
		return stUnknown, err
	}
	li, err := lf.Stat()
	if err != nil {
		return stUnknown, err
	}
	same, err := equalPrefix(sf, lf, min(si.Size(), li.Size()))
	switch {
	case err != nil:
		return stUnknown, err
	case !same:
		return stDiverged, nil
	case si.Size() == li.Size():
		return stInSync, nil
	case si.Size() > li.Size():
		return stSavedNewer, nil
	}
	return stLocalNewer, nil
}

func equalPrefix(a, b io.Reader, n int64) (bool, error) {
	ba, bb := make([]byte, 64<<10), make([]byte, 64<<10)
	for n > 0 {
		k := min(int64(len(ba)), n)
		if _, err := io.ReadFull(a, ba[:k]); err != nil {
			return false, err
		}
		if _, err := io.ReadFull(b, bb[:k]); err != nil {
			return false, err
		}
		if !bytes.Equal(ba[:k], bb[:k]) {
			return false, nil
		}
		n -= k
	}
	return true, nil
}

// needsForceError is returned when restoring would discard local content.
type needsForceError struct{ state syncState }

func (e *needsForceError) Error() string {
	return fmt.Sprintf("local copy %s (use force to overwrite; a backup is kept)", e.state.describe())
}

// restoreFile makes local match saved. It creates the local file if absent
// and updates it when it is a strict prefix of saved. Otherwise it needs
// force, in which case the existing local file is backed up first.
func restoreFile(saved, local string, force bool) (string, error) {
	st, err := compareFiles(saved, local)
	if err != nil {
		return "", err
	}
	action := "restored"
	switch st {
	case stInSync:
		return "already up to date", nil
	case stSavedNewer:
		action = "updated"
	case stLocalNewer, stDiverged:
		if !force {
			return "", &needsForceError{st}
		}
		bak := local + ".bak-" + time.Now().Format("20060102T150405")
		if err := copyFile(local, bak); err != nil {
			return "", fmt.Errorf("backup failed: %w", err)
		}
		action = "overwritten (backup: " + filepath.Base(bak) + ")"
	}
	if err := copyFile(saved, local); err != nil {
		return "", err
	}
	return action, nil
}

// copyFile copies src to dst atomically via a temp file and rename.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
