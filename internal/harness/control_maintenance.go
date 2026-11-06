package harness

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// ControlMaintenance reads the existing bounded journal without expiring or
// applying proposals. It uses the same integrity check as the control owner.
func ControlMaintenance(data string) (pending, unresolved int, returnErr error) {
	path := filepath.Join(data, controlJournalName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > controlJournalMaxBytes {
		return 0, 0, ErrControlJournal
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	statuses := map[string]ControlOperationStatus{}
	scanner := bufio.NewScanner(io.LimitReader(file, controlJournalMaxBytes+1))
	scanner.Buffer(make([]byte, 4096), controlJournalMaxBytes+1)
	count := 0
	for scanner.Scan() {
		var line controlJournalLine
		if json.Unmarshal(scanner.Bytes(), &line) != nil || !validControlHash(line) || line.Action == "" || line.Record.Operation.ID == "" {
			return 0, 0, ErrControlJournal
		}
		count++
		if count > controlJournalMaxRecords {
			return 0, 0, ErrControlJournal
		}
		switch line.Record.Operation.Status {
		case ControlPending, ControlApplied, ControlRejected, ControlStale, ControlPartiallyApplied, ControlFailed:
		default:
			return 0, 0, ErrControlJournal
		}
		statuses[line.Record.Operation.ID] = line.Record.Operation.Status
	}
	if scanner.Err() != nil {
		return 0, 0, scanner.Err()
	}
	for _, status := range statuses {
		switch status {
		case ControlPending:
			pending++
		case ControlPartiallyApplied:
			unresolved++
		}
	}
	return pending, unresolved, nil
}
