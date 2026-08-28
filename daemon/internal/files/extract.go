package files

import (
	"archive/zip"
	"compress/flate"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"PrismPanel-daemon/internal/apperr"
)

const extractTaskTTL = time.Hour

type ExtractInput struct {
	Destination    string `json:"destination"`
	ConflictPolicy string `json:"conflict_policy"`
	Password       string `json:"password"`
	Encoding       string `json:"encoding"`
	DirectoryMode  string `json:"directory_mode"`
}

type ExtractTask struct {
	ID             string        `json:"id"`
	ResourceType   string        `json:"resource_type"`
	ResourceID     string        `json:"resource_id"`
	Source         string        `json:"source"`
	Destination    string        `json:"destination"`
	ConflictPolicy string        `json:"conflict_policy"`
	Encoding       string        `json:"encoding"`
	DirectoryMode  string        `json:"directory_mode"`
	Status         string        `json:"status"`
	Stage          string        `json:"stage"`
	Message        string        `json:"message"`
	FilesTotal     int64         `json:"files_total"`
	FilesDone      int64         `json:"files_done"`
	Directories    int64         `json:"directories"`
	BytesTotal     int64         `json:"bytes_total"`
	BytesDone      int64         `json:"bytes_done"`
	CurrentFile    string        `json:"current_file,omitempty"`
	Skipped        int64         `json:"skipped"`
	StartedAt      time.Time     `json:"started_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	CompletedAt    *time.Time    `json:"completed_at,omitempty"`
	Error          *apperr.Error `json:"error,omitempty"`
	password       string
	directoryPerm  os.FileMode
	mu             *sync.Mutex
}

func (s *Service) StartExtract(target Target, source string, input ExtractInput) (ExtractTask, error) {
	source, err := normalizeRelative(source)
	if err != nil || source == "." || !strings.EqualFold(filepath.Ext(source), ".zip") {
		return ExtractTask{}, apperr.New("INVALID_ARCHIVE", "只能解压 ZIP 文件")
	}
	destination, err := normalizeRelative(input.Destination)
	if err != nil {
		return ExtractTask{}, err
	}
	policy := strings.ToLower(strings.TrimSpace(input.ConflictPolicy))
	if policy == "" {
		policy = "overwrite"
	}
	if policy != "overwrite" && policy != "skip" && policy != "rename" {
		return ExtractTask{}, apperr.New("INVALID_REQUEST", "解压冲突策略无效")
	}
	encoding := strings.ToLower(strings.TrimSpace(input.Encoding))
	if encoding == "" {
		encoding = "utf-8"
	}
	if encoding != "utf-8" && encoding != "gbk" {
		return ExtractTask{}, apperr.New("INVALID_REQUEST", "解压编码无效")
	}
	directoryMode := strings.TrimSpace(input.DirectoryMode)
	if directoryMode == "" {
		directoryMode = "755"
	}
	directoryPerm, err := parseDirectoryMode(directoryMode)
	if err != nil {
		return ExtractTask{}, err
	}
	id, err := extractTaskID()
	if err != nil {
		return ExtractTask{}, apperr.Wrap("INTERNAL", "无法创建解压任务", err)
	}
	now := time.Now().UTC()
	task := &ExtractTask{
		ID: id, ResourceType: target.Type, ResourceID: target.ID, Source: source,
		Destination: destination, ConflictPolicy: policy, Encoding: encoding, DirectoryMode: directoryMode, Status: "pending",
		Stage: "queued", Message: "等待解压", StartedAt: now, UpdatedAt: now,
		password: input.Password, directoryPerm: directoryPerm,
		mu: &sync.Mutex{},
	}
	s.extractMu.Lock()
	s.cleanupExtractTasksLocked(now)
	s.extracts[id] = task
	s.extractMu.Unlock()
	go s.runExtract(target, task)
	return task.snapshot(), nil
}

func (s *Service) ExtractStatus(target Target, taskID string) (ExtractTask, error) {
	s.extractMu.Lock()
	task := s.extracts[strings.TrimSpace(taskID)]
	s.extractMu.Unlock()
	if task == nil || task.ResourceType != target.Type || task.ResourceID != target.ID {
		return ExtractTask{}, apperr.New("FILE_TASK_NOT_FOUND", "解压任务不存在或已过期")
	}
	return task.snapshot(), nil
}

func (s *Service) runExtract(target Target, task *ExtractTask) {
	task.update(func(item *ExtractTask) {
		item.Status, item.Stage, item.Message = "running", "preflight", "正在检查压缩包"
	})
	err := s.mutate(target, func(root string) error {
		return s.extractZIP(root, task)
	})
	now := time.Now().UTC()
	if err != nil {
		apiError := apperr.Describe(err, task.currentStage(), false,
			"压缩文件："+task.Source, "目标目录："+task.Destination)
		task.update(func(item *ExtractTask) {
			item.password = ""
			item.Status, item.Message, item.Error, item.CompletedAt = "failed", apiError.Message, apiError, &now
		})
		return
	}
	task.update(func(item *ExtractTask) {
		item.password = ""
		item.Status, item.Stage, item.Message, item.CurrentFile = "done", "done", "解压完成", ""
		item.CompletedAt = &now
	})
}

func (s *Service) extractZIP(root string, task *ExtractTask) error {
	sourcePath, err := securePath(root, task.Source, false)
	if err != nil {
		return err
	}
	destinationPath, err := securePath(root, task.Destination, true)
	if err != nil {
		return err
	}
	reader, err := zip.OpenReader(sourcePath)
	if err != nil {
		return apperr.Wrap("INVALID_ARCHIVE", "压缩包格式无效或文件已损坏", err)
	}
	defer reader.Close()
	entries, summary, err := s.validateArchiveEncoding(&reader.Reader, task.Encoding)
	if err != nil {
		return err
	}
	task.update(func(item *ExtractTask) {
		item.FilesTotal = int64(summary.Files)
		item.Directories = int64(summary.Directories)
		item.BytesTotal = summary.Bytes
		item.Stage, item.Message = "extracting", "正在解压文件"
	})
	stage, err := os.MkdirTemp(filepath.Dir(destinationPath), ".prism-extract-*")
	if err != nil {
		return fileError(err, "无法创建解压临时目录")
	}
	defer os.RemoveAll(stage)
	for _, entry := range entries {
		if err := extractTaskEntry(stage, entry, s.maxExtract, task); err != nil {
			return err
		}
	}
	task.update(func(item *ExtractTask) {
		item.Stage, item.Message, item.CurrentFile = "publishing", "正在写入目标目录", ""
	})
	if err := os.MkdirAll(destinationPath, 0o750); err != nil {
		return fileError(err, "无法创建解压目标目录")
	}
	return publishExtracted(root, stage, destinationPath, task, task.directoryPerm)
}

func extractTaskEntry(stage string, entry archiveEntry, maxBytes int64, task *ExtractTask) error {
	target := filepath.Join(stage, filepath.FromSlash(entry.path))
	if !pathWithin(stage, target) {
		return apperr.New("INVALID_ARCHIVE", "压缩包文件路径越出工作目录")
	}
	task.update(func(item *ExtractTask) { item.CurrentFile = entry.path })
	if entry.file.FileInfo().IsDir() {
		if err := os.MkdirAll(target, 0o750); err != nil {
			return fileError(err, "压缩包目录创建失败")
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return fileError(err, "压缩包目录创建失败")
	}
	input, err := openExtractEntry(entry.file, task.password)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fileError(err, "压缩包文件创建失败")
	}
	written, copyErr := copyExtractProgress(output, input, maxBytes, task)
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return fileError(err, "压缩包条目解压失败")
	}
	if uint64(written) != entry.file.UncompressedSize64 {
		return apperr.New("INVALID_ARCHIVE", "压缩包条目大小与声明不一致")
	}
	if checksum, checksumErr := fileCRC32(target); checksumErr != nil {
		return fileError(checksumErr, "无法校验解压文件")
	} else if checksum != entry.file.CRC32 {
		return apperr.New("INVALID_PASSWORD", "解压密码错误或压缩包已损坏")
	}
	permission := entry.file.Mode().Perm()
	if permission == 0 {
		permission = 0o640
	}
	if err := os.Chmod(target, permission); err != nil {
		return fileError(err, "无法设置解压文件权限")
	}
	task.update(func(item *ExtractTask) { item.FilesDone++ })
	return nil
}

func copyExtractProgress(output io.Writer, input io.Reader, maxBytes int64, task *ExtractTask) (int64, error) {
	buffer := make([]byte, 256*1024)
	var written int64
	for {
		count, readErr := input.Read(buffer)
		if count > 0 {
			if task.bytesDone()+int64(count) > maxBytes {
				return written, apperr.New("FILE_TOO_LARGE", "压缩包实际解压大小超过限制")
			}
			outputCount, writeErr := output.Write(buffer[:count])
			written += int64(outputCount)
			task.update(func(item *ExtractTask) { item.BytesDone += int64(outputCount) })
			if writeErr != nil || outputCount != count {
				return written, errors.Join(writeErr, io.ErrShortWrite)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func publishExtracted(root, stage, destination string, task *ExtractTask, directoryPerm os.FileMode) error {
	directoryTargets := map[string]string{".": destination}
	directories := []string{destination}
	err := filepath.WalkDir(stage, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if source == stage {
			return nil
		}
		relative, err := filepath.Rel(stage, source)
		if err != nil {
			return err
		}
		parentTarget := directoryTargets[filepath.Dir(relative)]
		if parentTarget == "" {
			parentTarget = filepath.Join(destination, filepath.Dir(relative))
		}
		target := filepath.Join(parentTarget, filepath.Base(relative))
		if entry.IsDir() {
			info, err := os.Lstat(target)
			if err == nil && info.IsDir() {
				directoryTargets[relative] = target
				directories = append(directories, target)
				return nil
			}
			if err == nil {
				switch task.ConflictPolicy {
				case "skip":
					task.update(func(item *ExtractTask) { item.Skipped++ })
					return filepath.SkipDir
				case "rename":
					target = availableExtractPath(target)
				case "overwrite":
					if err := recycleExtractTarget(root, target); err != nil {
						return err
					}
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return fileError(err, "无法检查解压目标目录")
			}
			if err := os.MkdirAll(target, 0o750); err != nil {
				return fileError(err, "无法创建解压目标目录")
			}
			directoryTargets[relative] = target
			directories = append(directories, target)
			return nil
		}
		if info, err := os.Lstat(target); err == nil {
			switch task.ConflictPolicy {
			case "skip":
				task.update(func(item *ExtractTask) { item.Skipped++ })
				return nil
			case "rename":
				target = availableExtractPath(target)
			case "overwrite":
				if info.IsDir() {
					return apperr.New("FILE_EXISTS", "解压目标存在同名目录，无法用文件覆盖")
				}
				if err := recycleExtractTarget(root, target); err != nil {
					return err
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fileError(err, "无法检查解压目标文件")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fileError(err, "无法创建解压目标目录")
		}
		if err := os.Rename(source, target); err != nil {
			return fileError(err, "无法发布解压文件")
		}
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Chmod(directories[index], directoryPerm); err != nil {
			return fileError(err, "无法设置解压目录权限")
		}
	}
	return nil
}

func recycleExtractTarget(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
		return apperr.New("PATH_ESCAPE", "解压目标路径无效")
	}
	return moveToRecycleBin(root, relative)
}

func parseDirectoryMode(value string) (os.FileMode, error) {
	if len(value) != 3 {
		return 0, apperr.New("INVALID_REQUEST", "目录权限必须是 000 到 777 的八进制数")
	}
	parsed, err := strconv.ParseUint(value, 8, 12)
	if err != nil || parsed > 0o777 {
		return 0, apperr.New("INVALID_REQUEST", "目录权限必须是 000 到 777 的八进制数")
	}
	return os.FileMode(parsed), nil
}

func openExtractEntry(file *zip.File, password string) (io.ReadCloser, error) {
	if file.Flags&1 == 0 {
		input, err := file.Open()
		if err != nil {
			return nil, apperr.Wrap("INVALID_ARCHIVE", "压缩包条目读取失败", err)
		}
		return input, nil
	}
	if password == "" {
		return nil, apperr.New("PASSWORD_REQUIRED", "压缩包需要解压密码")
	}
	if file.Method == 99 {
		return nil, apperr.New("UNSUPPORTED_ARCHIVE_ENCRYPTION", "暂不支持 AES 加密的 ZIP 压缩包")
	}
	raw, err := file.OpenRaw()
	if err != nil {
		return nil, apperr.Wrap("INVALID_ARCHIVE", "无法读取加密压缩包条目", err)
	}
	decrypted, err := newZipCryptoReader(raw, []byte(password), zipPasswordCheckByte(file))
	if err != nil {
		return nil, err
	}
	switch file.Method {
	case zip.Store:
		return io.NopCloser(decrypted), nil
	case zip.Deflate:
		return flate.NewReader(decrypted), nil
	default:
		return nil, apperr.New("INVALID_ARCHIVE", "加密压缩包使用了不支持的压缩算法")
	}
}

func zipPasswordCheckByte(file *zip.File) byte {
	if file.Flags&8 != 0 {
		return byte(file.ModifiedTime >> 8)
	}
	return byte(file.CRC32 >> 24)
}

type zipCryptoReader struct {
	input io.Reader
	keys  [3]uint32
}

func newZipCryptoReader(input io.Reader, password []byte, check byte) (*zipCryptoReader, error) {
	reader := &zipCryptoReader{input: input, keys: [3]uint32{0x12345678, 0x23456789, 0x34567890}}
	for _, value := range password {
		reader.updateKeys(value)
	}
	header := make([]byte, 12)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, apperr.New("INVALID_ARCHIVE", "加密压缩包头无效")
	}
	if header[11] != check {
		return nil, apperr.New("INVALID_PASSWORD", "解压密码错误")
	}
	return reader, nil
}

func (reader *zipCryptoReader) Read(buffer []byte) (int, error) {
	count, err := reader.input.Read(buffer)
	for index := 0; index < count; index++ {
		value := buffer[index] ^ reader.decryptByte()
		buffer[index] = value
		reader.updateKeys(value)
	}
	return count, err
}

func (reader *zipCryptoReader) decryptByte() byte {
	temporary := reader.keys[2] | 2
	return byte((temporary * (temporary ^ 1)) >> 8)
}

func (reader *zipCryptoReader) updateKeys(value byte) {
	reader.keys[0] = zipCryptoCRC32(reader.keys[0], value)
	reader.keys[1] = reader.keys[1] + uint32(byte(reader.keys[0]))
	reader.keys[1] = reader.keys[1]*134775813 + 1
	reader.keys[2] = zipCryptoCRC32(reader.keys[2], byte(reader.keys[1]>>24))
}

func zipCryptoCRC32(checksum uint32, value byte) uint32 {
	return crc32.IEEETable[byte(checksum)^value] ^ checksum>>8
}

func fileCRC32(path string) (uint32, error) {
	input, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	hash := crc32.NewIEEE()
	_, err = io.Copy(hash, input)
	return hash.Sum32(), err
}

func availableExtractPath(original string) string {
	extension := filepath.Ext(original)
	base := strings.TrimSuffix(original, extension)
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, index, extension)
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

func (task *ExtractTask) update(change func(*ExtractTask)) {
	task.mu.Lock()
	change(task)
	task.UpdatedAt = time.Now().UTC()
	task.mu.Unlock()
}

func (task *ExtractTask) snapshot() ExtractTask {
	task.mu.Lock()
	defer task.mu.Unlock()
	result := *task
	result.mu = &sync.Mutex{}
	if task.Error != nil {
		copyError := *task.Error
		copyError.Details = append([]string(nil), task.Error.Details...)
		result.Error = &copyError
	}
	return result
}

func (task *ExtractTask) bytesDone() int64 {
	task.mu.Lock()
	defer task.mu.Unlock()
	return task.BytesDone
}

func (task *ExtractTask) currentStage() string {
	task.mu.Lock()
	defer task.mu.Unlock()
	return task.Stage
}

func (s *Service) cleanupExtractTasksLocked(now time.Time) {
	for id, task := range s.extracts {
		snapshot := task.snapshot()
		if snapshot.CompletedAt != nil && now.Sub(*snapshot.CompletedAt) > extractTaskTTL {
			delete(s.extracts, id)
		}
	}
}

func extractTaskID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
