package state

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// TailEvents reads events from a JSONL file starting at startOffset and sends
// them on the returned channel. After reading existing content, it polls for
// new lines at 500ms intervals. The channel closes when the context is
// cancelled.
func TailEvents(ctx context.Context, path string, startOffset int64) <-chan Event {
	ch := make(chan Event, 64)

	go func() {
		defer close(ch)

		offset := startOffset
		for {
			if ctx.Err() != nil {
				return
			}

			f, err := os.Open(path)
			if err != nil {
				select {
				case <-time.After(500 * time.Millisecond):
					continue
				case <-ctx.Done():
					return
				}
			}

			if offset > 0 {
				_, _ = f.Seek(offset, 0)
			}

			// bufio.Reader (not Scanner) because JSONL events can
			// exceed the 64 KiB default Scanner token limit.
			//
			// Only advance `offset` past COMPLETE lines (terminated by
			// \n). If a writer has appended a partial line at EOF, the
			// bytes are read into the bufio.Reader buffer but not
			// confirmed as an event — we must rewind `offset` to the
			// start of that partial so the next poll re-reads those
			// bytes together with the newline the writer will add.
			reader := bufio.NewReader(f)
			consumed := int64(0)
			for {
				line, readErr := reader.ReadString('\n')
				if readErr == nil {
					// Complete line (readErr nil means delimiter found).
					consumed += int64(len(line))
					trimmed := strings.TrimRight(line, "\r\n")
					if trimmed != "" {
						var e Event
						if jerr := json.Unmarshal([]byte(trimmed), &e); jerr == nil {
							select {
							case ch <- e:
							case <-ctx.Done():
								_ = f.Close()
								return
							}
						}
					}
					continue
				}
				// EOF or other error: partial line (if any) is in `line`
				// but must NOT advance offset — we'll re-read it next
				// poll when the newline arrives.
				break
			}
			offset += consumed
			_ = f.Close()

			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch
}
