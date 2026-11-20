package ipc

import (
	"context"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/mediautil"
)

func validContentID(id string) bool { return mediautil.ValidContentID(id) }

type CommentDTO = hgapi.CommentDTO
type CommentPageDTO = hgapi.CommentPageDTO

func validCommentCursor(v string) error { return hgapi.ValidCommentCursor(v) }
func validReplyCursor(v string) error   { return hgapi.ValidReplyCursor(v) }

func (s *HongguoService) GetEpisodeComments(ctx context.Context, seriesID, episodeID, cursor string) (*CommentPageDTO, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	api, err := s.requestAPI(ctx)
	if err != nil {
		return nil, err
	}
	page, err := api.EpisodeComments(ctx, seriesID, episodeID, cursor)
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		page.Items[i].AvatarURL = s.media.ImageURL(page.Items[i].AvatarURL)
	}
	return sanitizeIPC(page), nil
}
func (s *HongguoService) GetEpisodeCommentReplies(ctx context.Context, seriesID, episodeID, commentID, cursor string) (*CommentPageDTO, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	api, err := s.requestAPI(ctx)
	if err != nil {
		return nil, err
	}
	page, err := api.EpisodeCommentReplies(ctx, seriesID, episodeID, commentID, cursor)
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		page.Items[i].AvatarURL = s.media.ImageURL(page.Items[i].AvatarURL)
	}
	return sanitizeIPC(page), nil
}
