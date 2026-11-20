package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFeedQueueSurvivesRestartWithoutReorderingOrMixingChannels(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries := []FeedQueueEntry{
		{SeriesID: "a", Payload: json.RawMessage(`{"title":"A"}`)},
		{SeriesID: "b", Payload: json.RawMessage(`{"title":"B"}`)},
		{SeriesID: "c", Payload: json.RawMessage(`{"title":"C"}`)},
	}
	if err := s.AppendFeedQueue("推荐", entries); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendFeedQueue("漫剧", entries[:1]); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendFeedQueue("推荐", entries[:1]); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check := func(channel, want string) {
		t.Helper()
		items, err := s.GetFeedQueue(channel)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, item := range items {
			ids = append(ids, item.SeriesID)
			if !json.Valid(item.Payload) {
				t.Fatalf("invalid persisted metadata: %s", item.Payload)
			}
		}
		if strings.Join(ids, ",") != want {
			t.Fatalf("queue %s = %v, want %s", channel, ids, want)
		}
	}
	check("推荐", "a,b,c")
	if err := s.ConsumeFeedQueue("推荐", "b"); err != nil {
		t.Fatal(err)
	}
	check("推荐", "c")
	check("漫剧", "a")
	// A delayed acknowledgement must not delete the remaining unseen tail.
	if err := s.ConsumeFeedQueue("推荐", "a"); err != nil {
		t.Fatal(err)
	}
	check("推荐", "c")
	history, err := s.GetHistory()
	if err != nil || len(history) != 0 {
		t.Fatalf("queue operations must not invent viewing history: %+v, %v", history, err)
	}
}

func TestUnavailableWorkRejectionKeepsOtherPendingItemsInEveryChannel(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, channel := range []string{"推荐", "漫剧"} {
		if err := s.AppendFeedQueue(channel, []FeedQueueEntry{{SeriesID: "good", Payload: json.RawMessage(`{}`)}, {SeriesID: "gone", Payload: json.RawMessage(`{}`)}, {SeriesID: "next", Payload: json.RawMessage(`{}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RemoveUnavailableFeedSeries("gone"); err != nil {
		t.Fatal(err)
	}
	for _, channel := range []string{"推荐", "漫剧"} {
		items, err := s.GetFeedQueue(channel)
		if err != nil || len(items) != 2 || items[0].SeriesID != "good" || items[1].SeriesID != "next" {
			t.Fatalf("rejection consumed adjacent works in %s: %+v / %v", channel, items, err)
		}
	}
}
