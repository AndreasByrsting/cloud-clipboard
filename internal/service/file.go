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
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(content)
	return err
}

func (s *FileService) FinalizeChunkTarget(path string, originalName string) (StoredFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return StoredFile{}, err
	}
	finalPath := strings.TrimSuffix(path, ".partial"+filepath.Ext(path)) + filepath.Ext(originalName)
	if err := os.Rename(path, finalPath); err != nil {
		return StoredFile{}, err
	}
	return StoredFile{OriginalName: originalName, StoredPath: finalPath, Size: info.Size()}, nil
}

func (s *FileService) Delete(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *FileService) Read(path string) ([]byte, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return content, mimeType, nil
}
