// Command a11y-render writes the Ergonomos page HTML (with sample tasks)
// to stdout, so CI can run an accessibility checker (pa11y/axe) against the
// real template without a cluster or a login. Not shipped in the app image.
package main

import (
	"os"
	"time"

	"github.com/peristera-io/peristera/ergonomos/internal/task"
	"github.com/peristera-io/peristera/ergonomos/internal/web"
	"github.com/peristera-io/peristera/lib/pii"
)

func main() {
	owner := pii.Subject{Instance: "demo.example", UserID: "sample"}
	now := time.Unix(0, 0).UTC()
	tasks := []task.Task{
		{ID: "019f2e2a-0000-7000-8000-000000000001", Owner: owner, Title: "Buy milk", Created: now, Updated: now},
		{ID: "019f2e2a-0000-7000-8000-000000000002", Owner: owner, Title: "Write the report", Done: true, Created: now, Updated: now},
	}
	if err := web.Page(os.Stdout, tasks); err != nil {
		panic(err)
	}
}
