package harness

import (
	"bufio"
	"io"
	"strings"
	"unicode/utf8"
)

const maxProviderSSEEventBytes = 1 << 20

// scanProviderSSE parses the common SSE envelope. Provider-specific payloads
// remain in their adapters.
func scanProviderSSE(reader io.Reader, handle func(string, string) error) error {
	scanner := bufio.NewScanner(io.LimitReader(reader, maxModelResponseBytes+1))
	scanner.Buffer(make([]byte, 4096), maxProviderSSEEventBytes)
	event := ""
	var data strings.Builder
	bytesRead := 0
	flush := func() error {
		if data.Len() == 0 {
			event = ""
			return nil
		}
		value := data.String()
		if err := handle(event, value); err != nil {
			return err
		}
		event = ""
		data.Reset()
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		bytesRead += len(line) + 1
		if bytesRead > maxModelResponseBytes || !utf8.ValidString(line) {
			return io.ErrUnexpectedEOF
		}
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() != 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			if data.Len() > maxProviderSSEEventBytes {
				return io.ErrUnexpectedEOF
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}
