package supervisor

import (
	"bytes"
	"io"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

type singleByteReader struct {
	contents []byte
}

func (r *singleByteReader) Read(buffer []byte) (int, error) {
	if len(r.contents) == 0 {
		return 0, io.EOF
	}
	buffer[0] = r.contents[0]
	r.contents = r.contents[1:]
	return 1, nil
}

func TestDecodeGBKConsoleOutputAcrossReadBoundaries(t *testing.T) {
	encoded, _, err := transform.String(simplifiedchinese.GBK.NewEncoder(), "服务器已启动\n")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(decodeConsoleOutput("gbk", &singleByteReader{contents: []byte(encoded)}))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "服务器已启动\n" {
		t.Fatalf("unexpected decoded output %q", decoded)
	}
}

func TestWriteGBKConsoleInput(t *testing.T) {
	var output bytes.Buffer
	if err := writeConsoleInput(&output, "gbk", "说 你好\n"); err != nil {
		t.Fatal(err)
	}
	decoded, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), output.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "说 你好\n" {
		t.Fatalf("unexpected encoded command %q", decoded)
	}
}

func TestDecodeGBKSessionContentPreservesChineseText(t *testing.T) {
	raw, _, err := transform.String(simplifiedchinese.GBK.NewEncoder(), "服务器已启动")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSessionContent("gbk", []byte(raw), ""); got != "服务器已启动" {
		t.Fatalf("decoded content = %q", got)
	}
}
