package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"

	"github.com/gabstv/go-bsdiff/pkg/bsdiff"
	"github.com/pkg/errors"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/pkg/pgerr"
	"exusiai.dev/backend-next/internal/repo"
)

var (
	ErrSnapshotNotFound            = errors.New("snapshot not found")
	ErrSnapshotNonNullable         = errors.New("snapshot content cannot be empty")
	ErrSnapshotFromVersionNotFound = pgerr.ErrInvalidReq.Msg("snapshot matching `from` version not found")
	ErrSnapshotToVersionNotFound   = pgerr.ErrInvalidReq.Msg("snapshot matching `to` version not found")
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

func (s *Snapshot) GetDiffBetweenVersions(ctx context.Context, key, fromVersion, toVersion string) ([]byte, error) {
	snapshots, err := s.SnapshotRepo.GetSnapshotsByVersions(ctx, key, []string{fromVersion, toVersion})
	if err != nil {
		return nil, err
	}

	var fromContent, toContent null.String
	for _, snapshot := range snapshots {
		if snapshot.Version == fromVersion {
			fromContent = null.NewString(snapshot.Content, true)
		} else if snapshot.Version == toVersion {
			toContent = null.NewString(snapshot.Content, true)
		}
	}

	if !fromContent.Valid {
		return nil, ErrSnapshotFromVersionNotFound
	} else if !toContent.Valid {
		return nil, ErrSnapshotToVersionNotFound
	}

	fromBytes := []byte(fromContent.String)
	toBytes := []byte(toContent.String)

	return bsdiff.Bytes(fromBytes, toBytes)
}

func (s *Snapshot) CalculateVersion(content string) string {
	sha := sha1.Sum([]byte(content))
	return hex.EncodeToString(sha[:])
}
