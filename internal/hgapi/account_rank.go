package hgapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"myguo/internal/hgapi/model"
)

type rankCursor struct {
	Offset  int      `json:"o"`
	Seen    []string `json:"s,omitempty"`
	Session string   `json:"x,omitempty"`
	Content string   `json:"c,omitempty"`
	Board   string   `json:"b,omitempty"`
}

func decodeRankCursor(token string) (rankCursor, error) {
	cur := rankCursor{}
	if token == "" {
		return cur, nil
	}
	payload, ok := strings.CutPrefix(token, "s1.")
	if !ok {
		return cur, fmt.Errorf("invalid ranking cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, &cur) != nil || cur.Offset < 0 {
		return rankCursor{}, fmt.Errorf("invalid ranking cursor")
	}
	return cur, nil
}

func encodeRankCursor(cur rankCursor) (string, error) {
	raw, err := json.Marshal(cur)
	if err != nil {
		return "", err
	}
	return "s1." + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (c *SessionClient) AccountShortRank(ctx context.Context, contentId, boardId, cursor string) (*model.ShortRanking, error) {
	cur, err := decodeRankCursor(cursor)
	if err != nil {
		return nil, err
	}
	if cur.Content != "" && contentId != "" && cur.Content != contentId {
		return nil, fmt.Errorf("ranking cursor belongs to another content")
	}
	if cur.Board != "" && boardId != "" && cur.Board != boardId {
		return nil, fmt.Errorf("ranking cursor belongs to another board")
	}
	if cur.Content != "" {
		contentId = cur.Content
	}
	if cur.Board != "" {
		boardId = cur.Board
	}

	deviceID, iid, cdid, openudid := c.session.DeviceID, c.session.InstallID, c.session.CDID, c.session.OpenUDID
	pairs := [][2]string{
		{"iid", iid},
		{"device_id", deviceID},
		{"ac", "wifi"},
		{"channel", "xiaomi_8662_64"},
		{"aid", "8662"},
		{"app_name", "novelread"},
		{"cdid", cdid},
		{"version_code", "73932"},
		{"version_name", "7.3.9.32"},
		{"device_platform", "android"},
		{"os", "android"},
		{"ssmix", "a"},
		{"device_type", "23127PN0CC"},
		{"device_brand", "Xiaomi"},
		{"language", "zh"},
		{"os_api", "34"},
		{"os_version", "14"},
		{"manifest_version_code", "73932"},
		{"resolution", "1200*2670"},
		{"dpi", "460"},
		{"update_version_code", "73932"},
		{"host_abi", "arm64-v8a"},
		{"dragon_device_type", "phone"},
		{"pv_player", "73932"},
		{"compliance_status", "0"},
		{"need_personal_recommend", "1"},
		{"player_so_load", "1"},
		{"is_android_pad_screen", "0"},
		{"openudid", openudid},
		{"cell_id", "7470092475068071998"},
		{"tab_type", "26"},
		{"selected_items", contentId},
	}
	if boardId != "" {
		pairs = append(pairs, [2]string{"sub_selected_items", boardId})
	}
	pairs = append(pairs,
		[2]string{"offset", strconv.Itoa(cur.Offset)},
		[2]string{"limit", "0"},
		[2]string{"client_req_type", "2"},
		[2]string{"client_template", "2"},
		[2]string{"unlimited_selector_change_type", map[bool]string{true: "2", false: "1"}[cur.Offset > 0]},
	)
	if len(cur.Seen) > 0 && contentId != "ranklist_celebrity" {
		pairs = append(pairs, [2]string{"filter_ids", strings.Join(cur.Seen, ",")})
	}
	if cur.Session != "" {
		pairs = append(pairs, [2]string{"session_id", cur.Session})
	}
	pairs = append(pairs, [2]string{"user_is_login", "1"})

	body, err := c.call(ctx, endpoint{"排行榜", accountOrigin, "/reading/bookapi/bookmall/cell/change/v1/", "GET", getSigned, 4 << 20}, pairs, nil, nil)
	if err != nil {
		return nil, err
	}
	return parseAccountShortRank(body, contentId, boardId, cur)
}

type shortRankID string

func (v *shortRankID) UnmarshalJSON(raw []byte) error {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		*v = ""
		return nil
	}
	if text[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		*v = shortRankID(value)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return err
	}
	*v = shortRankID(number.String())
	return nil
}

type shortRankOption struct {
	Name     string      `json:"show_name"`
	ID       shortRankID `json:"selector_item_id"`
	Selected bool        `json:"is_selected"`
	Sub      struct {
		OuterRow struct {
			Items []shortRankOption `json:"items"`
		} `json:"outer_row"`
	} `json:"sub_cell_selector"`
}

func parseAccountShortRank(body []byte, contentId, boardId string, cur rankCursor) (*model.ShortRanking, error) {
	var env struct {
		Code json.RawMessage `json:"code"`
		Data struct {
			NextOffset int    `json:"next_offset"`
			HasMore    bool   `json:"has_more"`
			SessionID  string `json:"session_id"`
			CellView   struct {
				Selector struct {
					OuterRow struct {
						Items []shortRankOption `json:"items"`
					} `json:"outer_row"`
				} `json:"cell_selector"`
				CellData []struct {
					VideoData []struct {
						SeriesID     shortRankID `json:"series_id"`
						Title        string      `json:"title"`
						Cover        string      `json:"cover"`
						EpisodeCount int         `json:"episode_cnt"`
						Score        struct {
							Text string `json:"RecommendText"`
						} `json:"rec_text_item"`
					} `json:"video_data"`
					Users []struct {
						Info struct {
							Base struct {
								Name    string      `json:"user_name"`
								Avatar  string      `json:"user_avatar"`
								ActorID shortRankID `json:"actor_id_str"`
								UserID  shortRankID `json:"user_id"`
							} `json:"base_info"`
						} `json:"user_info"`
						Score string `json:"intro"`
						Rec   struct {
							Text string `json:"RecommendText"`
						} `json:"rec_text_item"`
						SubTitles []struct {
							Content string `json:"content"`
						} `json:"sub_title_list"`
					} `json:"ugc_user_data"`
				} `json:"cell_data"`
			} `json:"cell_view"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if code := strings.Trim(strings.TrimSpace(string(env.Code)), `"`); code != "" && code != "0" {
		return nil, fmt.Errorf("ranking API returned code %s", code)
	}

	out := &model.ShortRanking{Items: []model.RankItem{}}
	for _, item := range env.Data.CellView.Selector.OuterRow.Items {
		content := model.RankContent{ID: string(item.ID), Name: item.Name, Selected: item.Selected}
		for _, board := range item.Sub.OuterRow.Items {
			content.Boards = append(content.Boards, model.RankBoard{
				ID: string(board.ID), Name: board.Name, Selected: board.Selected,
			})
		}
		if content.ID != "" {
			out.Contents = append(out.Contents, content)
		}
	}

	for _, cell := range env.Data.CellView.CellData {
		for _, video := range cell.VideoData {
			if video.SeriesID == "" || strings.TrimSpace(video.Title) == "" {
				continue
			}
			out.Items = append(out.Items, model.RankItem{
				Position:     len(out.Items) + 1,
				SeriesID:     string(video.SeriesID),
				Title:        strings.TrimSpace(video.Title),
				CoverURL:     video.Cover,
				Score:        video.Score.Text,
				EpisodeCount: video.EpisodeCount,
			})
		}
		for _, user := range cell.Users {
			profileID := string(user.Info.Base.UserID)
			if profileID == "" {
				profileID = string(user.Info.Base.ActorID)
			}
			if profileID == "" || strings.TrimSpace(user.Info.Base.Name) == "" {
				continue
			}
			item := model.RankItem{
				Position:  len(out.Items) + 1,
				ActorID:   string(user.Info.Base.ActorID),
				UserID:    profileID,
				Actor:     strings.TrimSpace(user.Info.Base.Name),
				AvatarURL: user.Info.Base.Avatar,
				Score:     user.Rec.Text,
				Intro:     user.Score,
			}
			for _, subtitle := range user.SubTitles {
				if subtitle.Content != "" {
					item.Details = append(item.Details, subtitle.Content)
				}
			}
			out.Items = append(out.Items, item)
		}
	}

	for _, content := range out.Contents {
		if !content.Selected {
			continue
		}
		out.Content = content.ID
		for _, board := range content.Boards {
			if board.Selected {
				out.Board = board.ID
			}
		}
	}
	if out.Content == "" {
		out.Content = contentId
	}
	if out.Board == "" {
		out.Board = boardId
	}
	for i := range out.Items {
		out.Items[i].Position += cur.Offset
	}
	if env.Data.HasMore && env.Data.NextOffset > cur.Offset && len(out.Items) > 0 {
		next := cur
		next.Offset = env.Data.NextOffset
		next.Session = env.Data.SessionID
		next.Content = out.Content
		next.Board = out.Board
		for _, item := range out.Items {
			id := item.SeriesID
			if id == "" {
				id = item.ActorID
			}
			if id != "" {
				next.Seen = append(next.Seen, id)
			}
		}
		out.Cursor, _ = encodeRankCursor(next)
		out.HasMore = out.Cursor != ""
	}
	return out, nil
}
