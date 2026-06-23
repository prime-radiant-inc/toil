package main

import (
	"fmt"
	"os"

	"primeradiant.com/toil/cmd/semantic_port/ledger"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "ledger":
		runLedger(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: semantic_port <command>

Commands:
  ledger    Manage the commit disposition ledger`)
}

func runLedger(args []string) {
	if len(args) < 1 {
		printLedgerUsage()
		os.Exit(1)
	}

	path := os.Getenv("LEDGER_PATH")
	if path == "" {
		path = "semantic_port/ledger.tsv"
	}

	l := ledger.New(path)
	if err := l.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "error loading ledger: %v\n", err)
		os.Exit(1)
	}

	switch args[0] {
	case "add":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: semantic_port ledger add <shortsha> <timestamp>")
			os.Exit(1)
		}
		if err := l.Add(args[1], args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := l.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "error saving: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("added %s\n", args[1])

	case "update":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: semantic_port ledger update <shortsha> <disposition>")
			os.Exit(1)
		}
		if err := l.Update(args[1], args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := l.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "error saving: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("updated %s → %s\n", args[1], args[2])

	case "sort":
		l.Sort()
		if err := l.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "error saving: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("sorted")

	case "earliest":
		disposition := "new"
		if len(args) > 1 {
			disposition = args[1]
		}
		entry, ok := l.Earliest(disposition)
		if !ok {
			fmt.Printf("no entries with disposition=%s\n", disposition)
			os.Exit(0)
		}
		fmt.Printf("%s\t%s\t%s\n", entry.SHA, entry.Timestamp, entry.Disposition)

	case "stats":
		stats := l.Stats()
		total := 0
		for _, v := range stats {
			total += v
		}
		fmt.Printf("total: %d\n", total)
		for _, d := range []string{"new", "implemented", "acknowledged"} {
			fmt.Printf("  %s: %d\n", d, stats[d])
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown ledger command: %s\n", args[0])
		printLedgerUsage()
		os.Exit(1)
	}
}

func printLedgerUsage() {
	fmt.Fprintln(os.Stderr, `Usage: semantic_port ledger <command>

Commands:
  add <sha> <timestamp>       Add a new commit entry
  update <sha> <disposition>  Update disposition (new|implemented|acknowledged)
  sort                        Sort entries chronologically
  earliest [disposition]      Show earliest entry (default: new)
  stats                       Show disposition counts`)
}
