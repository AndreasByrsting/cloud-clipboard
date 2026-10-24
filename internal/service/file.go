package service

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type StoredFile struct {
	OriginalName string
	StoredPath   string
	Size         int64
}

type FileService struct {
	uploadDir string
}

func NewFileService(uploadDir string) *FileService {
	return &FileService{uploadDir: uploadDir}
}

func (s *FileService) Save(name string, content []byte, now time.Time) (StoredFile, error) {
	if strings.TrimSpace(name) == "" {
		name = "file"
	}
	if err := os.MkdirAll(s.uploadDir, 0o755); err != nil {
		return StoredFile{}, err
	}
	token, err := GenerateToken()
	if err != nil {
		return StoredFile{}, err
	}
	ext := filepath.Ext(name)
	storedName := fmt.Sprintf("%d_%s%s", now.UnixNano(), token[:12], ext)
	storedPath := filepath.Join(s.uploadDir, storedName)
	if err := os.WriteFile(storedPath, content, 0o644); err != nil {
		return StoredFile{}, err
	}
	return StoredFile{OriginalName: name, StoredPath: storedPath, Size: int64(len(content))}, nil
}

func (s *FileService) CreateChunkTarget(name string, now time.Time) (StoredFile, error) {
	if strings.TrimSpace(name) == "" {
		name = "file"
	}
	if err := os.MkdirAll(s.uploadDir, 0o755); err != nil {
		return StoredFile{}, err
	}
	token, err := GenerateToken()
	if err != nil {
		return StoredFile{}, err
	}
	ext := filepath.Ext(name)
	storedName := fmt.Sprintf("%d_%s.partial%s", now.UnixNano(), token[:12], ext)
	storedPath := filepath.Join(s.uploadDir, storedName)
	file, err := os.Create(storedPath)
	if err != nil {
		return StoredFile{}, err
	}
	_ = file.Close()
	return StoredFile{OriginalName: name, StoredPath: storedPath, Size: 0}, nil
}

func (s *FileService) AppendChunk(path string, content []byte) error {
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(resolved, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(content)
	return err
}

func (s *FileService) FinalizeChunkTarget(path string, originalName string) (StoredFile, error) {
	resolved, err := s.resolve(path)
	if err != nil {
		return StoredFile{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return StoredFile{}, err
	}
	finalPath := strings.TrimSuffix(resolved, ".partial"+filepath.Ext(resolved)) + filepath.Ext(originalName)
	if err := os.Rename(resolved, finalPath); err != nil {
		return StoredFile{}, err
	}
	return StoredFile{OriginalName: originalName, StoredPath: finalPath, Size: info.Size()}, nil
}

func (s *FileService) Delete(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}
	if err := os.Remove(resolved); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *FileService) Read(path string) ([]byte, string, error) {
	resolved, err := s.resolve(path)
	if err != nil {
		return nil, "", err
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, "", err
	}
	mimeType := mime.TypeByExtension(filepath.Ext(resolved))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return content, mimeType, nil
}

// Open returns a file handle suitable for http.ServeContent. Unlike Read it
// never buffers the whole file in memory.
func (s *FileService) Open(path string) (*os.File, os.FileInfo, error) {
	resolved, err := s.resolve(path)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if info.IsDir() {
		_ = file.Close()
		return nil, nil, fmt.Errorf("path is a directory")
	}
	return file, info, nil
}

// resolve makes sure a stored path stays inside the configured upload
// directory before any filesystem operation touches it.
func (s *FileService) resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty file path")
	}
	root, err := filepath.Abs(s.uploadDir)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file path is outside the upload directory")
	}
	return target, nil
}
