package browser

import (
	"fmt"
	"os"
	"strings"
)

func testProcessAlive(pid int) bool {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return false
	}
	return !strings.Contains(string(b), ") Z ")
}
