//go:build integration

package main

import (
	"context"
	"errors"
	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
	"reflect"
	"testing"
)

func testPrivateTags(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, directory string) {
	f := newPublicationFixture(t, ctx, admin, dsn, directory)
	ownerCtx, owner, _, _ := f.user(1000)
	otherCtx, _, _, _ := f.user(1000)
	reader, _ := files.NewStore(f.pool)
	p := f.publisher(f.pool, f.local)
	owned, err := p.Publish(ownerCtx, []*upload.Staged{f.staged("private-tag-content")})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := p.Publish(otherCtx, []*upload.Staged{f.staged("private-tag-content")})
	if err != nil {
		t.Fatal(err)
	}
	id := owned[0].ID
	tags, err := reader.SetTags(ownerCtx, id, []string{" Work ", "tax", "work"})
	if err != nil || !reflect.DeepEqual(tags, []string{"tax", "work"}) {
		t.Fatal(tags, err)
	}
	if _, err = reader.SetTags(otherCtx, id, []string{"stolen"}); !errors.Is(err, files.ErrNotFound) {
		t.Fatal("foreign edit", err)
	}
	own, err := reader.Get(ownerCtx, id)
	if err != nil || !reflect.DeepEqual(own.Tags, tags) {
		t.Fatal(own, err)
	}
	copy, err := reader.Get(otherCtx, foreign[0].ID)
	if err != nil || len(copy.Tags) != 0 {
		t.Fatal("dedup copy leaked tags", err)
	}
	for _, input := range [][]string{{"work", "tax"}, {"WORK"}} {
		page, err := reader.List(ownerCtx, files.ListOptions{First: 20, Filter: files.Filter{TagsAll: input}})
		if err != nil || len(page.Nodes) != 1 || page.Nodes[0].ID != id {
			t.Fatal(page, err)
		}
	}
	page, err := reader.List(otherCtx, files.ListOptions{First: 20, Filter: files.Filter{TagsAll: []string{"work"}}})
	if err != nil || len(page.Nodes) != 0 {
		t.Fatal("foreign tags matched", err)
	}
	page, err = reader.List(ownerCtx, files.ListOptions{First: 20, Filter: files.Filter{TagsAll: []string{"work", "missing"}}})
	if err != nil || len(page.Nodes) != 0 {
		t.Fatal("all tags not required", err)
	}
	hash, err := auth.HashPassword([]byte("synthetic-tag-fixture-password"))
	if err != nil {
		t.Fatal(err)
	}
	// SQL literal wildcard escaping: this fixture login contains underscores.
	if _, err = admin.Exec(ctx, "INSERT INTO vault.credentials(user_id,login_name,password_hash) VALUES($1,'tag_owner_test',$2)", owner, hash); err != nil {
		t.Fatal(err)
	}
	match := "OWNER_"
	page, err = reader.List(ownerCtx, files.ListOptions{First: 20, Filter: files.Filter{UploaderNameContains: &match, TagsAll: []string{"work"}}})
	if err != nil || len(page.Nodes) != 1 {
		t.Fatal("combined uploader filter", page, err)
	}
	mismatch := "%"
	page, err = reader.List(ownerCtx, files.ListOptions{First: 20, Filter: files.Filter{UploaderNameContains: &mismatch}})
	if err != nil || len(page.Nodes) != 0 {
		t.Fatal("wildcard was not literal", err)
	}
	if _, err = reader.SetTags(ownerCtx, id, []string{"invalid,"}); !errors.Is(err, files.ErrInvalidInput) {
		t.Fatal(err)
	}
	own, err = reader.Get(ownerCtx, id)
	if err != nil || !reflect.DeepEqual(own.Tags, tags) {
		t.Fatal("invalid replacement changed tags", err)
	}
	if _, err = reader.SetTags(ownerCtx, id, nil); err != nil {
		t.Fatal(err)
	}
	own, err = reader.Get(ownerCtx, id)
	if err != nil || len(own.Tags) != 0 {
		t.Fatal("clear failed", err)
	}
	if _, err = reader.SetTags(ownerCtx, id, []string{"cleanup"}); err != nil {
		t.Fatal(err)
	}
	if err = reader.Delete(ownerCtx, id); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM vault.file_tags WHERE file_id=$1", id).Scan(&count); err != nil || count != 0 {
		t.Fatal("tag cascade", err)
	}
}
