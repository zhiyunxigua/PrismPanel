package files

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestExtractZIPConflictPoliciesAndProgress(t *testing.T) {
	tests := []struct {
		policy       string
		expectedMain string
		expectedCopy string
		skipped      int64
	}{
		{policy: "overwrite", expectedMain: "new"},
		{policy: "skip", expectedMain: "old", skipped: 1},
		{policy: "rename", expectedMain: "old", expectedCopy: "new"},
	}
	for _, test := range tests {
		t.Run(test.policy, func(t *testing.T) {
			service, target, root := newTestService(t)
			archive := makeZIP(t, []zipTestEntry{
				{name: "item.txt", contents: "new"},
				{name: "nested/", directory: true},
				{name: "nested/value.txt", contents: "value"},
			})
			if err := os.WriteFile(filepath.Join(root, "bundle.zip"), archive, 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "output"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "output", "item.txt"), []byte("old"), 0o640); err != nil {
				t.Fatal(err)
			}

			task, err := service.StartExtract(target, "bundle.zip", ExtractInput{Destination: "output", ConflictPolicy: test.policy})
			if err != nil {
				t.Fatal(err)
			}
			status := waitExtractTask(t, service, target, task.ID)
			if status.Status != "done" || status.Stage != "done" || status.FilesDone != 2 || status.BytesDone != 8 || status.Skipped != test.skipped {
				t.Fatalf("unexpected extract status: %#v", status)
			}
			assertFileContents(t, filepath.Join(root, "output", "item.txt"), test.expectedMain)
			assertFileContents(t, filepath.Join(root, "output", "nested", "value.txt"), "value")
			entries, recycleErr := service.RecycleList(target)
			if recycleErr != nil {
				t.Fatal(recycleErr)
			}
			if test.policy == "overwrite" && (len(entries) != 1 || entries[0].OriginalPath != "output/item.txt") {
				t.Fatalf("overwritten file was not preserved in recycle bin: %#v", entries)
			}
			if test.policy != "overwrite" && len(entries) != 0 {
				t.Fatalf("non-overwrite conflict policy populated recycle bin: %#v", entries)
			}
			if test.expectedCopy != "" {
				assertFileContents(t, filepath.Join(root, "output", "item (1).txt"), test.expectedCopy)
			}
		})
	}
}

func TestExtractZIPRejectsUnsafePathWithoutPublishing(t *testing.T) {
	service, target, root := newTestService(t)
	archive := makeZIP(t, []zipTestEntry{{name: "../outside.txt", contents: "outside"}})
	if err := os.WriteFile(filepath.Join(root, "unsafe.zip"), archive, 0o640); err != nil {
		t.Fatal(err)
	}
	task, err := service.StartExtract(target, "unsafe.zip", ExtractInput{Destination: "output"})
	if err != nil {
		t.Fatal(err)
	}
	status := waitExtractTask(t, service, target, task.ID)
	if status.Status != "failed" || status.Error == nil || status.Error.Code != "INVALID_ARCHIVE" {
		t.Fatalf("unsafe archive was not rejected: %#v", status)
	}
	if _, err := os.Stat(filepath.Join(root, "output")); !os.IsNotExist(err) {
		t.Fatalf("unsafe archive published output: %v", err)
	}
}

func TestExtractZIPAppliesDirectoryModeWithoutChangingFileMode(t *testing.T) {
	service, target, root := newTestService(t)
	archive := makeZIP(t, []zipTestEntry{
		{name: "nested/", directory: true},
		{name: "nested/value.txt", contents: "value", mode: 0o640},
	})
	if err := os.WriteFile(filepath.Join(root, "bundle.zip"), archive, 0o640); err != nil {
		t.Fatal(err)
	}
	task, err := service.StartExtract(target, "bundle.zip", ExtractInput{Destination: "output", DirectoryMode: "755"})
	if err != nil {
		t.Fatal(err)
	}
	status := waitExtractTask(t, service, target, task.ID)
	if status.Status != "done" {
		t.Fatalf("unexpected extract status: %#v", status)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, directory := range []string{"output", filepath.Join("output", "nested")} {
		info, statErr := os.Stat(filepath.Join(root, directory))
		if statErr != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("directory %s mode = %v, %v", directory, info.Mode().Perm(), statErr)
		}
	}
	info, err := os.Stat(filepath.Join(root, "output", "nested", "value.txt"))
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("file mode = %v, %v", info.Mode().Perm(), err)
	}
}

func TestExtractZIPValidatesOptions(t *testing.T) {
	service, target, _ := newTestService(t)
	tests := []ExtractInput{
		{Destination: "output", Encoding: "shift-jis"},
		{Destination: "output", DirectoryMode: "888"},
		{Destination: "output", DirectoryMode: "75"},
	}
	for _, input := range tests {
		if _, err := service.StartExtract(target, "bundle.zip", input); err == nil {
			t.Fatalf("expected invalid options to be rejected: %#v", input)
		}
	}
}

func TestExtractZIPDecodesGBKFileName(t *testing.T) {
	service, target, root := newTestService(t)
	archive := makeZIP(t, []zipTestEntry{{name: string([]byte{0xb2, 0xe2, 0xca, 0xd4}) + ".txt", contents: "value"}})
	if err := os.WriteFile(filepath.Join(root, "bundle.zip"), archive, 0o640); err != nil {
		t.Fatal(err)
	}
	task, err := service.StartExtract(target, "bundle.zip", ExtractInput{Destination: "output", Encoding: "gbk"})
	if err != nil {
		t.Fatal(err)
	}
	status := waitExtractTask(t, service, target, task.ID)
	if status.Status != "done" {
		t.Fatalf("unexpected extract status: %#v", status)
	}
	assertFileContents(t, filepath.Join(root, "output", "测试.txt"), "value")
}

