package ipc

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"myguo/internal/hgapi"
)

func adaptationOffset(seriesID, cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 3 || parts[0] != "related1" || parts[1] != seriesID {
		return 0, fmt.Errorf("改编作品游标不属于当前短剧")
	}
	offset, err := strconv.Atoi(parts[2])
	if err != nil || offset <= 0 || offset > 1000000 {
		return 0, fmt.Errorf("改编作品游标无效")
	}
	return offset, nil
}

func (s *HongguoService) officialAdaptations(ctx context.Context, seriesID, cursor string, exclude map[string]bool) (*SeriesCollectionDTO, error) {
	offset, err := adaptationOffset(seriesID, cursor)
	if err != nil {
		return nil, err
	}
	api, err := s.requestAPI(ctx)
	if err != nil {
		return nil, err
	}
	cells, err := api.RelatedWorks(ctx, seriesID, offset)
	if err != nil {
		return nil, err
	}
	out := &SeriesCollectionDTO{Title: "同一剧本改编", Items: []SeriesCard{}, Seasons: []SeriesCard{}}
	seen := map[string]bool{}
	for _, cell := range cells {
		// In this scene 547 contains both relations. Partition by the explicit
		// relation tag, not the title or season_index (other versions have seasons
		// too). Never accept guess cells.
		if cell.ShowType != 547 {
			continue
		}
		if cell.HasMore {
			if cell.NextOffset <= offset {
				return nil, fmt.Errorf("相关作品分页没有前进")
			}
			out.HasMore = true
			out.Cursor = fmt.Sprintf("related1:%s:%d", seriesID, cell.NextOffset)
		}
		for _, item := range cell.Items {
			tag := strings.TrimSpace(item.Tag.Text)
			season := strings.HasPrefix(tag, "第") && strings.HasSuffix(tag, "季") && len([]rune(tag)) > 2
			if (!season && tag != "同IP") || !validContentID(item.ID) || item.ID == seriesID || seen[item.ID] || (!season && exclude[item.ID]) {
				continue
			}
			seen[item.ID] = true
			upcoming := isUpcomingSeries(item.Episodes, item.RightText)
			status, heat := "", ""
			if upcoming {
				status, heat = "upcoming", "待上线"
			}
			card := SeriesCard{ID: item.ID, Title: item.Title, Description: item.Description, CoverURL: s.media.ImageURL(item.Cover), EpisodeCount: item.Episodes, ContentType: item.ContentType, Category: seriesContentTypeLabel(item.ContentType), Upcoming: upcoming, Status: status, Heat: heat, Reserved: s.store.HasReservation(item.ID)}
			if season {
				card.BadgeText = tag
				out.Seasons = append(out.Seasons, card)
			} else {
				out.Items = append(out.Items, card)
			}
		}
	}
	return sanitizeIPC(out), nil
}

type ReviewDTO = hgapi.ReviewDTO
type ReviewPageDTO = hgapi.ReviewPageDTO

func (s *HongguoService) GetSeriesReviews(ctx context.Context, seriesID, cursor string) (*ReviewPageDTO, error) {
	if !validContentID(seriesID) {
		return nil, fmt.Errorf("无效短剧 ID")
	}
	if err := validCommentCursor(cursor); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	api, err := s.requestAPI(ctx)
	if err != nil {
		return nil, err
	}
	page, err := api.SeriesReviews(ctx, seriesID, cursor)
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		page.Items[i].AvatarURL = s.media.ImageURL(page.Items[i].AvatarURL)
	}
	return sanitizeIPC(page), nil
}

func (s *HongguoService) GetSeriesReviewReplies(ctx context.Context, seriesID, reviewID, cursor string) (*CommentPageDTO, error) {
	if !validContentID(seriesID) || !validContentID(reviewID) {
		return nil, fmt.Errorf("无效剧评 ID")
	}
	if err := validReplyCursor(cursor); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	api, err := s.requestAPI(ctx)
	if err != nil {
		return nil, err
	}
	page, err := api.SeriesReviewReplies(ctx, seriesID, reviewID, cursor)
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		page.Items[i].AvatarURL = s.media.ImageURL(page.Items[i].AvatarURL)
	}
	return sanitizeIPC(page), nil
}
