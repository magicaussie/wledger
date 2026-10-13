package backup

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"testing"
)

func fakeRestoreArchive(headers ...zip.FileHeader) *zip.Reader {
	files := make([]*zip.File, len(headers))
	for i := range headers {
		files[i] = &zip.File{FileHeader: headers[i]}
	}
	return &zip.Reader{File: files}
}

func TestValidateRestoreArchiveLimitsEntryCount(t *testing.T) {
	headers := make([]zip.FileHeader, maxRestoreArchiveEntries+1)
	for i := range headers {
		headers[i].Name = "unused/entry"
	}
	if err := validateRestoreArchive(fakeRestoreArchive(headers...)); err == nil {
		t.Fatal("expected excessive archive entry count to be rejected")
	}
}

func TestValidateRestoreArchiveRejectsUnsafeAndDuplicateUploads(t *testing.T) {
	cases := []struct {
		name    string
		headers []zip.FileHeader
	}{
		{name: "duplicate path", headers: []zip.FileHeader{{Name: "uploads/photo.png"}, {Name: "uploads/photo.png"}}},
		{name: "parent traversal", headers: []zip.FileHeader{{Name: "uploads/../outside.txt"}}},
		{name: "backslash path", headers: []zip.FileHeader{{Name: `uploads/..\outside.txt`}}},
		{name: "oversized individual file", headers: []zip.FileHeader{{Name: "uploads/big.bin", UncompressedSize64: maxRestoreFileBytes + 1}}},
		{name: "oversized cumulative uploads", headers: []zip.FileHeader{
			{Name: "uploads/a.bin", UncompressedSize64: maxRestoreFileBytes},
			{Name: "uploads/b.bin", UncompressedSize64: maxRestoreFileBytes},
			{Name: "uploads/c.bin", UncompressedSize64: maxRestoreFileBytes},
			{Name: "uploads/d.bin", UncompressedSize64: maxRestoreFileBytes},
			{Name: "uploads/e.bin", UncompressedSize64: maxRestoreFileBytes},
			{Name: "uploads/f.bin", UncompressedSize64: maxRestoreFileBytes},
			{Name: "uploads/g.bin", UncompressedSize64: maxRestoreFileBytes},
			{Name: "uploads/h.bin", UncompressedSize64: maxRestoreFileBytes},
			{Name: "uploads/i.bin", UncompressedSize64: maxRestoreFileBytes},
			{Name: "uploads/j.bin", UncompressedSize64: maxRestoreFileBytes},
			{Name: "uploads/k.bin", UncompressedSize64: maxRestoreFileBytes},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateRestoreArchive(fakeRestoreArchive(tc.headers...)); err == nil {
				t.Fatal("expected archive to be rejected")
			}
		})
	}
}

func TestValidateRestoreArchiveRejectsUnsupportedEntryTypes(t *testing.T) {
	var link zip.FileHeader
	link.Name = "uploads/link"
	link.SetMode(os.ModeSymlink | 0o777)
	if err := validateRestoreArchive(fakeRestoreArchive(link)); err == nil {
		t.Fatal("expected symlink archive entry to be rejected")
	}
}

func TestValidateRestoreArchiveRejectsOversizedManifestMetadata(t *testing.T) {
	archive := fakeRestoreArchive(zip.FileHeader{
		Name:               legacyManifestName,
		UncompressedSize64: maxRestoreManifestBytes + 1,
	})
	if err := validateRestoreArchive(archive); err == nil {
		t.Fatal("expected oversized manifest metadata to be rejected")
	}
}

func TestReadManifestEntryBoundsDecompressedBytes(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create(legacyManifestName)
	if err != nil {
		t.Fatal(err)
	}
	zeros := make([]byte, 32*1024)
	remaining := int64(maxRestoreManifestBytes + 1)
	for remaining > 0 {
		n := int64(len(zeros))
		if remaining < n {
			n = remaining
		}
		if _, err := entry.Write(zeros[:n]); err != nil {
			t.Fatal(err)
		}
		remaining -= n
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = readManifestEntry(reader.File[0])
	var formatErr *BackupFormatError
	if !errors.As(err, &formatErr) || formatErr.Code != FormatMalformedManifestJSON {
		t.Fatalf("read oversized decompressed manifest error = %v, want malformed-manifest size error", err)
	}
}

func TestRestoreRejectsOversizedArchiveBeforeReading(t *testing.T) {
	_, _, service, _, cleanup := newBackupService(t)
	defer cleanup()
	reader := bytes.NewReader(nil)
	err := service.Restore(t.Context(), reader, maxRestoreArchiveBytes+1)
	if err == nil {
		t.Fatal("expected oversized archive to be rejected")
	}
}
