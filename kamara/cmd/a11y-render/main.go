// Command a11y-render writes the Kamara file-browser page HTML (with sample
// folders and files) to stdout, so CI can run an accessibility checker (axe)
// against the real template without a cluster or a login (README §4).
package main

import (
	"os"
	"time"

	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/kamara/internal/web"
	"github.com/peristera-io/peristera/lib/pii"
)

func main() {
	owner := pii.Subject{Instance: "demo.example", UserID: "sample"}
	now := time.Unix(0, 0).UTC()
	here := "019sample-folder"
	v := web.View{
		Crumbs:  []file.Folder{{ID: here, Owner: owner, Name: "Projects", Created: now, Updated: now}},
		Folders: []file.Folder{{ID: "019sub", Owner: owner, Name: "Designs", ParentID: &here, Created: now, Updated: now}},
		Files: []file.Object{
			{ID: "019f1", Owner: owner, Name: "report.pdf", Size: 512000, FolderID: &here, Created: now, Updated: now},
			{ID: "019f2", Owner: owner, Name: "notes.txt", Size: 900, FolderID: &here, Created: now, Updated: now},
		},
		Inline: true, // inline the CSS so axe evaluates real colour contrast
	}
	if err := web.Page(os.Stdout, v); err != nil {
		panic(err)
	}
}
