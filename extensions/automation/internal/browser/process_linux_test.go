package browser

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func testProcessAlive(pid int) bool {
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		if _, e := strconv.Atoi(entry.Name()); e != nil {
			continue
		}
		b, e := os.ReadFile(fmt.Sprintf("/proc/%s/stat", entry.Name()))
		if e != nil {
			continue
		}
		parts := strings.Fields(string(b)[strings.LastIndex(string(b), ")")+1:])
		if len(parts) > 2 && parts[0] != "Z" && parts[2] == strconv.Itoa(pid) {
			return true
		}
	}
	return false
}
