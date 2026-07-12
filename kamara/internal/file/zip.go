package file

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/peristera-io/peristera/kamara/internal/engine"
	"github.com/peristera-io/peristera/lib/pii"
)

// DownloadZip streams folder's subtree (nil = the caller's whole root) to w
// as a zip archive. Entries are written as the tree is walked — no temp
// files and no size precomputation, so the download starts immediately and
// memory stays flat regardless of tree size. The walk is per-owner scoped
// like ListChildren; the named folder is access-checked up front. Empty
// folders survive as explicit directory entries.
func (s *Service) DownloadZip(ctx context.Context, caller pii.Subject, folder *string, w io.Writer) error {
	if folder != nil {
		if err := s.authorizeFolder(ctx, caller, *folder); err != nil {
			return err
		}
	}
	// Seed the cycle guard with the root itself so a malformed parent edge
	// back to it cannot re-emit the whole subtree (like DeleteFolderTree).
	seen := map[string]bool{}
	if folder != nil {
		seen[*folder] = true
	}
	zw := zip.NewWriter(w)
	if err := s.zipTree(ctx, caller, folder, "", zw, seen); err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

func (s *Service) zipTree(ctx context.Context, caller pii.Subject, folder *string, prefix string, zw *zip.Writer, seen map[string]bool) error {
	rd := s.tx.Reader()
	files, err := rd.Objects.ObjectsInFolder(ctx, caller, folder)
	if err != nil {
		return err
	}
	folders, err := rd.Objects.FoldersInParent(ctx, caller, folder)
	if err != nil {
		return err
	}
	used := map[string]bool{}   // entry names already emitted in this directory
	pending := map[string]int{} // literal sibling names not yet emitted
	for _, o := range files {
		pending[sanitizeEntryName(o.Name)]++
	}
	for _, f := range folders {
		pending[sanitizeEntryName(f.Name)]++
	}
	for _, o := range files {
		hdr := &zip.FileHeader{Name: prefix + entryName(used, pending, o.Name), Method: zip.Deflate, Modified: o.Updated}
		ew, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		refs, err := rd.Objects.ManifestOf(ctx, o.ID)
		if err != nil {
			return err
		}
		if err := engine.Reassemble(ctx, refs, s.cipher, s.blobs, ew); err != nil {
			return fmt.Errorf("file: zip entry %s: %w", o.ID, err)
		}
	}
	for _, f := range folders {
		if seen[f.ID] { // malformed-cycle guard, like Ancestors
			continue
		}
		seen[f.ID] = true
		dir := prefix + entryName(used, pending, f.Name) + "/"
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: dir, Modified: f.Updated}); err != nil {
			return err
		}
		fid := f.ID
		if err := s.zipTree(ctx, caller, &fid, dir, zw, seen); err != nil {
			return err
		}
	}
	return nil
}

// entryName makes a display name safe and unique within its zip directory.
// A sibling collision (names are not unique per folder) gets a " (n)" suffix
// before the extension so no entry silently shadows another. The first
// occurrence of a literal name always keeps it; a generated suffix skips both
// names already emitted (used) and literal siblings not yet emitted (pending),
// so "a.txt", "a.txt", "a (2).txt" comes out as "a.txt", "a (3).txt",
// "a (2).txt" regardless of listing order.
func entryName(used map[string]bool, pending map[string]int, name string) string {
	name = sanitizeEntryName(name)
	pending[name]--
	if !used[name] {
		used[name] = true
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, n, ext)
		if !used[candidate] && pending[candidate] <= 0 {
			used[candidate] = true
			return candidate
		}
	}
}

// sanitizeEntryName flattens path separators and neutralizes "."/".." so an
// extractor can never be steered outside its target directory (zip-slip).
func sanitizeEntryName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if name == "." || name == ".." {
		return "_"
	}
	return name
}
