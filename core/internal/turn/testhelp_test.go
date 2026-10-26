package turn

import (
	"os"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func noticeForTest() tools.NotifyResult {
	code := 0
	return tools.NotifyResult{
		CallID: "c1", JobID: "job-1", Status: tools.JobDone,
		ExitCode: &code, Output: "out", Truncated: false, IsError: false,
	}
}
