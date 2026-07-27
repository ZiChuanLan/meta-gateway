package webdavsync

import "testing"

func TestResolveBackupURLDirectoryAndFile(t *testing.T) {
	dirURL, err := ResolveBackupURL("https://dav.example.com/remote.php/dav/files/user/")
	if err != nil {
		t.Fatal(err)
	}
	wantDir := "https://dav.example.com/remote.php/dav/files/user/all-api-hub-backup/all-api-hub-1-0.json"
	if dirURL != wantDir {
		t.Fatalf("dir got %q want %q", dirURL, wantDir)
	}

	fileURL, err := ResolveBackupURL("https://dav.example.com/backups/custom.json")
	if err != nil {
		t.Fatal(err)
	}
	if fileURL != "https://dav.example.com/backups/custom.json" {
		t.Fatalf("file got %q", fileURL)
	}

	if _, err := ResolveBackupURL(""); err == nil {
		t.Fatal("expected empty url error")
	}
	if _, err := ResolveBackupURL("not-a-url"); err == nil {
		t.Fatal("expected invalid url error")
	}
}

func TestRedactedURLStripsUserinfo(t *testing.T) {
	got := RedactedURL("https://user:pass@dav.example.com/path")
	if got != "https://dav.example.com/path" {
		t.Fatalf("got %q", got)
	}
}
