package ledger

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

type Entry struct {
	SHA         string
	Timestamp   string
	Disposition string
}

type Ledger struct {
	Path    string
	Entries []Entry
}

func New(path string) *Ledger {
	return &Ledger{Path: path}
}

func (l *Ledger) Load() error {
	data, err := os.ReadFile(l.Path)
	if os.IsNotExist(err) {
		l.Entries = nil
		return nil
	}
	if err != nil {
		return err
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= 1 {
		l.Entries = nil
		return nil
	}

	l.Entries = nil
	for _, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return fmt.Errorf("invalid ledger line: %q", line)
		}
		l.Entries = append(l.Entries, Entry{
			SHA:         fields[0],
			Timestamp:   fields[1],
			Disposition: fields[2],
		})
	}
	return nil
}

func (l *Ledger) Save() error {
	var b strings.Builder
	b.WriteString("shortsha\ttimestamp\tdisposition\n")
	for _, e := range l.Entries {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", e.SHA, e.Timestamp, e.Disposition)
	}
	return os.WriteFile(l.Path, []byte(b.String()), 0o644)
}

func (l *Ledger) Add(sha, timestamp string) error {
	for _, e := range l.Entries {
		if e.SHA == sha {
			return fmt.Errorf("duplicate SHA: %s", sha)
		}
	}
	l.Entries = append(l.Entries, Entry{SHA: sha, Timestamp: timestamp, Disposition: "new"})
	return nil
}

func (l *Ledger) Update(sha, disposition string) error {
	for i := range l.Entries {
		if l.Entries[i].SHA == sha {
			l.Entries[i].Disposition = disposition
			return nil
		}
	}
	return fmt.Errorf("SHA not found: %s", sha)
}

func (l *Ledger) Sort() {
	sort.Slice(l.Entries, func(i, j int) bool {
		return l.Entries[i].Timestamp < l.Entries[j].Timestamp
	})
}

func (l *Ledger) Earliest(disposition string) (Entry, bool) {
	for _, e := range l.Entries {
		if e.Disposition == disposition {
			return e, true
		}
	}
	return Entry{}, false
}

func (l *Ledger) Stats() map[string]int {
	stats := make(map[string]int)
	for _, e := range l.Entries {
		stats[e.Disposition]++
	}
	return stats
}
