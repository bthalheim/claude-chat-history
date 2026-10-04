package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeProject(t *testing.T) {
	cases := map[string]string{
		`C:\opt\repos\rcc-automations\BCDR\ninjarmm`: "C--opt-repos-rcc-automations-BCDR-ninjarmm",
		`C:\Users\ben.t\.claude`:                     "C--Users-ben-t--claude",
	}
	for in, want := range cases {
		if got := encodeProject(in); got != want {
			t.Errorf("encodeProject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRestoreStates(t *testing.T) {
	dir := t.TempDir()
	saved := filepath.Join(dir, "saved.jsonl")
	local := filepath.Join(dir, "sub", "local.jsonl")

	write(t, saved, "a\nb\n")

	// missing -> created (parent dir is created too)
	if st, _ := compareFiles(saved, local); st != stMissing {
		t.Fatalf("want missing, got %v", st)
	}
	if res, err := restoreFile(saved, local, false); err != nil || res != "restored" {
		t.Fatalf("restore missing: %q %v", res, err)
	}
	if st, _ := compareFiles(saved, local); st != stInSync {
		t.Fatalf("want in sync, got %v", st)
	}

	// saved grows -> local updated
	write(t, saved, "a\nb\nc\n")
	if st, _ := compareFiles(saved, local); st != stSavedNewer {
		t.Fatalf("want saved newer, got %v", st)
	}
	if res, err := restoreFile(saved, local, false); err != nil || res != "updated" {
		t.Fatalf("update: %q %v", res, err)
	}

	// local grows -> refuse without force, keep local
	write(t, local, "a\nb\nc\nd\n")
	var nf *needsForceError
	if _, err := restoreFile(saved, local, false); !errors.As(err, &nf) || nf.state != stLocalNewer {
		t.Fatalf("want needsForce(local+), got %v", err)
	}
	if b, _ := os.ReadFile(local); string(b) != "a\nb\nc\nd\n" {
		t.Fatalf("local was modified: %q", b)
	}

	// diverged -> refuse; force overwrites and keeps a backup
	write(t, local, "a\nX\nc\n")
	if _, err := restoreFile(saved, local, false); !errors.As(err, &nf) || nf.state != stDiverged {
		t.Fatalf("want needsForce(diverged), got %v", err)
	}
	res, err := restoreFile(saved, local, true)
	if err != nil || !strings.HasPrefix(res, "overwritten") {
		t.Fatalf("force: %q %v", res, err)
	}
	if b, _ := os.ReadFile(local); string(b) != "a\nb\nc\n" {
		t.Fatalf("local not restored: %q", b)
	}
	baks, _ := filepath.Glob(local + ".bak-*")
	if len(baks) != 1 {
		t.Fatalf("want 1 backup, got %v", baks)
	}
	if b, _ := os.ReadFile(baks[0]); string(b) != "a\nX\nc\n" {
		t.Fatalf("backup content wrong: %q", b)
	}
}
