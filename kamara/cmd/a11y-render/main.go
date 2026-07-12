// Command a11y-render writes a Kamara UI state's HTML (with sample content
// and the CSS inlined so axe evaluates real colour contrast) to stdout, so
// CI can run an accessibility checker against the real templates without a
// cluster or a login (README §4).
//
// The state is chosen by the first argument (default "browse"), so CI can
// axe several distinct layouts — the populated browse view + open drawer, the
// empty folder, the root (no breadcrumb), and long/wrapping names (#39).
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/kamara/internal/web"
	"github.com/peristera-io/peristera/lib/pii"
)

func main() {
	state := "browse"
	if len(os.Args) > 1 {
		state = os.Args[1]
	}
	if state == "texteditor" { // its own template, not a folder listing
		v := textEditorView()
		v.Inline = true
		if err := web.TextEditor(os.Stdout, v); err != nil {
			panic(err)
		}
		return
	}
	v, err := view(state)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	v.Inline = true // inline the CSS so axe evaluates real colour contrast
	if err := web.Page(os.Stdout, v); err != nil {
		panic(err)
	}
}

// textEditorView renders the plain-text editor with the conflict alert and
// saved notice both visible, so axe checks their contrast and roles too.
func textEditorView() web.TextEditorView {
	owner := pii.Subject{Instance: "demo.example", UserID: "sample"}
	now := time.Unix(0, 0).UTC()
	return web.TextEditorView{
		Object:   file.Object{ID: "019f2", Owner: owner, Name: "notes.txt", Size: 900, ContentType: "text/plain", Created: now, Updated: now},
		Content:  "Meeting notes\n=============\n\n- ship the file manager\n- review the zip download\n",
		Base:     1,
		Saved:    true,
		Conflict: true,
	}
}

func view(state string) (web.View, error) {
	owner := pii.Subject{Instance: "demo.example", UserID: "sample"}
	now := time.Unix(0, 0).UTC()
	here := "019sample-folder"
	root := file.Folder{ID: here, Owner: owner, Name: "Projects"}
	sub := file.Folder{ID: "019sub", Owner: owner, Name: "Designs", ParentID: &here, Created: now, Updated: now}
	report := file.Object{ID: "019f1", Owner: owner, Name: "report.pdf", Size: 512000, ContentType: "application/pdf", FolderID: &here, Created: now, Updated: now}
	notes := file.Object{ID: "019f2", Owner: owner, Name: "notes.txt", Size: 900, ContentType: "text/plain", FolderID: &here, Created: now, Updated: now}
	all := []file.Folder{root, sub}

	switch state {
	case "browse": // populated folder with the details drawer open
		v := web.View{Crumbs: []file.Folder{root}, Folders: []file.Folder{sub},
			Files: []file.Object{report, notes}, AllFolders: all}
		v.Drawer = &web.DetailView{Object: report, Office: true, Latest: 1,
			Versions: []file.Version{{Ordinal: 1, Size: 512000, Created: now}, {Ordinal: 0, Size: 480000, Created: now}}}
		return v, nil
	case "empty": // an empty folder (the dashed placeholder + its contrast)
		return web.View{Crumbs: []file.Folder{root}, AllFolders: all}, nil
	case "root": // the root view — no breadcrumb entries
		return web.View{Folders: []file.Folder{root}, AllFolders: all}, nil
	case "long": // long / wrapping names
		longName := "A-very-long-file-name-that-should-wrap-gracefully-without-breaking-the-layout-or-contrast.pdf"
		longFolder := file.Folder{ID: "019long", Owner: owner, Name: "A deeply nested project folder with an unusually long descriptive title"}
		return web.View{
			Folders:    []file.Folder{longFolder},
			Files:      []file.Object{{ID: "019l", Owner: owner, Name: longName, Size: 1, Created: now, Updated: now}},
			AllFolders: []file.Folder{longFolder},
		}, nil
	default:
		return web.View{}, fmt.Errorf("unknown state %q (browse|empty|root|long|texteditor)", state)
	}
}
