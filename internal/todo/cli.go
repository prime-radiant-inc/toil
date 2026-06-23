package todo

import (
	"fmt"
	"io"
)

func Run(args []string, home string, stdout, stderr io.Writer, confirmAdd bool, addUsage string) int {
	app := App{
		Store:      NewStore(home),
		Stdout:     stdout,
		Stderr:     stderr,
		ConfirmAdd: confirmAdd,
		AddUsage:   addUsage,
	}

	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: todo <add|list|done|delete>")
		return 1
	}

	switch args[0] {
	case "add":
		return app.Add(args[1:])
	case "list":
		return app.List()
	case "done":
		return app.Done(args[1:])
	case "delete":
		return app.Delete(args[1:])
	default:
		fmt.Fprintln(stderr, "Error: unknown command")
		return 1
	}
}
