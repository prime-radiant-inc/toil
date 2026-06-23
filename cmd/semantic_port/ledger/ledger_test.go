package ledger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	l := New(path)

	if err := l.Add("abc1234", "2025-12-01T10:00:00Z"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := l.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	l2 := New(path)
	if err := l2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(l2.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(l2.Entries))
	}
	e := l2.Entries[0]
	if e.SHA != "abc1234" || e.Disposition != "new" {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestUpdateDisposition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	l := New(path)
	_ = l.Add("abc1234", "2025-12-01T10:00:00Z")

	if err := l.Update("abc1234", "implemented"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if l.Entries[0].Disposition != "implemented" {
		t.Fatalf("expected implemented, got %s", l.Entries[0].Disposition)
	}
}

func TestUpdateMissingSHAErrors(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "ledger.tsv"))
	if err := l.Update("nonexistent", "implemented"); err == nil {
		t.Fatal("expected error for missing SHA")
	}
}

func TestAddDuplicateSHAErrors(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "ledger.tsv"))
	_ = l.Add("abc1234", "2025-12-01T10:00:00Z")
	if err := l.Add("abc1234", "2025-12-02T10:00:00Z"); err == nil {
		t.Fatal("expected error for duplicate SHA")
	}
}

func TestSortChronological(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "ledger.tsv"))
	_ = l.Add("bbb0000", "2025-12-03T10:00:00Z")
	_ = l.Add("aaa0000", "2025-12-01T10:00:00Z")
	_ = l.Add("ccc0000", "2025-12-02T10:00:00Z")
	l.Sort()

	if l.Entries[0].SHA != "aaa0000" || l.Entries[1].SHA != "ccc0000" || l.Entries[2].SHA != "bbb0000" {
		t.Fatalf("unexpected order: %v", l.Entries)
	}
}

func TestEarliestByDisposition(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "ledger.tsv"))
	_ = l.Add("aaa0000", "2025-12-01T10:00:00Z")
	_ = l.Add("bbb0000", "2025-12-02T10:00:00Z")
	_ = l.Update("aaa0000", "implemented")
	l.Sort()

	entry, ok := l.Earliest("new")
	if !ok {
		t.Fatal("expected to find a 'new' entry")
	}
	if entry.SHA != "bbb0000" {
		t.Fatalf("expected bbb0000, got %s", entry.SHA)
	}

	_, ok = l.Earliest("acknowledged")
	if ok {
		t.Fatal("should not find 'acknowledged' entry")
	}
}

func TestStats(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "ledger.tsv"))
	_ = l.Add("aaa0000", "2025-12-01T10:00:00Z")
	_ = l.Add("bbb0000", "2025-12-02T10:00:00Z")
	_ = l.Add("ccc0000", "2025-12-03T10:00:00Z")
	_ = l.Update("aaa0000", "implemented")
	_ = l.Update("bbb0000", "acknowledged")

	stats := l.Stats()
	if stats["new"] != 1 || stats["implemented"] != 1 || stats["acknowledged"] != 1 {
		t.Fatalf("unexpected stats: %v", stats)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	l := New(path)
	if err := l.Load(); err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(l.Entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(l.Entries))
	}
}

func TestSaveCreatesTSVFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	l := New(path)
	_ = l.Add("abc1234", "2025-12-01T10:00:00Z")
	_ = l.Save()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	expected := "shortsha\ttimestamp\tdisposition\nabc1234\t2025-12-01T10:00:00Z\tnew\n"
	if string(data) != expected {
		t.Fatalf("unexpected TSV:\ngot:  %q\nwant: %q", string(data), expected)
	}
}
