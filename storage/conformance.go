package storage

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestConformance runs the shared Disk behavioral suite against a Disk
// built by factory, so every backend (local, s3, ...) is held to identical
// guarantees. Each subtest calls factory fresh and runs its own cleanup
// func afterward, so subtests are independent and safe to run in parallel.
func TestConformance(t *testing.T, factory func() (Disk, func())) {
	t.Helper()
	ctx := context.Background()

	run := func(name string, fn func(t *testing.T, d Disk)) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d, cleanup := factory()
			t.Cleanup(cleanup)
			fn(t, d)
		})
	}

	run("put then get", func(t *testing.T, d Disk) {
		if err := d.Put(ctx, "a.txt", strings.NewReader("hello")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := d.Get(ctx, "a.txt")
		if err != nil || string(got) != "hello" {
			t.Fatalf("Get = (%s, %v), want (hello, nil)", got, err)
		}
	})

	run("put string", func(t *testing.T, d Disk) {
		if err := d.PutString(ctx, "a.txt", "hello"); err != nil {
			t.Fatalf("PutString: %v", err)
		}
		got, err := d.Get(ctx, "a.txt")
		if err != nil || string(got) != "hello" {
			t.Fatalf("Get = (%s, %v), want (hello, nil)", got, err)
		}
	})

	run("put overwrites", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "a.txt", "first")
		mustPutString(t, ctx, d, "a.txt", "second")
		got, err := d.Get(ctx, "a.txt")
		if err != nil || string(got) != "second" {
			t.Fatalf("Get = (%s, %v), want (second, nil)", got, err)
		}
	})

	run("get missing file", func(t *testing.T, d Disk) {
		_, err := d.Get(ctx, "missing.txt")
		if !errors.Is(err, ErrFileNotFound) {
			t.Fatalf("Get(missing) = %v, want %v", err, ErrFileNotFound)
		}
	})

	run("exists and missing", func(t *testing.T, d Disk) {
		exists, err := d.Exists(ctx, "a.txt")
		if err != nil || exists {
			t.Fatalf("Exists(unwritten) = (%v, %v), want (false, nil)", exists, err)
		}
		missing, err := d.Missing(ctx, "a.txt")
		if err != nil || !missing {
			t.Fatalf("Missing(unwritten) = (%v, %v), want (true, nil)", missing, err)
		}

		mustPutString(t, ctx, d, "a.txt", "hello")

		exists, err = d.Exists(ctx, "a.txt")
		if err != nil || !exists {
			t.Fatalf("Exists(written) = (%v, %v), want (true, nil)", exists, err)
		}
		missing, err = d.Missing(ctx, "a.txt")
		if err != nil || missing {
			t.Fatalf("Missing(written) = (%v, %v), want (false, nil)", missing, err)
		}
	})

	run("delete", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "a.txt", "hello")
		mustPutString(t, ctx, d, "b.txt", "world")

		if err := d.Delete(ctx, "a.txt", "b.txt"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if exists, _ := d.Exists(ctx, "a.txt"); exists {
			t.Error("a.txt still exists after Delete")
		}
		if exists, _ := d.Exists(ctx, "b.txt"); exists {
			t.Error("b.txt still exists after Delete")
		}
	})

	run("delete is idempotent", func(t *testing.T, d Disk) {
		if err := d.Delete(ctx, "never-existed.txt"); err != nil {
			t.Fatalf("Delete(missing): %v", err)
		}
	})

	run("copy", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "a.txt", "hello")

		if err := d.Copy(ctx, "a.txt", "b.txt"); err != nil {
			t.Fatalf("Copy: %v", err)
		}
		got, err := d.Get(ctx, "b.txt")
		if err != nil || string(got) != "hello" {
			t.Fatalf("Get(copy) = (%s, %v), want (hello, nil)", got, err)
		}
		if exists, _ := d.Exists(ctx, "a.txt"); !exists {
			t.Error("Copy removed the source")
		}
	})

	run("move", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "a.txt", "hello")

		if err := d.Move(ctx, "a.txt", "b.txt"); err != nil {
			t.Fatalf("Move: %v", err)
		}
		got, err := d.Get(ctx, "b.txt")
		if err != nil || string(got) != "hello" {
			t.Fatalf("Get(dest) = (%s, %v), want (hello, nil)", got, err)
		}
		if exists, _ := d.Exists(ctx, "a.txt"); exists {
			t.Error("Move left the source behind")
		}
	})

	run("size", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "a.txt", "hello")

		size, err := d.Size(ctx, "a.txt")
		if err != nil || size != 5 {
			t.Fatalf("Size = (%d, %v), want (5, nil)", size, err)
		}
	})

	run("last modified is recent", func(t *testing.T, d Disk) {
		before := time.Now().Add(-time.Minute)
		mustPutString(t, ctx, d, "a.txt", "hello")

		modified, err := d.LastModified(ctx, "a.txt")
		if err != nil {
			t.Fatalf("LastModified: %v", err)
		}
		if modified.Before(before) {
			t.Errorf("LastModified = %v, want a time after %v", modified, before)
		}
	})

	run("content type is sniffed from plain text", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "a.txt", "hello, world")

		ct, err := d.ContentType(ctx, "a.txt")
		if err != nil {
			t.Fatalf("ContentType: %v", err)
		}
		if !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("ContentType = %q, want a text/plain... type", ct)
		}
	})

	run("url contains the path", func(t *testing.T, d Disk) {
		u := d.Url("a/b.txt")
		if u == "" || !strings.Contains(u, "a/b.txt") {
			t.Errorf("Url(a/b.txt) = %q, want it to contain the path", u)
		}
	})

	run("temporary url", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "a.txt", "hello")

		u, err := d.TemporaryUrl(ctx, "a.txt", time.Hour)
		if err != nil || u == "" {
			t.Fatalf("TemporaryUrl = (%q, %v), want a non-empty URL", u, err)
		}
	})

	run("presigned put url", func(t *testing.T, d Disk) {
		u, err := d.PresignedPutUrl(ctx, "a.txt", time.Hour)
		if err != nil || u == "" {
			t.Fatalf("PresignedPutUrl = (%q, %v), want a non-empty URL", u, err)
		}
	})

	run("files lists only the given directory's own files", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "dir/a.txt", "a")
		mustPutString(t, ctx, d, "dir/sub/b.txt", "b")

		files, err := d.Files(ctx, "dir")
		if err != nil {
			t.Fatalf("Files: %v", err)
		}
		assertSameSet(t, files, []string{"dir/a.txt"})
	})

	run("all files lists recursively", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "dir/a.txt", "a")
		mustPutString(t, ctx, d, "dir/sub/b.txt", "b")

		files, err := d.AllFiles(ctx, "dir")
		if err != nil {
			t.Fatalf("AllFiles: %v", err)
		}
		assertSameSet(t, files, []string{"dir/a.txt", "dir/sub/b.txt"})
	})

	run("directories lists only the given directory's immediate children", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "dir/sub/b.txt", "b")
		mustPutString(t, ctx, d, "dir/a.txt", "a")

		dirs, err := d.Directories(ctx, "dir")
		if err != nil {
			t.Fatalf("Directories: %v", err)
		}
		assertSameSet(t, dirs, []string{"dir/sub"})
	})

	run("all directories lists every nested directory", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "dir/sub/deeper/c.txt", "c")

		dirs, err := d.AllDirectories(ctx, "dir")
		if err != nil {
			t.Fatalf("AllDirectories: %v", err)
		}
		assertSameSet(t, dirs, []string{"dir/sub", "dir/sub/deeper"})
	})

	run("make directory is visible with no files in it", func(t *testing.T, d Disk) {
		if err := d.MakeDirectory(ctx, "dir/empty"); err != nil {
			t.Fatalf("MakeDirectory: %v", err)
		}
		dirs, err := d.Directories(ctx, "dir")
		if err != nil {
			t.Fatalf("Directories: %v", err)
		}
		assertSameSet(t, dirs, []string{"dir/empty"})
	})

	run("delete directory removes everything under it", func(t *testing.T, d Disk) {
		mustPutString(t, ctx, d, "dir/a.txt", "a")
		mustPutString(t, ctx, d, "dir/sub/b.txt", "b")

		if err := d.DeleteDirectory(ctx, "dir"); err != nil {
			t.Fatalf("DeleteDirectory: %v", err)
		}
		files, err := d.AllFiles(ctx, "dir")
		if err != nil {
			t.Fatalf("AllFiles after DeleteDirectory: %v", err)
		}
		if len(files) != 0 {
			t.Errorf("AllFiles after DeleteDirectory = %v, want none", files)
		}
	})
}

func mustPutString(t *testing.T, ctx context.Context, d Disk, path, contents string) {
	t.Helper()
	if err := d.PutString(ctx, path, contents); err != nil {
		t.Fatalf("PutString(%q): %v", path, err)
	}
}

// assertSameSet compares got and want as sets (order-independent) — backend
// listing order isn't part of the Disk contract.
func assertSameSet(t *testing.T, got, want []string) {
	t.Helper()
	gotSorted, wantSorted := slices.Clone(got), slices.Clone(want)
	slices.Sort(gotSorted)
	slices.Sort(wantSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Errorf("got %v, want %v", got, want)
	}
}
