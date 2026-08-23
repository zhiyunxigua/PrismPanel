package supervisor

import (
	"io"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func decodeConsoleOutput(encoding string, reader io.Reader) io.Reader {
	if strings.EqualFold(encoding, "gbk") {
		return transform.NewReader(reader, simplifiedchinese.GBK.NewDecoder())
	}
	return reader
}

func writeConsoleInput(writer io.Writer, encoding, value string) error {
	if strings.EqualFold(encoding, "gbk") {
		encoded, _, err := transform.String(simplifiedchinese.GBK.NewEncoder(), value)
		if err != nil {
			return err
		}
		_, err = io.WriteString(writer, encoded)
		return err
	}
	_, err := io.WriteString(writer, value)
	return err
}
