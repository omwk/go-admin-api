package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupPrefersProcessEnvironment(t *testing.T) {
	t.Setenv("TESTENV_SAMPLE", "from-process")
	if got := Lookup("TESTENV_SAMPLE"); got != "from-process" {
		t.Fatalf("got %q", got)
	}
}

func TestLookupFallsBackToDotEnv(t *testing.T) {
	dir := t.TempDir()
	// 故意写成 CRLF，模拟 Windows 上的 .env。
	content := "# comment\r\nTESTENV_FALLBACK=postgres://user:pass@host/db?sslmode=disable\r\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	t.Setenv("TESTENV_FALLBACK", "")

	want := "postgres://user:pass@host/db?sslmode=disable"
	if got := Lookup("TESTENV_FALLBACK"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLookupStripsQuotesAndIgnoresMissingKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TESTENV_QUOTED=\"hello\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	t.Setenv("TESTENV_QUOTED", "")
	t.Setenv("TESTENV_ABSENT", "")

	if got := Lookup("TESTENV_QUOTED"); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if got := Lookup("TESTENV_ABSENT"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
