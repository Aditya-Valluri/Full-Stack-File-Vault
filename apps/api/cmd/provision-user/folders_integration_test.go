//go:build integration

package main

import (
	"context"
	"errors"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
	"testing"
)

func testFolders(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, directory string) {
	f := newPublicationFixture(t, ctx, admin, dsn, directory)
	store, err := files.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, _, _ := f.user(1000)
	other, _, _, _ := f.user(1000)
	root, err := store.CreateFolder(owner, "Work", nil)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateFolder(owner, "Reports", &root.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateFolder(other, "Private", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateFolder(owner, "Work", nil); !errors.Is(err, files.ErrInvalidInput) {
		t.Fatal("duplicate folder", err)
	}
	if _, err = store.CreateFolder(other, "Foreign child", &root.ID); !errors.Is(err, files.ErrNotFound) {
		t.Fatal("cross-owner parent", err)
	}
	if _, err = store.RenameFolder(other, root.ID, "Stolen"); !errors.Is(err, files.ErrNotFound) {
		t.Fatal("cross-owner rename", err)
	}
	if err = store.DeleteFolder(other, root.ID); !errors.Is(err, files.ErrNotFound) {
		t.Fatal("cross-owner delete", err)
	}
	if err = store.DeleteFolder(owner, root.ID); !errors.Is(err, files.ErrInvalidInput) {
		t.Fatal("nonempty parent deleted", err)
	}
	published, err := f.publisher(f.pool, f.local).PublishWithTags(owner, []*upload.Staged{f.staged("folder-file")}, []string{"private"})
	if err != nil {
		t.Fatal(err)
	}
	id := published[0].ID
	if err = store.MoveFile(other, id, &foreign.ID); !errors.Is(err, files.ErrNotFound) {
		t.Fatal("foreign file moved", err)
	}
	if err = store.MoveFile(owner, id, &foreign.ID); !errors.Is(err, files.ErrNotFound) {
		t.Fatal("foreign destination", err)
	}
	if err = store.MoveFile(owner, id, &child.ID); err != nil {
		t.Fatal(err)
	}
	item, err := store.Get(owner, id)
	if err != nil || item.FolderID == nil || *item.FolderID != child.ID || len(item.Tags) != 1 {
		t.Fatal("move lost metadata", err)
	}
	if err = store.DeleteFolder(owner, child.ID); !errors.Is(err, files.ErrInvalidInput) {
		t.Fatal("folder with files deleted", err)
	}
	for _, tc := range []struct {
		filter files.Filter
		count  int
	}{
		{files.Filter{}, 1}, {files.Filter{RootOnly: true}, 0}, {files.Filter{FolderID: &child.ID, TagsAll: []string{"private"}}, 1}, {files.Filter{FolderID: &foreign.ID}, 0},
	} {
		page, e := store.List(owner, files.ListOptions{First: 20, Filter: tc.filter})
		if e != nil || len(page.Nodes) != tc.count {
			t.Fatal("folder search", e)
		}
	}
	if _, err = store.RenameFolder(owner, child.ID, "Invoices"); err != nil {
		t.Fatal(err)
	}
	listed, err := store.Folders(owner)
	if err != nil || len(listed) != 2 {
		t.Fatal("folder list ownership", err)
	}
	if err = store.MoveFile(owner, id, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteFolder(owner, child.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteFolder(owner, root.ID); err != nil {
		t.Fatal(err)
	}
	item, err = store.Get(owner, id)
	if err != nil || item.FolderID != nil {
		t.Fatal("root move", err)
	}
	f.usage(f.publisher(f.pool, f.local), owner, int64(len("folder-file")))
	// Bounded ancestry: a 21st level is rejected without changing the tree.
	var parent *string
	for i := 0; i < 20; i++ {
		next, e := store.CreateFolder(owner, "Level", parent)
		if e != nil {
			t.Fatal(e)
		}
		parent = &next.ID
	}
	if _, err = store.CreateFolder(owner, "Too deep", parent); !errors.Is(err, files.ErrInvalidInput) {
		t.Fatal("unbounded folder depth", err)
	}
}
