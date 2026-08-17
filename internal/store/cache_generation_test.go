package store

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestCacheGenerationRejectsStaleMissResults(t *testing.T) {
	t.Run("site", func(t *testing.T) {
		store := newSiteStore(nil)
		generation := store.generation
		store.ClearCache()
		store.cachePutIfGeneration(&domain.Site{ID: 1, Name: "stale"}, generation)
		if _, ok := store.byID[1]; ok {
			t.Fatal("stale site query repopulated the cache")
		}
	})

	t.Run("credential", func(t *testing.T) {
		store := newCredentialStore(nil)
		generation := store.generation
		store.ClearCache()
		credential := domain.Credential{ID: 1, SiteID: 2, Kind: "api_key"}
		store.cachePutIfGeneration(&credential, generation)
		store.cacheSiteKeysIfGeneration(2, []domain.Credential{credential}, generation)
		if _, ok := store.byID[1]; ok {
			t.Fatal("stale credential query repopulated the id cache")
		}
		if _, ok := store.bySiteKeys[2]; ok {
			t.Fatal("stale credential pool query repopulated the site cache")
		}
	})

	t.Run("downstream key", func(t *testing.T) {
		store := newDownstreamKeyStore(nil)
		generation := store.generation
		store.ClearCache()
		store.cachePutIfGeneration(&domain.DownstreamKey{ID: 1, TokenHash: "stale"}, generation)
		if _, ok := store.byID[1]; ok {
			t.Fatal("stale downstream key query repopulated the id cache")
		}
		if _, ok := store.byHash["stale"]; ok {
			t.Fatal("stale downstream key query repopulated the auth cache")
		}
	})

	t.Run("group", func(t *testing.T) {
		store := newGroupStore(nil)
		generation := store.generation
		store.ClearCache()
		store.cachePutIfGeneration(&domain.KeyGroup{Name: "stale"}, generation)
		if _, ok := store.cache["stale"]; ok {
			t.Fatal("stale group query repopulated the cache")
		}
	})

	t.Run("model ratio", func(t *testing.T) {
		store := newModelRatioStore(nil)
		generation := store.generation
		store.ClearCache()
		store.cachePutIfGeneration("stale", 2, generation)
		if _, ok := store.cache["stale"]; ok {
			t.Fatal("stale model ratio query repopulated the cache")
		}
	})
}