func TestExtractZIPWithTraditionalPassword(t *testing.T) {
	service, target, root := newTestService(t)
	archive := makeTraditionalEncryptedZIP(t, "secret.txt", []byte("protected"), "correct-password")
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(reader.File) != 1 || reader.File[0].Flags&1 == 0 {
		t.Fatalf("encrypted ZIP fixture is invalid: flags=%#x, %v", reader.File[0].Flags, err)
	}
	if err := os.WriteFile(filepath.Join(root, "bundle.zip"), archive, 0o640); err != nil {
		t.Fatal(err)
	}
	task, err := service.StartExtract(target, "bundle.zip", ExtractInput{Destination: "missing-password"})
	if err != nil {
		t.Fatal(err)
	}
	status := waitExtractTask(t, service, target, task.ID)
	if status.Status != "failed" || status.Error == nil || status.Error.Code != "PASSWORD_REQUIRED" {
		t.Fatalf("missing password was not rejected: %#v", status)
	}

	task, err = service.StartExtract(target, "bundle.zip", ExtractInput{Destination: "wrong", Password: "wrong-password"})
	if err != nil {
		t.Fatal(err)
	}
	status = waitExtractTask(t, service, target, task.ID)
	if status.Status != "failed" || status.Error == nil || status.Error.Code != "INVALID_PASSWORD" {
		t.Fatalf("wrong password was not rejected: %#v", status)
	}

	task, err = service.StartExtract(target, "bundle.zip", ExtractInput{Destination: "output", Password: "correct-password"})
	if err != nil {
		t.Fatal(err)
	}
	status = waitExtractTask(t, service, target, task.ID)
	if status.Status != "done" {
		t.Fatalf("unexpected extract status: %#v", status)
	}
	assertFileContents(t, filepath.Join(root, "output", "secret.txt"), "protected")
}

func makeTraditionalEncryptedZIP(t *testing.T, name string, contents []byte, password string) []byte {
	t.Helper()
	var plain bytes.Buffer
	writer := zip.NewWriter(&plain)
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(0o640)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	result := append([]byte(nil), plain.Bytes()...)
	originalCentralOffset := bytes.Index(result, []byte{'P', 'K', 1, 2})
	if originalCentralOffset < 0 {
		t.Fatal("missing central directory")
	}
	nameLength := int(binary.LittleEndian.Uint16(result[26:28]))
	extraLength := int(binary.LittleEndian.Uint16(result[28:30]))
	dataOffset := 30 + nameLength + extraLength
	plainSize := int(binary.LittleEndian.Uint32(result[originalCentralOffset+20 : originalCentralOffset+24]))
	check := byte(binary.LittleEndian.Uint16(result[10:12]) >> 8)
	encrypted := zipCryptoEncrypt([]byte(password), contents, check)
	updated := make([]byte, 0, len(result)+12)
	updated = append(updated, result[:dataOffset]...)
	updated = append(updated, encrypted...)
	updated = append(updated, result[dataOffset+plainSize:]...)
	binary.LittleEndian.PutUint16(updated[6:8], binary.LittleEndian.Uint16(updated[6:8])|1)
	binary.LittleEndian.PutUint32(updated[18:22], uint32(len(encrypted)))
	centralOffset := originalCentralOffset + 12
	binary.LittleEndian.PutUint16(updated[centralOffset+8:centralOffset+10], binary.LittleEndian.Uint16(updated[centralOffset+8:centralOffset+10])|1)
	binary.LittleEndian.PutUint32(updated[centralOffset+20:centralOffset+24], uint32(len(encrypted)))
	endOffset := bytes.Index(updated[centralOffset:], []byte{'P', 'K', 5, 6}) + centralOffset
	binary.LittleEndian.PutUint32(updated[endOffset+16:endOffset+20], uint32(centralOffset))
	return updated
}

func zipCryptoEncrypt(password, contents []byte, check byte) []byte {
	keys := [3]uint32{0x12345678, 0x23456789, 0x34567890}
	update := func(value byte) {
		keys[0] = crc32.IEEETable[byte(keys[0])^value] ^ keys[0]>>8
		keys[1] = (keys[1]+uint32(byte(keys[0])))*134775813 + 1
		keys[2] = crc32.IEEETable[byte(keys[2])^byte(keys[1]>>24)] ^ keys[2]>>8
	}
	for _, value := range password {
		update(value)
	}
	plain := append(make([]byte, 11), check)
	plain = append(plain, contents...)
	result := make([]byte, len(plain))
	for index, value := range plain {
		temporary := keys[2] | 2
		result[index] = value ^ byte((temporary*(temporary^1))>>8)
		update(value)
	}
	return result
}

func waitExtractTask(t *testing.T, service *Service, target Target, id string) ExtractTask {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := service.ExtractStatus(target, id)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status == "done" || status.Status == "failed" {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("extract task did not complete")
	return ExtractTask{}
}

func assertFileContents(t *testing.T, path, expected string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != expected {
		t.Fatalf("contents of %s = %q, %v", path, contents, err)
	}
}
