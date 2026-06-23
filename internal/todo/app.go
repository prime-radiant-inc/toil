package todo

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

type App struct {
	Store      Store
	Stdout     io.Writer
	Stderr     io.Writer
	ConfirmAdd bool
	AddUsage   string
}

func (a App) Add(args []string) int {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		usage := a.AddUsage
		if usage == "" {
			usage = "Usage: todo add <text>"
		}
		fmt.Fprintln(a.Stderr, usage)
		return 1
	}

	task, err := a.Store.Add(text)
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return 1
	}

	if a.ConfirmAdd {
		fmt.Fprintf(a.Stdout, "Added task %d.\n", task.ID)
	}
	return 0
}

func (a App) List() int {
	tasks, err := a.Store.Load()
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return 1
	}

	if len(tasks) == 0 {
		fmt.Fprintln(a.Stdout, "No tasks.")
		return 0
	}

	for _, task := range tasks {
		status := "[ ]"
		if task.Done {
			status = "[x]"
		}
		fmt.Fprintf(a.Stdout, "%d. %s %s\n", task.ID, status, task.Text)
	}

	return 0
}

func (a App) Done(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Stderr, "Usage: todo done <id>")
		return 1
	}

	id, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || id < 1 {
		fmt.Fprintf(a.Stderr, "Error: invalid id %q\n", args[0])
		return 1
	}

	if _, err := a.Store.MarkDone(id); err != nil {
		fmt.Fprintf(a.Stderr, "Error: %v\n", err)
		return 1
	}

	return 0
}

func (a App) Delete(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Stderr, "Usage: todo delete <id>")
		return 1
	}

	id, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || id < 1 {
		fmt.Fprintf(a.Stderr, "Error: invalid id %q\n", args[0])
		return 1
	}

	if err := a.Store.Delete(id); err != nil {
		fmt.Fprintf(a.Stderr, "Error: %v\n", err)
		return 1
	}

	return 0
}
