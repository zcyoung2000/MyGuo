package catalog

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"myguo/internal/resourcecache"
)

func TestInvalidatePrefixesOnlyRemovesMatchingEntries(t *testing.T) {
	service := NewService(nil, nil, nil, Dependencies{})
	service.cache.Set("new_releases:today", resourcecache.Item{Value: "stale"})
	service.cache.Set("new_releases:tomorrow", resourcecache.Item{Value: "stale"})
	service.cache.Set("hot_search", resourcecache.Item{Value: "keep", ExpiresAt: time.Now().Add(time.Minute)})

	service.InvalidatePrefixes("new_releases:")

	if _, ok := service.cache.Get("new_releases:today"); ok {
		t.Fatal("matching entry was not invalidated")
	}
	if _, ok := service.cache.Get("new_releases:tomorrow"); ok {
		t.Fatal("matching entry was not invalidated")
	}
	if item, ok := service.cache.Get("hot_search"); !ok || item.Value != "keep" {
		t.Fatalf("unmatched entry = %#v, %v", item, ok)
	}
}

func TestExpiredCatalogCacheIsNotServed(t *testing.T) {
	service := NewService(nil, nil, nil, Dependencies{})
	service.cache.Set("expired", resourcecache.Item{Value: "old", ExpiresAt: time.Now().Add(-time.Second)})
	service.cache.Set("fresh", resourcecache.Item{Value: "new", ExpiresAt: time.Now().Add(time.Minute)})

	expired, expiredOK := service.cache.Get("expired")
	fresh, freshOK := service.cache.Get("fresh")
	if expiredOK {
		t.Fatal("expired entry is still considered valid")
	}
	if !freshOK {
		t.Fatal("fresh entry was rejected")
	}
	if fresh.Value != "new" || expired.Value != nil {
		t.Fatalf("cache vals = %v/%v", expired.Value, fresh.Value)
	}
}

func TestCatalogMetadataCacheEvictsAndRejectsOversizedKeys(t *testing.T) {
	service := NewService(nil, nil, nil, Dependencies{})
	keys := make([]string, 0, metadataCacheCapacity+1)
	for index := 0; index <= metadataCacheCapacity; index++ {
		key := fmt.Sprintf("catalog:%04d", index)
		keys = append(keys, key)
		service.cache.Set(key, resourcecache.Item{Value: index, ExpiresAt: time.Now().Add(time.Minute)})
	}
	if service.cache.Len() != metadataCacheCapacity {
		t.Fatalf("cache length = %d, want %d", service.cache.Len(), metadataCacheCapacity)
	}
	if _, ok := service.cache.Get(keys[0]); ok {
		t.Fatal("oldest entry was not evicted")
	}
	if item, ok := service.cache.Get(keys[len(keys)-1]); !ok || item.Value != metadataCacheCapacity {
		t.Fatalf("newest entry = %#v, %v", item, ok)
	}

	oversized := make([]byte, resourcecache.MaxKeyLength+1)
	if service.cache.Set(string(oversized), resourcecache.Item{Value: "rejected"}) {
		t.Fatal("oversized cache key was accepted")
	}
}

func TestShortFilterSelections(t *testing.T) {
	tests := []struct {
		name   string
		genre  string
		values map[string]string
		want   map[string][]string
	}{
		{
			name:   "uses every selected filter row",
			genre:  "short_play",
			values: map[string]string{"category_dim_role": "cate_26", "category_dim_epoch": "cate_127", "sort": ""},
			want: map[string][]string{
				"genre":              {"short_play"},
				"category_dim_role":  {"cate_26"},
				"category_dim_epoch": {"cate_127"},
			},
		},
		{
			name:   "frontend genre overrides any stale row value",
			genre:  "comic_series",
			values: map[string]string{"genre": "short_play"},
			want:   map[string][]string{"genre": {"comic_series"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortFilterSelections(tt.genre, tt.values)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("shortFilterSelections() = %v, want %v", got, tt.want)
			}
		})
	}
}
