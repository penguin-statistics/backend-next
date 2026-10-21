package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"

	"github.com/pkg/errors"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/repo"
)

var (
	ErrSnapshotNotFound    = errors.New("snapshot not found")
	ErrSnapshotNonNullable = errors.New("snapshot content cannot be empty")
)

type Snapshot struct {
	SnapshotRepo *repo.Snapshot
}

func NewSnapshot(snapshotRepo *repo.Snapshot) *Snapshot {
	return &Snapshot{
		SnapshotRepo: snapshotRepo,
	}
}

func (s *Snapshot) SaveSnapshot(ctx context.Context, key string, content string) (*model.Snapshot, error) {
	if content == "" {
		return nil, ErrSnapshotNonNullable
	}
	version := s.CalculateVersion(content)
	entity := &model.Snapshot{
		Key:     key,
		Version: version,
		Content: content,
	}
	return s.SnapshotRepo.SaveSnapshot(ctx, entity)
}

func (s *Snapshot) CalculateVersion(content string) string {
	sha := sha1.Sum([]byte(content))
	return hex.EncodeToString(sha[:])
}
