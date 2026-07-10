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
	zw := zip.NewWriter(w)
	if err := s.zipTree(ctx, caller, folder, "", zw, map[string]bool{}); err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

func (s *Service) zipTree(ctx context.Context, caller pii.Subject, folder *string, prefix string, zw *zip.Writer, seen map[string]bool) error {
	rd := s.tx.Reader()
	used := map[string]int{} // sibling entry names, for collision suffixes
	files, err := rd.Objects.ObjectsInFolder(ctx, caller, folder)
	if err != nil {
		return err
	}
	for _, o := range files {
		hdr := &zip.FileHeader{Name: prefix + entryName(used, o.Name), Method: zip.Deflate, Modified: o.Updated}
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
	folders, err := rd.Objects.FoldersInParent(ctx, caller, folder)
	if err != nil {
		return err
	}
	for _, f := range folders {
		if seen[f.ID] { // malformed-cycle guard, like Ancestors
			continue
		}
		seen[f.ID] = true
		dir := prefix + entryName(used, f.Name) + "/"
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
// Path separators are flattened and "."/".." neutralized so an extractor
// can never be steered outside its target directory (zip-slip); a sibling
// collision (names are not unique per folder) gets a " (n)" suffix before
// the extension so no entry silently shadows another.
func entryName(used map[string]int, name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if name == "." || name == ".." {
		name = "_"
	}
	n := used[name]
	used[name] = n + 1
	if n == 0 {
		return name
	}
	ext := path.Ext(name)
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n+1, ext)
}
